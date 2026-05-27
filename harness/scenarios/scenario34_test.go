package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/lightwebinc/multicast-test/harness/env"
	"github.com/lightwebinc/multicast-test/harness/metrics"
)

// Scenario 34 — BRC-132 subtree data: NACK retransmission
//
// Mirrors scenarios/34-subtree-data-retransmit/run.sh.
//
// 10% loss on listeners + BRC-132 frames. Retry endpoint caches them.
// Listeners detect gaps and NACK → retransmit fills them.
func TestScenario34_SubtreeDataRetransmit(t *testing.T) {
	ctx := context.Background()
	e, _ := retryTopology(t, "s34")
	// TXID_DEDUP_LOCAL_CAP=0 disables the proxy's ingress dedup so multiple
	// V5 frames sharing a SubtreeID (the dedup key for BRC-132) all flow and
	// build a per-SubtreeID SeqNum chain that gap detection can observe.
	e.PatchEnv("s34-proxy", map[string]string{
		"TCP_LISTEN_PORT":      "9002",
		"TXID_DEDUP_LOCAL_CAP": "0",
	})
	for _, l := range []string{"s34-listener1", "s34-listener2", "s34-listener3"} {
		e.PatchEnv(l, map[string]string{"SUBTREE_DATA_ENABLED": "true"})
	}
	// Retry endpoint must also opt in to cache BRC-132 V5 frames; without
	// this it joins only shard groups and never caches CtrlGroupSubtreeAnnounce.
	e.PatchEnv("s34-retry1", map[string]string{"SUBTREE_DATA_ENABLED": "true"})
	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle")

	// 10% loss on listeners.
	for _, l := range []string{"s34-listener1", "s34-listener2", "s34-listener3"} {
		if err := env.ApplyNetemLoss(ctx, l, 10.0); err != nil {
			t.Fatalf("netem loss %s: %v", l, err)
		}
		t.Cleanup(func() { env.RemoveNetemLoss(ctx, l) }) //nolint:errcheck
	}

	beforeL := snapshotListeners(t, e, ctx, "s34")
	beforeR := e.Snapshot(ctx, "s34-retry1")

	genCmd := []string{
		"send-subtree-data",
		"-addr", "[fd10::2]:9002",
		"-frames", "60",
		"-nodes", "8",
		"-subtree-count", "8",
		"-msg-type", "hashes",
		"-interval", "80ms",
	}
	startGenerator(t, ctx, "s34", genCmd)
	waitGenerator(t, ctx, "s34")

	e.Sleep(10*time.Second, "NACK pipeline drain")

	afterL := scrapeListeners(t, e, ctx, "s34")
	urlR := e.MetricsURL(ctx, "s34-retry1")
	afterR := metrics.ScrapeOrFail(t, urlR)

	gapsDetected := sumListenerDelta("s34", "bsl_gaps_detected_total", beforeL, afterL)
	nacksDispatched := sumListenerDelta("s34", "bsl_nacks_dispatched_total", beforeL, afterL)
	framesRecv := sumListenerDelta("s34", "bsl_frames_received_total", beforeL, afterL)

	deltaR := metrics.DeltaMap(beforeR, afterR)
	retransmits := deltaR["bre_retransmits_total"]
	reFramesRecv := deltaR["bre_frames_received_total"]
	reCached := deltaR["bre_frames_cached_total"]

	t.Logf("listeners: frames_recv=%.0f gaps=%.0f nacks=%.0f", framesRecv, gapsDetected, nacksDispatched)
	t.Logf("retry: frames_recv=%.0f cached=%.0f retransmits=%.0f", reFramesRecv, reCached, retransmits)

	metrics.AssertGT(t, "gaps detected", gapsDetected)
	metrics.AssertGT(t, "NACKs dispatched", nacksDispatched)
	metrics.AssertGT(t, "retransmits", retransmits)
}
