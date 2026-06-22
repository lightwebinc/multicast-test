package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/lightwebinc/multicast-test/harness/env"
	"github.com/lightwebinc/multicast-test/harness/metrics"
)

// Scenario 11 — Permanent gap / MISS (cache-empty)
//
// Mirrors scenarios/11-permanent-gap-miss/run.sh.
//
// Blocks multicast ingress on the retry endpoint so its cache is empty.
// 2% netem loss on listeners creates gaps; every NACK → MISS; gaps evict
// as unrecovered after MaxRetries.
func TestScenario11_PermanentGapMISS(t *testing.T) {
	ctx := context.Background()
	e, _ := retryTopology(t, "s11")
	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle")

	// Block multicast ingress on retry endpoint (port 9001).
	if err := env.BlockUDPIngress(ctx, "s11-retry1", 9001); err != nil {
		t.Fatalf("block retry ingress: %v", err)
	}
	t.Cleanup(func() { env.UnblockUDPIngress(ctx, "s11-retry1", 9001) }) //nolint:errcheck

	// 2% loss on listeners.
	for _, l := range []string{"s11-listener1", "s11-listener2", "s11-listener3"} {
		if err := env.ApplyNetemLoss(ctx, l, 2.0); err != nil {
			t.Fatalf("netem loss %s: %v", l, err)
		}
		t.Cleanup(func() { env.RemoveNetemLoss(ctx, l) }) //nolint:errcheck
	}

	e.Sleep(3*time.Second, "stabilise")

	beforeL := snapshotListeners(t, e, ctx, "s11")
	beforeR := e.Snapshot(ctx, "s11-retry1")

	genCmd := subtxGenCmd("[fd10::2]:8725")
	genCmd = append(genCmd, "-pps", "500", "-duration", "10s")
	startGenerator(t, ctx, "s11", genCmd)
	waitGenerator(t, ctx, "s11")

	// Long drain: MaxRetries exhaust takes time.
	e.Sleep(45*time.Second, "NACK retry pipeline exhaust")

	afterL := scrapeListeners(t, e, ctx, "s11")
	urlR := e.MetricsURL(ctx, "s11-retry1")
	afterR := metrics.ScrapeOrFail(t, urlR)
	e.LogContainerOutput(ctx, "s11-source")

	gapsDetected := sumListenerDelta("s11", "bsl_gaps_detected_total", beforeL, afterL)
	nacksDispatched := sumListenerDelta("s11", "bsl_nacks_dispatched_total", beforeL, afterL)
	gapsUnrecovered := sumListenerDelta("s11", "bsl_gaps_unrecovered_total", beforeL, afterL)

	deltaR := metrics.DeltaMap(beforeR, afterR)
	framesCached := deltaR["bre_frames_cached_total"]
	cacheMisses := deltaR["bre_cache_misses_total"]
	retransmits := deltaR["bre_retransmits_total"]

	t.Logf("gaps_detected=%.0f nacks=%.0f unrecovered=%.0f",
		gapsDetected, nacksDispatched, gapsUnrecovered)
	t.Logf("retry: cached=%.0f misses=%.0f retransmits=%.0f",
		framesCached, cacheMisses, retransmits)

	// Retry must NOT have cached any frames.
	metrics.AssertZero(t, "frames cached (ingress blocked)", framesCached)

	// Gaps and NACKs must be detected.
	metrics.AssertGT(t, "gaps detected", gapsDetected)
	metrics.AssertGT(t, "NACKs dispatched", nacksDispatched)

	// All NACKs should be cache misses.
	metrics.AssertGT(t, "cache misses", cacheMisses)

	// No retransmits (cache empty).
	metrics.AssertZero(t, "retransmits (cache empty)", retransmits)

	// Gaps must be evicted as unrecovered.
	metrics.AssertGT(t, "gaps unrecovered", gapsUnrecovered)
}
