package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/lightwebinc/bitcoin-multicast-test/harness/env"
	"github.com/lightwebinc/bitcoin-multicast-test/harness/metrics"
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
	e.PatchEnv("s34-proxy", map[string]string{"TCP_LISTEN_PORT": "9002"})
	for _, l := range []string{"s34-listener1", "s34-listener2", "s34-listener3"} {
		e.PatchEnv(l, map[string]string{"SUBTREE_DATA_ENABLED": "true"})
	}
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
		"-frames", "50",
		"-nodes", "8",
		"-msg-type", "hashes",
		"-interval", "50ms",
	}
	startGenerator(t, ctx, "s34", genCmd)
	waitGenerator(t, ctx, "s34")

	e.Sleep(10*time.Second, "NACK pipeline drain")

	afterL := scrapeListeners(t, e, ctx, "s34")
	urlR := e.MetricsURL(ctx, "s34-retry1")
	afterR := metrics.ScrapeOrFail(t, urlR)

	gapsDetected := sumListenerDelta("s34", "bsl_gaps_detected_total", beforeL, afterL)
	nacksDispatched := sumListenerDelta("s34", "bsl_nacks_dispatched_total", beforeL, afterL)

	deltaR := metrics.DeltaMap(beforeR, afterR)
	retransmits := deltaR["bre_retransmits_total"]

	t.Logf("gaps_detected=%.0f nacks=%.0f retransmits=%.0f",
		gapsDetected, nacksDispatched, retransmits)

	metrics.AssertGT(t, "gaps detected", gapsDetected)
	metrics.AssertGT(t, "NACKs dispatched", nacksDispatched)
	metrics.AssertGT(t, "retransmits", retransmits)
}
