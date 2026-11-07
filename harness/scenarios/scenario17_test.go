package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/lightwebinc/multicast-test/harness/env"
	"github.com/lightwebinc/multicast-test/harness/metrics"
)

// Scenario 17: Recovery shortfall identity (criterion #1, clean regime)
//
// For every unfiltered listener, EXACTLY
//
//	delivered == sent   (sent − delivered == 0, so nothing was permanently lost)
//
// where sent = proxy bsp_packets_forwarded_total (pre-loss) and delivered =
// bsl_frames_forwarded_total + bsl_egress_errors_total, plus exact gap
// closure (detected == suppressed + unrecovered). A run that loses frames
// without repairing them, or suppresses gaps without the repair actually
// arriving, cannot pass. Scenario 18 asserts the unrecovered > 0 half of the
// identity (permanent MISS loss).
//
// This scenario originally caught a real tracker defect (2026-08-21): a
// unicast repair filling a gap at a seqNum below the proxy-restart threshold
// fell through to the restart heuristic, flushing every other pending gap to
// unrecovered (phantoms) and re-baselining the flow, 7–56 phantom
// unrecovered per listener while delivered == sent held exactly. Fixed in
// shard-listener nack.Observe (repairs return after auto-fill; only live-
// source frames can signal a restart; regression tests in nack_test.go), so
// the FULL identity is asserted here: unrecovered == sent − delivered == 0.
//
// Four preconditions make the identity exact (each guards a real leak):
//  1. Unicast-only repair, a multicast repair duplicates delivery on
//     listeners that never lost the frame (egress dedup defaults off) and a
//     trusted bare ACK can cancel a gap whose repair was itself lost.
//  2. No -seq-gap-* injection, injected seqnums are pre-stamped and PRESERVED
//     by the proxy, never sent by anyone, so each becomes a phantom permanent
//     gap with no sent−delivered counterpart.
//  3. Lossless WARMUP before the measurement window, the tracker baselines a
//     flow on its first observed frame, so a first-frame loss is invisible.
//  4. Lossless TRAILER after loss removal, tail-loss detection needs 16
//     contiguous frames for a probe; trailer frames extend every chain so tail
//     losses become ordinary interior gaps.
func TestScenario17_RecoveryShortfallIdentity(t *testing.T) {
	ctx := context.Background()
	e := retryTopologyNACKEnv(t, "s17", nil)
	e.PatchEnv("s17-retry1", map[string]string{
		"BEACON_FLAGS_MULTICAST": "false",
		"BEACON_FLAGS_UNICAST":   "true",
		// Rate limits out of the way: a silently rate-dropped NACK burns one
		// of the listener's 5 retry rounds, and a gap that exhausts its rounds
		// under contention gets abandoned, after which a later range-fill can
		// STILL deliver the frame (delivered but booked unrecovered, breaking
		// the identity). The clean regime must actually be clean: every NACK
		// answered within its round.
		"RL_IP_RATE":         "50000",
		"RL_IP_BURST":        "10000",
		"RL_CHAIN_RATE":      "10000",
		"RL_CHAIN_WINDOW":    "60s",
		"RL_SEQUENCE_MAX":    "10000",
		"RL_SEQUENCE_WINDOW": "60s",
		"RL_GROUP_RATE":      "10000",
		"RL_GROUP_BURST":     "10000",
	})
	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle")

	// Lossless warmup: baseline every flow before loss exists.
	warm := subtxGenCmd("[fd10::2]:8725")
	warm = append(warm, "-pps", "500", "-duration", "3s")
	startGenerator(t, ctx, "s17", warm)
	waitGenerator(t, ctx, "s17")
	e.Sleep(2*time.Second, "warmup drain (quiet point)")

	beforeL := snapshotListeners(t, e, ctx, "s17")
	beforeP := e.Snapshot(ctx, "s17-proxy")
	beforeR := e.Snapshot(ctx, "s17-retry1")

	for _, l := range []string{"s17-listener1", "s17-listener2", "s17-listener3"} {
		if err := env.ApplyNetemLoss(ctx, l, 2.0); err != nil {
			t.Fatalf("netem loss %s: %v", l, err)
		}
		t.Cleanup(func() { env.RemoveNetemLoss(ctx, l) }) //nolint:errcheck
	}

	genCmd := subtxGenCmd("[fd10::2]:8725")
	genCmd = append(genCmd, "-pps", "1000", "-duration", "10s")
	startGenerator(t, ctx, "s17", genCmd)
	waitGenerator(t, ctx, "s17")

	for _, l := range []string{"s17-listener1", "s17-listener2", "s17-listener3"} {
		env.RemoveNetemLoss(ctx, l) //nolint:errcheck
	}

	// Lossless trailer: extend every chain past its last loss.
	trail := subtxGenCmd("[fd10::2]:8725")
	trail = append(trail, "-pps", "500", "-duration", "3s")
	startGenerator(t, ctx, "s17", trail)
	waitGenerator(t, ctx, "s17")

	afterL := settleRecovery(t, e, ctx, "s17", beforeL, 30*time.Second)
	afterP := metrics.ScrapeOrFail(t, e.MetricsURL(ctx, "s17-proxy"))
	afterR := metrics.ScrapeOrFail(t, e.MetricsURL(ctx, "s17-retry1"))
	e.LogContainerOutput(ctx, "s17-source")

	deltaP := metrics.DeltaMap(beforeP, afterP)
	sent := deltaP["bsp_packets_forwarded_total"]
	proxyDropped := deltaP["bsp_packets_dropped_total"]
	gapsDetected := sumListenerDelta("s17", "bsl_gaps_detected_total", beforeL, afterL)
	nacksDispatched := sumListenerDelta("s17", "bsl_nacks_dispatched_total", beforeL, afterL)
	unrecovered := sumListenerDelta("s17", "bsl_gaps_unrecovered_total", beforeL, afterL)
	deltaR := metrics.DeltaMap(beforeR, afterR)
	framesCached := deltaR["bre_frames_cached_total"]
	unicastRetransmits := deltaR["bre_unicast_retransmits_total"]
	nacksReceived := deltaR["bre_nack_requests_total"]
	cacheMisses := deltaR["bre_cache_misses_total"]
	rateDrops := deltaR["bre_rate_limit_drops_total"]

	t.Logf("proxy: sent=%.0f dropped=%.0f", sent, proxyDropped)
	t.Logf("listeners: gaps_detected=%.0f nacks_dispatched=%.0f unrecovered=%.0f",
		gapsDetected, nacksDispatched, unrecovered)
	for i := 0; i < 3; i++ {
		d := metrics.DeltaMap(beforeL[i], afterL[i])
		t.Logf("listener%d: recv=%.0f fwd=%.0f egr_err=%.0f det=%.0f sup=%.0f unrec=%.0f",
			i+1, d["bsl_frames_received_total"], d["bsl_frames_forwarded_total"],
			d["bsl_egress_errors_total"], d["bsl_gaps_detected_total"],
			d["bsl_gaps_suppressed_total"], d["bsl_gaps_unrecovered_total"])
	}
	t.Logf("retry: cached=%.0f nacks_received=%.0f unicast_retransmits=%.0f cache_misses=%.0f rate_drops=%.0f",
		framesCached, nacksReceived, unicastRetransmits, cacheMisses, rateDrops)

	metrics.AssertGT(t, "proxy sent frames", sent)
	// Loss must actually have created gaps, or the identity passes vacuously.
	metrics.AssertGT(t, "gaps detected", gapsDetected)
	// Repairs must have been SERVED as unicast (the only repair path here).
	metrics.AssertGT(t, "unicast retransmits served", unicastRetransmits)
	// Nothing may be dropped after SeqNum stamping, or `sent` undercounts the
	// seqnum space the listeners track.
	metrics.AssertZero(t, "proxy post-stamp drops", proxyDropped)

	for i, name := range []string{"listener1", "listener2", "listener3"} {
		// The full criterion-#1 identity: closure exact AND
		// unrecovered == sent − delivered.
		assertRecoveryIdentity(t, name, beforeL[i], afterL[i], sent)
		// Clean regime on top: every stamped frame was delivered, netem loss
		// fully repaired, exactly, no tolerance (so unrecovered above is 0).
		d := metrics.DeltaMap(beforeL[i], afterL[i])
		delivered := d["bsl_frames_forwarded_total"] + d["bsl_egress_errors_total"]
		metrics.AssertEq(t, name+" delivered == sent (loss fully repaired)", delivered, sent)
	}
}
