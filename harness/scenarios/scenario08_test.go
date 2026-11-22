package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/lightwebinc/multicast-test/harness/env"
	"github.com/lightwebinc/multicast-test/harness/metrics"
)

// Scenario 08: NACK retransmit with BRC-128 (Extended Format) payloads
//
// Ported from the retired bash scenario 08.
//
// Same topology, loss and late gap fills as scenario 99 but with BRC-128
// payloads. Verifies that the retry cache and NACK pipeline are
// payload-agnostic: every gap closes, by a retransmit or its late fill, and
// none is lost.
func TestScenario08_NackRetransmitBRC128(t *testing.T) {
	ctx := context.Background()
	e, _ := retryTopology(t, "s08")
	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle")

	// 1% loss on listeners.
	for _, l := range []string{"s08-listener1", "s08-listener2", "s08-listener3"} {
		if err := env.ApplyNetemLoss(ctx, l, 1.0); err != nil {
			t.Fatalf("netem loss %s: %v", l, err)
		}
		t.Cleanup(func() { env.RemoveNetemLoss(ctx, l) }) //nolint:errcheck
	}

	beforeL := snapshotListeners(t, e, ctx, "s08")
	beforeR := e.Snapshot(ctx, "s08-retry1")

	genCmd := subtxGenCmd("[fd10::2]:8725")
	genCmd = append(genCmd,
		"-payload-format", "brc128",
		"-seq-gap-every", "200",
		"-seq-gap-size", "1",
		"-seq-gap-delay", "50ms",
	)
	startGenerator(t, ctx, "s08", genCmd)
	waitGenerator(t, ctx, "s08")

	// Settle: every gap closed or booked unrecovered, and counters still.
	afterL := settleRecovery(t, e, ctx, "s08", beforeL, 60*time.Second)
	urlR := e.MetricsURL(ctx, "s08-retry1")
	afterR := metrics.ScrapeOrFail(t, urlR)
	late := generatorLate(t, ctx, "s08")

	gapsDetected := sumListenerDelta("s08", "bsl_gaps_detected_total", beforeL, afterL)
	nacksDispatched := sumListenerDelta("s08", "bsl_nacks_dispatched_total", beforeL, afterL)

	deltaR := metrics.DeltaMap(beforeR, afterR)
	nacksReceived := deltaR["bre_nack_requests_total"]

	g := newGapRepair(beforeL, afterL, beforeR, afterR, late)
	g.log(t)
	logGapOutcomes(t, e, ctx, "s08")
	t.Logf("gaps_detected=%.0f nacks=%.0f; retry: nacks_received=%.0f", gapsDetected, nacksDispatched, nacksReceived)

	metrics.AssertGT(t, "gaps detected", gapsDetected)
	metrics.AssertGT(t, "NACKs dispatched", nacksDispatched)
	metrics.AssertGT(t, "NACKs received", nacksReceived)
	metrics.AssertGT(t, "retransmits served", g.retransmits)
	metrics.AssertGT(t, "late gap fills sent", late)
	g.assertNothingLost(t)
	g.assertAccounted(t, 0.10)
}
