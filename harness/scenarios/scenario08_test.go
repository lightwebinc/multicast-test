package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/lightwebinc/bitcoin-multicast-test/harness/env"
	"github.com/lightwebinc/bitcoin-multicast-test/harness/metrics"
)

// Scenario 08 — NACK retransmit with BRC-128 (Extended Format) payloads
//
// Mirrors scenarios/08-nack-retransmit-brc128/run.sh.
//
// Same topology as scenario 99 but with BRC-128 payloads. Verifies that the
// retry cache and NACK pipeline are payload-agnostic.
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

	genCmd := subtxGenCmd("[fd10::2]:9000")
	genCmd = append(genCmd,
		"-payload-format", "brc128",
		"-seq-gap-every", "200",
		"-seq-gap-size", "1",
		"-seq-gap-delay", "50ms",
	)
	startGenerator(t, ctx, "s08", genCmd)
	waitGenerator(t, ctx, "s08")

	e.Sleep(5*time.Second, "NACK pipeline drain")

	afterL := scrapeListeners(t, e, ctx, "s08")
	urlR := e.MetricsURL(ctx, "s08-retry1")
	afterR := metrics.ScrapeOrFail(t, urlR)

	gapsDetected := sumListenerDelta("s08", "bsl_gaps_detected_total", beforeL, afterL)
	nacksDispatched := sumListenerDelta("s08", "bsl_nacks_dispatched_total", beforeL, afterL)
	gapsSuppressed := sumListenerDelta("s08", "bsl_gaps_suppressed_total", beforeL, afterL)

	deltaR := metrics.DeltaMap(beforeR, afterR)
	nacksReceived := deltaR["bre_nack_requests_total"]
	retransmits := deltaR["bre_retransmits_total"]

	t.Logf("gaps_detected=%.0f nacks=%.0f suppressed=%.0f",
		gapsDetected, nacksDispatched, gapsSuppressed)
	t.Logf("retry: nacks_received=%.0f retransmits=%.0f", nacksReceived, retransmits)

	metrics.AssertGT(t, "gaps detected", gapsDetected)
	metrics.AssertGT(t, "NACKs dispatched", nacksDispatched)
	metrics.AssertGT(t, "NACKs received", nacksReceived)
	metrics.AssertGT(t, "retransmits", retransmits)
	metrics.AssertGT(t, "gaps suppressed", gapsSuppressed)
}
