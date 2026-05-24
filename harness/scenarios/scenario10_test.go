package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/lightwebinc/bitcoin-multicast-test/harness/env"
	"github.com/lightwebinc/bitcoin-multicast-test/harness/metrics"
)

// Scenario 10 — Single endpoint ACK (tightened)
//
// Mirrors scenarios/10-single-endpoint-ack/run.sh.
//
// Low PPS with infrequent gaps so every dispatched NACK should produce an ACK.
// Assertions are tighter than scenario 99: gaps_unrecovered must be 0.
func TestScenario10_SingleEndpointACK(t *testing.T) {
	ctx := context.Background()
	e, _ := retryTopology(t, "s10")
	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle")

	// 1% loss on listeners.
	for _, l := range []string{"s10-listener1", "s10-listener2", "s10-listener3"} {
		if err := env.ApplyNetemLoss(ctx, l, 1.0); err != nil {
			t.Fatalf("netem loss %s: %v", l, err)
		}
		t.Cleanup(func() { env.RemoveNetemLoss(ctx, l) }) //nolint:errcheck
	}

	beforeL := snapshotListeners(t, e, ctx, "s10")
	beforeR := e.Snapshot(ctx, "s10-retry1")

	genCmd := subtxGenCmd("[fd10::2]:9000")
	genCmd = append(genCmd,
		"-pps", "200",
		"-duration", "10s",
		"-seq-gap-every", "500",
		"-seq-gap-size", "1",
		"-seq-gap-delay", "500ms",
	)
	startGenerator(t, ctx, "s10", genCmd)
	waitGenerator(t, ctx, "s10")

	e.Sleep(3*time.Second, "NACK pipeline drain")

	afterL := scrapeListeners(t, e, ctx, "s10")
	urlR := e.MetricsURL(ctx, "s10-retry1")
	afterR := metrics.ScrapeOrFail(t, urlR)
	e.LogContainerOutput(ctx, "s10-source")

	gapsDetected := sumListenerDelta("s10", "bsl_gaps_detected_total", beforeL, afterL)
	nacksDispatched := sumListenerDelta("s10", "bsl_nacks_dispatched_total", beforeL, afterL)
	gapsSuppressed := sumListenerDelta("s10", "bsl_gaps_suppressed_total", beforeL, afterL)
	gapsUnrecovered := sumListenerDelta("s10", "bsl_gaps_unrecovered_total", beforeL, afterL)

	deltaR := metrics.DeltaMap(beforeR, afterR)
	nacksReceived := deltaR["bre_nack_requests_total"]
	retransmits := deltaR["bre_retransmits_total"]

	t.Logf("gaps_detected=%.0f nacks=%.0f suppressed=%.0f unrecovered=%.0f",
		gapsDetected, nacksDispatched, gapsSuppressed, gapsUnrecovered)
	t.Logf("retry: nacks_received=%.0f retransmits=%.0f", nacksReceived, retransmits)

	metrics.AssertGT(t, "gaps detected", gapsDetected)
	metrics.AssertGT(t, "NACKs dispatched", nacksDispatched)
	metrics.AssertGT(t, "NACKs received", nacksReceived)
	metrics.AssertGT(t, "retransmits", retransmits)
	metrics.AssertGT(t, "gaps suppressed", gapsSuppressed)
	metrics.AssertZero(t, "gaps unrecovered", gapsUnrecovered)
}
