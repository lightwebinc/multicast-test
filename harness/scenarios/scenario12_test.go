package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/lightwebinc/bitcoin-multicast-test/harness/env"
	"github.com/lightwebinc/bitcoin-multicast-test/harness/metrics"
)

// Scenario 12 — Burst gap + rate limiting
//
// Mirrors scenarios/12-burst-gap-ratelimit/run.sh.
//
// Frequent multi-frame gaps create a NACK flood. Verifies that the retry
// endpoint's rate limiter activates AND some retransmits still succeed.
func TestScenario12_BurstGapRatelimit(t *testing.T) {
	ctx := context.Background()
	e, _ := retryTopology(t, "s12")
	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle")

	// 5% loss on listeners.
	for _, l := range []string{"s12-listener1", "s12-listener2", "s12-listener3"} {
		if err := env.ApplyNetemLoss(ctx, l, 5.0); err != nil {
			t.Fatalf("netem loss %s: %v", l, err)
		}
		t.Cleanup(func() { env.RemoveNetemLoss(ctx, l) }) //nolint:errcheck
	}

	e.Sleep(3*time.Second, "stabilise")

	beforeL := snapshotListeners(t, e, ctx, "s12")
	beforeR := e.Snapshot(ctx, "s12-retry1")

	genCmd := subtxGenCmd("[fd10::2]:9000")
	genCmd = append(genCmd,
		"-pps", "500",
		"-duration", "15s",
		"-seq-gap-every", "50",
		"-seq-gap-size", "3",
		"-seq-gap-delay", "500ms",
	)
	startGenerator(t, ctx, "s12", genCmd)
	waitGenerator(t, ctx, "s12")

	e.Sleep(5*time.Second, "NACK pipeline drain")

	afterL := scrapeListeners(t, e, ctx, "s12")
	urlR := e.MetricsURL(ctx, "s12-retry1")
	afterR := metrics.ScrapeOrFail(t, urlR)
	e.LogContainerOutput(ctx, "s12-source")

	gapsDetected := sumListenerDelta("s12", "bsl_gaps_detected_total", beforeL, afterL)
	nacksDispatched := sumListenerDelta("s12", "bsl_nacks_dispatched_total", beforeL, afterL)
	gapsSuppressed := sumListenerDelta("s12", "bsl_gaps_suppressed_total", beforeL, afterL)

	deltaR := metrics.DeltaMap(beforeR, afterR)
	nacksReceived := deltaR["bre_nack_requests_total"]
	rateDrops := deltaR["bre_rate_limit_drops_total"]
	retransmits := deltaR["bre_retransmits_total"]

	t.Logf("gaps_detected=%.0f nacks=%.0f suppressed=%.0f",
		gapsDetected, nacksDispatched, gapsSuppressed)
	t.Logf("retry: nacks_received=%.0f rate_drops=%.0f retransmits=%.0f",
		nacksReceived, rateDrops, retransmits)

	metrics.AssertGT(t, "gaps detected", gapsDetected)
	metrics.AssertGT(t, "NACKs dispatched", nacksDispatched)
	metrics.AssertGT(t, "NACKs received", nacksReceived)
	// Rate limiter should fire under burst load.
	metrics.AssertGT(t, "rate limit drops (burst activated)", rateDrops)
	// Some retransmits must still succeed.
	metrics.AssertGT(t, "retransmits", retransmits)
	// Some gaps should be recovered despite rate limiting.
	metrics.AssertGT(t, "gaps suppressed", gapsSuppressed)
}
