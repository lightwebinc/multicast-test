package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/lightwebinc/multicast-test/harness/env"
	"github.com/lightwebinc/multicast-test/harness/metrics"
)

// Scenario 18: Unrecoverable-loss (cache MISS) shortfall identity
//
// Scenario 17's exact identity in the regime where repair genuinely CANNOT
// happen: the retry endpoint's multicast ingress is blocked (scenario 11's
// mechanism), so its cache is empty, every NACK MISSes, and each gap evicts
// as unrecovered once the listener exhausts its retry rounds. The identity
//
//	gaps_unrecovered == sent − delivered
//
// must hold EXACTLY with unrecovered large, honest loss accounting when
// nothing can be repaired. This is the harness analogue of the lab's
// 32=32 / 21=21 / 13=13 / 9=9 runs.
//
// Deliberately NOT rate-limit starvation: "unrecovered" means the tracker gave
// up, not that the frame never arrived. A rate-limiter only DELAYS service, so
// an abandoned gap's frame can still land later via a range fill, delivered
// but booked unrecovered, and the identity over-counts (verified: 1059 booked
// vs 425 true shortfall at RL_GROUP_RATE=2). Rate-limit behaviour is covered
// by scenarios 12/16; the exact identity needs loss to be PERMANENT, which an
// empty cache provides.
//
// Same remaining preconditions as scenario 17: unicast-only repair flags, no
// injected seq-gaps, lossless warmup + trailer.
func TestScenario18_UnrecoverableLossShortfallIdentity(t *testing.T) {
	ctx := context.Background()
	e := retryTopologyNACKEnv(t, "s18", nil)
	e.PatchEnv("s18-retry1", map[string]string{
		"BEACON_FLAGS_MULTICAST": "false",
		"BEACON_FLAGS_UNICAST":   "true",
	})
	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle")

	// Empty cache: the retry hears NACKs (unicast, port 9300) but never the
	// data plane (multicast, port 9001), every NACK is a MISS forever.
	if err := env.BlockUDPIngress(ctx, "s18-retry1", 9001); err != nil {
		t.Fatalf("block retry ingress: %v", err)
	}
	t.Cleanup(func() { env.UnblockUDPIngress(ctx, "s18-retry1", 9001) }) //nolint:errcheck

	warm := subtxGenCmd("[fd10::2]:8725")
	warm = append(warm, "-pps", "500", "-duration", "3s")
	startGenerator(t, ctx, "s18", warm)
	waitGenerator(t, ctx, "s18")
	e.Sleep(2*time.Second, "warmup drain (quiet point)")

	beforeL := snapshotListeners(t, e, ctx, "s18")
	beforeP := e.Snapshot(ctx, "s18-proxy")
	beforeR := e.Snapshot(ctx, "s18-retry1")

	for _, l := range []string{"s18-listener1", "s18-listener2", "s18-listener3"} {
		if err := env.ApplyNetemLoss(ctx, l, 5.0); err != nil {
			t.Fatalf("netem loss %s: %v", l, err)
		}
		t.Cleanup(func() { env.RemoveNetemLoss(ctx, l) }) //nolint:errcheck
	}

	genCmd := subtxGenCmd("[fd10::2]:8725")
	genCmd = append(genCmd, "-pps", "1000", "-duration", "15s")
	startGenerator(t, ctx, "s18", genCmd)
	waitGenerator(t, ctx, "s18")

	for _, l := range []string{"s18-listener1", "s18-listener2", "s18-listener3"} {
		env.RemoveNetemLoss(ctx, l) //nolint:errcheck
	}

	// The trailer must be GENTLE here, unlike scenario 17's: a frame dropped
	// during the trailer (e.g. a socket-level drop under burst) lands on a
	// flow tail, its probe MISSes against the empty cache, and probe MISSes
	// are deliberately never booked unrecovered, an invisible one-frame
	// shortfall that breaks exactness. Scenario 17 is immune (its full cache
	// recovers trailer losses); with nothing repairable, pace the trailer so
	// nothing drops.
	trail := subtxGenCmd("[fd10::2]:8725")
	trail = append(trail, "-pps", "100", "-duration", "5s")
	startGenerator(t, ctx, "s18", trail)
	waitGenerator(t, ctx, "s18")

	// Every gap must walk all 5 MISS rounds (~11s from its detection) before
	// evicting unrecovered; thousands of gaps stagger, so give the settle loop
	// the same order of headroom scenario 11's flat 45s drain uses.
	afterL := settleRecovery(t, e, ctx, "s18", beforeL, 90*time.Second)
	afterP := metrics.ScrapeOrFail(t, e.MetricsURL(ctx, "s18-proxy"))
	afterR := metrics.ScrapeOrFail(t, e.MetricsURL(ctx, "s18-retry1"))
	e.LogContainerOutput(ctx, "s18-source")

	sent := metrics.DeltaMap(beforeP, afterP)["bsp_packets_forwarded_total"]
	gapsDetected := sumListenerDelta("s18", "bsl_gaps_detected_total", beforeL, afterL)
	unrecovered := sumListenerDelta("s18", "bsl_gaps_unrecovered_total", beforeL, afterL)
	deltaR := metrics.DeltaMap(beforeR, afterR)
	framesCached := deltaR["bre_frames_cached_total"]
	cacheMisses := deltaR["bre_cache_misses_total"]
	unicastRetransmits := deltaR["bre_unicast_retransmits_total"]

	t.Logf("sent=%.0f gaps_detected=%.0f unrecovered=%.0f cached=%.0f misses=%.0f unicast_retransmits=%.0f",
		sent, gapsDetected, unrecovered, framesCached, cacheMisses, unicastRetransmits)

	metrics.AssertGT(t, "proxy sent frames", sent)
	metrics.AssertGT(t, "gaps detected", gapsDetected)
	// The starvation must be total: nothing cached, every NACK a MISS,
	// nothing ever served.
	metrics.AssertZero(t, "frames cached (ingress blocked)", framesCached)
	metrics.AssertGT(t, "cache misses", cacheMisses)
	metrics.AssertZero(t, "unicast retransmits served", unicastRetransmits)
	// Real permanent loss for the identity to account for.
	metrics.AssertGT(t, "unrecovered gaps (unrepairable)", unrecovered)

	for i, name := range []string{"listener1", "listener2", "listener3"} {
		assertRecoveryIdentity(t, name, beforeL[i], afterL[i], sent)
	}
}
