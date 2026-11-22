package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/lightwebinc/multicast-test/harness/env"
	"github.com/lightwebinc/multicast-test/harness/metrics"
)

// Scenario 12: Burst gap + rate limiting
//
// Ported from the retired bash scenario 12.
//
// Frequent multi-frame gaps, each filled late (500ms) by the generator, and 5%
// netem loss create a NACK flood. Verifies that the retry endpoint's rate
// limiter activates AND some retransmits still succeed, and that every gap
// still closes, by a retransmit or its late fill, with none lost.
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

	genCmd := subtxGenCmd("[fd10::2]:8725")
	genCmd = append(genCmd,
		"-pps", "500",
		"-duration", "15s",
		"-seq-gap-every", "50",
		"-seq-gap-size", "3",
		"-seq-gap-delay", "500ms",
	)
	startGenerator(t, ctx, "s12", genCmd)
	waitGenerator(t, ctx, "s12")

	// Settle: every gap closed or booked unrecovered, and counters still.
	afterL := settleRecovery(t, e, ctx, "s12", beforeL, 90*time.Second)
	urlR := e.MetricsURL(ctx, "s12-retry1")
	afterR := metrics.ScrapeOrFail(t, urlR)
	e.LogContainerOutput(ctx, "s12-source")
	late := generatorLate(t, ctx, "s12")

	gapsDetected := sumListenerDelta("s12", "bsl_gaps_detected_total", beforeL, afterL)
	nacksDispatched := sumListenerDelta("s12", "bsl_nacks_dispatched_total", beforeL, afterL)

	deltaR := metrics.DeltaMap(beforeR, afterR)
	nacksReceived := deltaR["bre_nack_requests_total"]
	rateDrops := deltaR["bre_rate_limit_drops_total"]

	g := newGapRepair(beforeL, afterL, beforeR, afterR, late)
	g.log(t)
	logGapOutcomes(t, e, ctx, "s12")
	t.Logf("gaps_detected=%.0f nacks=%.0f; retry: nacks_received=%.0f rate_drops=%.0f",
		gapsDetected, nacksDispatched, nacksReceived, rateDrops)

	metrics.AssertGT(t, "gaps detected", gapsDetected)
	metrics.AssertGT(t, "NACKs dispatched", nacksDispatched)
	metrics.AssertGT(t, "NACKs received", nacksReceived)
	// Rate limiter should fire under burst load.
	metrics.AssertGT(t, "rate limit drops (burst activated)", rateDrops)
	// Some retransmits must still succeed.
	metrics.AssertGT(t, "retransmits served", g.retransmits)
	metrics.AssertGT(t, "late gap fills sent", late)
	g.assertNothingLost(t)
	assertNoRestartFlush(t, e, ctx, "s12")
	g.assertAccounted(t, 0.10)
}
