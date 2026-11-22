package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/lightwebinc/multicast-test/harness/env"
	"github.com/lightwebinc/multicast-test/harness/metrics"
)

// Scenario 99: NACK retransmit end-to-end
//
// Ported from the retired bash scenario 99.
//
// Topology: proxy + 3 listeners + 1 retry endpoint + subtx-gen.
// 1% netem loss on listeners creates gaps that retry1's cache repairs, and
// the generator skips a sequence number every 200 frames of each flow and
// sends it 50ms late. A late fill closes its gap itself; retry1 holds no copy
// before it is sent, so a NACK that beats it gets a MISS. Assertions: gaps
// detected, NACKs sent, retransmits served, late fills sent; every gap closes
// and none is lost; no more gaps close than retransmits and late fills
// account for.
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

	genCmd := subtxGenCmd("[fd10::2]:8725")
	genCmd = append(genCmd,
		"-seq-gap-every", "200",
		"-seq-gap-size", "1",
		"-seq-gap-delay", "50ms",
		"-duration", "15s",
	)
	startGenerator(t, ctx, "s99", genCmd)
	waitGenerator(t, ctx, "s99")

	// Settle: every gap closed or booked unrecovered, and counters still.
	afterL := settleRecovery(t, e, ctx, "s99", beforeL, 60*time.Second)
	urlR := e.MetricsURL(ctx, "s99-retry1")
	afterR := metrics.ScrapeOrFail(t, urlR)

	e.LogContainerOutput(ctx, "s99-source")
	late := generatorLate(t, ctx, "s99")

	// Aggregate listener metrics.
	gapsDetected := sumListenerDelta("s99", "bsl_gaps_detected_total", beforeL, afterL)
	nacksDispatched := sumListenerDelta("s99", "bsl_nacks_dispatched_total", beforeL, afterL)

	deltaR := metrics.DeltaMap(beforeR, afterR)
	framesCached := deltaR["bre_frames_cached_total"]
	nacksReceived := deltaR["bre_nack_requests_total"]

	g := newGapRepair(beforeL, afterL, beforeR, afterR, late)
	g.log(t)
	logGapOutcomes(t, e, ctx, "s99")
	t.Logf("gaps_detected=%.0f nacks_dispatched=%.0f; retry: cached=%.0f nacks_received=%.0f",
		gapsDetected, nacksDispatched, framesCached, nacksReceived)

	metrics.AssertGT(t, "retry cached frames", framesCached)
	metrics.AssertGT(t, "gaps detected", gapsDetected)
	metrics.AssertGT(t, "NACKs dispatched", nacksDispatched)
	metrics.AssertGT(t, "NACKs received by retry", nacksReceived)
	metrics.AssertGT(t, "retransmits served", g.retransmits)
	metrics.AssertGT(t, "late gap fills sent", late)
	g.assertNothingLost(t)
	g.assertAccounted(t, 0.10)
}
