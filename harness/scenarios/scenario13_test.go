package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/lightwebinc/multicast-test/harness/env"
	"github.com/lightwebinc/multicast-test/harness/metrics"
)

// Scenario 13 — MISS escalation by tier
//
// Mirrors scenarios/13-miss-escalation-tier/run.sh.
//
// Topology: proxy + 3 listeners + 3 retry endpoints (T0/P128, T0/P64, T1/P128).
// Block multicast ingress on retry1+retry2 so their caches are empty.
// retry3 has a warm cache. Listeners escalate: retry1→MISS, retry2→MISS,
// retry3→ACK. Verifies escalation ordering and recovery.
func TestScenario13_MissEscalationTier(t *testing.T) {
	ctx := context.Background()
	e := multiRetryTopology(t, "s13")

	// This scenario exercises miss-escalation recovery, not rate limiting. All
	// NACK traffic originates from just 3 listener IPs, so the default per-IP
	// limiter (RL_IP_RATE=100/s, burst 10) throttles the recovery burst and
	// silently drops NACKs (no MISS response). Listeners cannot distinguish a
	// throttled endpoint from a dead one: they time out, back off, and exhaust
	// retries before reaching retry3, leaving gaps unrecovered. In a real fleet
	// NACKs are spread across many IPs so per-IP limits never bite. Raise the
	// IP/chain limits (as scenario16 does) so escalation is not throttled here.
	for _, name := range []string{"s13-retry1", "s13-retry2", "s13-retry3"} {
		e.PatchEnv(name, map[string]string{
			"RL_IP_RATE":      "50000",
			"RL_IP_BURST":     "10000",
			"RL_CHAIN_RATE":   "10000",
			"RL_CHAIN_WINDOW": "60s",
		})
	}

	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle")

	// Block multicast ingress on retry1 and retry2.
	for _, name := range []string{"s13-retry1", "s13-retry2"} {
		if err := env.BlockUDPIngress(ctx, name, 9001); err != nil {
			t.Fatalf("block ingress %s: %v", name, err)
		}
		t.Cleanup(func() { env.UnblockUDPIngress(ctx, name, 9001) }) //nolint:errcheck
	}

	// 1% loss on listeners.
	for _, l := range []string{"s13-listener1", "s13-listener2", "s13-listener3"} {
		if err := env.ApplyNetemLoss(ctx, l, 1.0); err != nil {
			t.Fatalf("netem loss %s: %v", l, err)
		}
		t.Cleanup(func() { env.RemoveNetemLoss(ctx, l) }) //nolint:errcheck
	}

	// Wait for beacon discovery convergence.
	e.Sleep(12*time.Second, "beacon discovery convergence (2x beacon_interval)")

	beforeL := snapshotListeners(t, e, ctx, "s13")
	beforeR := snapshotRetries(t, e, ctx, "s13")

	// 150 pps keeps the single in-order listener worker (NUM_WORKERS=1, required
	// so SO_REUSEPORT spreading does not manufacture false gaps) from falling
	// behind under Docker CPU contention. At 1000 pps the harness reorders and
	// drops far above the injected 1%, inflating gaps and producing correlated
	// loss bursts that strand recovery; aggressive retrying only adds load and
	// makes it worse. At this rate every gap escalates cleanly through all three
	// tiers (nacks ≈ 3×gaps, matching the VM reference) and recovers within the
	// 4% budget.
	genCmd := subtxGenCmd("[fd10::2]:8725")
	genCmd = append(genCmd, "-duration", "15s", "-pps", "150")
	startGenerator(t, ctx, "s13", genCmd)
	waitGenerator(t, ctx, "s13")

	e.Sleep(5*time.Second, "NACK pipeline drain")

	afterL := scrapeListeners(t, e, ctx, "s13")
	afterR := scrapeRetries(t, e, ctx, "s13")
	e.LogContainerOutput(ctx, "s13-source")

	// Listener aggregates.
	gapsDetected := sumListenerDelta("s13", "bsl_gaps_detected_total", beforeL, afterL)
	nacksDispatched := sumListenerDelta("s13", "bsl_nacks_dispatched_total", beforeL, afterL)
	gapsUnrecovered := sumListenerDelta("s13", "bsl_gaps_unrecovered_total", beforeL, afterL)

	// Per-retry metrics.
	r1Nacks := retryDelta(0, "bre_nack_requests_total", beforeR, afterR)
	r1Misses := retryDelta(0, "bre_cache_misses_total", beforeR, afterR)
	r1Cached := retryDelta(0, "bre_frames_cached_total", beforeR, afterR)

	r2Nacks := retryDelta(1, "bre_nack_requests_total", beforeR, afterR)
	r2Misses := retryDelta(1, "bre_cache_misses_total", beforeR, afterR)
	r2Cached := retryDelta(1, "bre_frames_cached_total", beforeR, afterR)

	r3Cached := retryDelta(2, "bre_frames_cached_total", beforeR, afterR)
	r3Hits := retryDelta(2, "bre_cache_hits_total", beforeR, afterR)
	r3Retransmits := retryDelta(2, "bre_retransmits_total", beforeR, afterR)

	t.Logf("gaps_detected=%.0f nacks=%.0f unrecovered=%.0f",
		gapsDetected, nacksDispatched, gapsUnrecovered)
	t.Logf("retry1: nacks=%.0f misses=%.0f cached=%.0f", r1Nacks, r1Misses, r1Cached)
	t.Logf("retry2: nacks=%.0f misses=%.0f cached=%.0f", r2Nacks, r2Misses, r2Cached)
	t.Logf("retry3: cached=%.0f hits=%.0f retransmits=%.0f", r3Cached, r3Hits, r3Retransmits)

	// Gaps must be detected.
	metrics.AssertGT(t, "gaps detected", gapsDetected)
	metrics.AssertGT(t, "NACKs dispatched", nacksDispatched)

	// retry1 must receive NACKs and respond MISS (cache empty).
	metrics.AssertGT(t, "retry1 received NACKs", r1Nacks)
	metrics.AssertGT(t, "retry1 cache misses", r1Misses)
	metrics.AssertZero(t, "retry1 frames cached (blocked)", r1Cached)

	// retry2 must receive escalated NACKs and respond MISS.
	metrics.AssertGT(t, "retry2 received NACKs (escalated)", r2Nacks)
	metrics.AssertGT(t, "retry2 cache misses", r2Misses)
	metrics.AssertZero(t, "retry2 frames cached (blocked)", r2Cached)

	// retry3 must have warm cache and serve frames.
	metrics.AssertGT(t, "retry3 cached frames", r3Cached)
	metrics.AssertGT(t, "retry3 cache hits", r3Hits)
	metrics.AssertGT(t, "retry3 retransmits", r3Retransmits)

	// Most gaps should be recovered. Allow 4% unrecovered (floor 10).
	limit := gapsDetected * 0.04
	if limit < 10 {
		limit = 10
	}
	metrics.AssertLT(t, "gaps unrecovered within tolerance", gapsUnrecovered, limit)
}
