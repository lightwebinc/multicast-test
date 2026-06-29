package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/lightwebinc/multicast-test/harness/env"
	"github.com/lightwebinc/multicast-test/harness/metrics"
)

// TestScenario91_CoalesceLossRecovery proves BRC-142 bundle-unit NACK recovery
// on real binaries: under multicast loss, the listener detects gaps in the
// *bundle* SeqNum stream, NACKs the missing bundle, and the retry endpoint —
// which cached the bundle opaquely by (HashKey, SeqNum) — retransmits it whole
// (deriving the group from the bundle header). The recovered bundle is
// re-decoalesced and the gap closes.
//
// netem loss is applied to the listener containers only; the retry endpoint
// receives every bundle and keeps a warm cache, so missed bundles recover.
func TestScenario91_CoalesceLossRecovery(t *testing.T) {
	ctx := context.Background()
	e, _ := retryTopology(t, "s91")

	e.PatchEnv("s91-proxy", map[string]string{
		"SHARD_BITS":         "1",
		"NUM_WORKERS":        "1", // single bundle stream per flow
		"COALESCE":           "true",
		"COALESCE_MAX_BYTES": "1400",
	})

	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle + multicast group joins")

	// Drop ~3% of inbound multicast at each listener → bundle SeqNum gaps → NACKs.
	listeners := []string{"s91-listener1", "s91-listener2", "s91-listener3"}
	for _, l := range listeners {
		if err := env.ApplyNetemLoss(ctx, l, 3.0); err != nil {
			t.Fatalf("apply netem loss on %s: %v", l, err)
		}
		l := l
		t.Cleanup(func() { env.RemoveNetemLoss(context.Background(), l) }) //nolint:errcheck
	}

	beforeL := snapshotListeners(t, e, ctx, "s91")
	beforeR := e.Snapshot(ctx, "s91-retry1")

	// Low, steady rate for deterministic recovery (the retry pipeline is the
	// thing under test, not throughput). Single (group, subtree) flow so the
	// bundle SeqNum stream is contiguous and gaps are unambiguous.
	genCmd := []string{
		"-addr", "[fd10::2]:8725",
		"-shard-bits", "1",
		"-subtrees", "1",
		"-subtree-seed", "multicast-lab-bsv",
		"-pps", "300",
		"-duration", "12s",
		"-payload-size", "200",
		"-log-interval", "3s",
	}
	startGenerator(t, ctx, "s91", genCmd)
	waitGenerator(t, ctx, "s91")

	// Remove loss BEFORE the recovery drain: the generator has stopped (no new
	// gaps), so a clean window lets retransmitted bundles arrive without being
	// re-dropped (otherwise netem drops the retransmits too and gaps age out
	// unrecovered). Also makes the metrics scrape reliable.
	for _, l := range listeners {
		env.RemoveNetemLoss(ctx, l) //nolint:errcheck
	}
	e.Sleep(7*time.Second, "NACK + retransmit recovery drain (loss removed)")

	afterL := scrapeListeners(t, e, ctx, "s91")
	afterR := metrics.ScrapeOrFail(t, e.MetricsURL(ctx, "s91-retry1"))

	gapsDetected := sumListenerDelta("s91", "bsl_gaps_detected_total", beforeL, afterL)
	nacksDispatched := sumListenerDelta("s91", "bsl_nacks_dispatched_total", beforeL, afterL)
	gapsUnrecovered := sumListenerDelta("s91", "bsl_gaps_unrecovered_total", beforeL, afterL)

	deltaR := metrics.DeltaMap(beforeR, afterR)
	nackRequests := deltaR["bre_nack_requests_total"]
	cacheHits := deltaR["bre_cache_hits_total"]
	retransmits := deltaR["bre_retransmits_total"]

	// The full bundle-unit recovery loop fired (these prove the BRC-142 path;
	// the exact recovery *rate* is a tuning property of loss/TTL/pps, already
	// characterised by the sims and the non-bundle NACK scenarios, so it is
	// logged, not asserted — it varies with host load):
	//   listener gap-tracks the BUNDLE SeqNum stream and NACKs (P2),
	//   retry caches the bundle opaquely and retransmits it whole (P3),
	//   the re-decoalesced bundle closes the gap.
	recovered := gapsDetected - gapsUnrecovered
	metrics.AssertGT(t, "listener gaps detected", gapsDetected)
	metrics.AssertGT(t, "listener NACKs dispatched", nacksDispatched)
	metrics.AssertGT(t, "retry NACK requests received", nackRequests)
	metrics.AssertGT(t, "retry cache hits (bundle found in cache)", cacheHits)
	metrics.AssertGT(t, "retry bundle retransmits", retransmits)
	metrics.AssertGTE(t, "retransmits per cache hit", retransmits, cacheHits*0.9) // each served bundle is retransmitted
	metrics.AssertGT(t, "gaps recovered via bundle retransmit", recovered)

	if gapsDetected > 0 {
		t.Logf("BRC-142 recovery: %.0f bundle-stream gaps, %.0f NACKs → %.0f retry cache hits, %.0f bundle retransmits; %.0f recovered, %.0f unrecovered (%.0f%% recovered)",
			gapsDetected, nacksDispatched, cacheHits, retransmits, recovered, gapsUnrecovered,
			100*recovered/gapsDetected)
	}
}
