package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/lightwebinc/multicast-test/harness/env"
	"github.com/lightwebinc/multicast-test/harness/metrics"
)

// Scenario 99 — NACK retransmit end-to-end
//
// Mirrors scenarios/99-nack-retransmit/run.sh.
//
// Topology: proxy + 3 listeners + 1 retry endpoint + subtx-gen.
// 1% netem loss on listeners creates gaps. Generator injects sequence gaps
// (every=200, size=1, delay=50ms). Assertions: gaps detected, NACKs sent,
// retransmits occur, gaps suppressed (recovered).
func TestScenario99_NACKRetransmit(t *testing.T) {
	ctx := context.Background()
	e, _ := retryTopology(t, "s99")
	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle")

	// Apply 1% loss on all listeners.
	for _, l := range []string{"s99-listener1", "s99-listener2", "s99-listener3"} {
		if err := env.ApplyNetemLoss(ctx, l, 1.0); err != nil {
			t.Fatalf("netem loss %s: %v", l, err)
		}
		t.Cleanup(func() { env.RemoveNetemLoss(ctx, l) }) //nolint:errcheck
	}

	beforeL := snapshotListeners(t, e, ctx, "s99")
	beforeR := e.Snapshot(ctx, "s99-retry1")

	genCmd := subtxGenCmd("[fd10::2]:9000")
	genCmd = append(genCmd,
		"-seq-gap-every", "200",
		"-seq-gap-size", "1",
		"-seq-gap-delay", "50ms",
		"-duration", "15s",
	)
	startGenerator(t, ctx, "s99", genCmd)
	waitGenerator(t, ctx, "s99")

	e.Sleep(4*time.Second, "NACK/retransmit pipeline drain")

	afterL := scrapeListeners(t, e, ctx, "s99")
	urlR := e.MetricsURL(ctx, "s99-retry1")
	afterR := metrics.ScrapeOrFail(t, urlR)

	e.LogContainerOutput(ctx, "s99-source")

	// Aggregate listener metrics.
	gapsDetected := sumListenerDelta("s99", "bsl_gaps_detected_total", beforeL, afterL)
	nacksDispatched := sumListenerDelta("s99", "bsl_nacks_dispatched_total", beforeL, afterL)
	gapsSuppressed := sumListenerDelta("s99", "bsl_gaps_suppressed_total", beforeL, afterL)

	deltaR := metrics.DeltaMap(beforeR, afterR)
	framesCached := deltaR["bre_frames_cached_total"]
	nacksReceived := deltaR["bre_nack_requests_total"]
	retransmits := deltaR["bre_retransmits_total"]

	t.Logf("gaps_detected=%.0f nacks_dispatched=%.0f gaps_suppressed=%.0f",
		gapsDetected, nacksDispatched, gapsSuppressed)
	t.Logf("retry: cached=%.0f nacks_received=%.0f retransmits=%.0f",
		framesCached, nacksReceived, retransmits)

	metrics.AssertGT(t, "retry cached frames", framesCached)
	metrics.AssertGT(t, "gaps detected", gapsDetected)
	metrics.AssertGT(t, "NACKs dispatched", nacksDispatched)
	metrics.AssertGT(t, "NACKs received by retry", nacksReceived)
	metrics.AssertGT(t, "retransmits", retransmits)
	metrics.AssertGT(t, "gaps suppressed (recovered)", gapsSuppressed)
}
