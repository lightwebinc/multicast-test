package scenarios

import (
	"context"
	"fmt"
	"testing"
	"time"

	dockerdriver "github.com/lightwebinc/multicast-test/harness/driver/docker"
	"github.com/lightwebinc/multicast-test/harness/env"
	"github.com/lightwebinc/multicast-test/harness/metrics"
)

// Scenario 74b: cross-domain NACK proxying with its Redis failing
//
// Scenario 74's topology, with the downstream retry claiming its work in a
// shared Redis: the NACK proxy's in-flight claim (one upstream fetch per gap
// across sibling endpoints) and the multicast retransmit claim. Redis is
// stopped mid-run. Neither claim may stop or slow recovery: the endpoint
// proxies and retransmits anyway, books the claims that failed in
// bre_dedup_errors_total and, once the shared claim breaker is open, the ones
// it skipped in bre_dedup_skipped_total, and the consumer still recovers every
// frame the bridge never re-emitted.
func TestScenario74b_NACKProxyRedisFailure(t *testing.T) {
	const (
		bridge   = "s74b-bridge"
		consumer = "s74b-consumer"
		dsRetry  = "s74b-ds-retry"
		redis    = "s74b-redis"
	)
	ctx := context.Background()
	e := env.New(t, dockerdriver.New())
	// Redis first: the downstream retry connects at startup, and runs without
	// shared claims if it cannot.
	e.AddNode(redisNodeAt(redis, "fd10::50"))
	crossDomainNodes(e, "s74b", map[string]string{"REDIS_ADDR": "[fd10::50]:6379"})

	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle + multicast group joins")

	if err := env.ApplyNetemLoss(ctx, bridge, 5.0); err != nil {
		t.Fatalf("netem loss on %s: %v", bridge, err)
	}
	t.Cleanup(func() { env.RemoveNetemLoss(context.Background(), bridge) }) //nolint:errcheck

	beforeC := e.Snapshot(ctx, consumer)
	beforeDS := e.Snapshot(ctx, dsRetry)

	genCmd := subtxGenCmd("[fd10::2]:8725")
	genCmd = append(genCmd, "-duration", "20s")
	startGenerator(t, ctx, "s74b", genCmd)

	e.Sleep(8*time.Second, "proxied recovery with Redis up")
	proxyClaims := redisKeys(t, ctx, e, redis, "bre:proxy:*")
	retransmitClaims := redisKeys(t, ctx, e, redis, "bre:dedup:*")
	midDS := metrics.ScrapeOrFail(t, e.MetricsURL(ctx, dsRetry))
	proxyErrsUp := dedupErrors(t, ctx, e, dsRetry, "proxy")
	retransmitErrsUp := dedupErrors(t, ctx, e, dsRetry, "retransmit")
	proxySkippedUp := dedupSkipped(t, ctx, e, dsRetry, "proxy")
	retransmitSkippedUp := dedupSkipped(t, ctx, e, dsRetry, "retransmit")
	if err := e.Driver.Stop(ctx, redis); err != nil {
		t.Fatalf("stop redis: %v", err)
	}
	t.Logf("redis stopped with %.0f proxy claims and %.0f retransmit claims in it", proxyClaims, retransmitClaims)
	// As in scenario 55: once the claims in flight at the stop have failed,
	// only probes reach the dead backend.
	e.Sleep(time.Second, "claims in flight at the stop fail")
	errsAtStop := dedupErrors(t, ctx, e, dsRetry, "proxy") - proxyErrsUp +
		dedupErrors(t, ctx, e, dsRetry, "retransmit") - retransmitErrsUp
	opened := time.Now()

	waitGenerator(t, ctx, "s74b")
	afterC := settleNode(t, e, ctx, consumer, beforeC, 60*time.Second)
	afterDS := metrics.ScrapeOrFail(t, e.MetricsURL(ctx, dsRetry))
	proxyErrs := dedupErrors(t, ctx, e, dsRetry, "proxy") - proxyErrsUp
	retransmitErrs := dedupErrors(t, ctx, e, dsRetry, "retransmit") - retransmitErrsUp
	proxySkipped := dedupSkipped(t, ctx, e, dsRetry, "proxy") - proxySkippedUp
	retransmitSkipped := dedupSkipped(t, ctx, e, dsRetry, "retransmit") - retransmitSkippedUp
	probedFor := time.Since(opened).Round(time.Second)

	up := metrics.DeltaMap(beforeDS, midDS)
	down := metrics.DeltaMap(midDS, afterDS)
	c := metrics.DeltaMap(beforeC, afterC)
	t.Logf("ds-retry with Redis up: proxy_requests=%.0f proxy_recovered=%.0f proxy_failed=%.0f proxy_inflight_dedup=%.0f retransmits=%.0f retransmit_dedup=%.0f retransmit_repeats=%.0f dedup_errors proxy=%.0f retransmit=%.0f, dedup_skipped proxy=%.0f retransmit=%.0f",
		up["bre_proxy_requests_total"], up["bre_proxy_recovered_total"], up["bre_proxy_failed_total"], up["bre_proxy_inflight_dedup_total"],
		up["bre_retransmits_total"], up["bre_retransmit_dedup_total"], up["bre_retransmit_repeats_total"],
		proxyErrsUp, retransmitErrsUp, proxySkippedUp, retransmitSkippedUp)
	t.Logf("ds-retry with Redis down: proxy_requests=%.0f proxy_recovered=%.0f proxy_failed=%.0f proxy_inflight_dedup=%.0f retransmits=%.0f retransmit_dedup=%.0f retransmit_repeats=%.0f dedup_errors proxy=%.0f retransmit=%.0f (%.0f in the first second, %.0f in the %s after), dedup_skipped proxy=%.0f retransmit=%.0f",
		down["bre_proxy_requests_total"], down["bre_proxy_recovered_total"], down["bre_proxy_failed_total"], down["bre_proxy_inflight_dedup_total"],
		down["bre_retransmits_total"], down["bre_retransmit_dedup_total"], down["bre_retransmit_repeats_total"],
		proxyErrs, retransmitErrs, errsAtStop, proxyErrs+retransmitErrs-errsAtStop, probedFor, proxySkipped, retransmitSkipped)
	cURL := e.MetricsURL(ctx, consumer)
	t.Logf("consumer: detected=%.0f suppressed=%.0f unrecovered=%.0f late_filled=%.0f (given up: retries=%.0f ttl=%.0f restart=%.0f)",
		c["bsl_gaps_detected_total"], c["bsl_gaps_suppressed_total"], c["bsl_gaps_unrecovered_total"], c["bsl_gaps_late_filled_total"],
		metrics.LabelledValue(t, cURL, "bsl_gaps_abandoned_total", "reason", "retries"),
		metrics.LabelledValue(t, cURL, "bsl_gaps_abandoned_total", "reason", "ttl"),
		metrics.LabelledValue(t, cURL, "bsl_gaps_abandoned_total", "reason", "restart"))

	metrics.AssertGT(t, "proxy claims written while Redis was up", proxyClaims)
	metrics.AssertGT(t, "proxied recoveries with Redis up", up["bre_proxy_recovered_total"])
	// Both claims share one breaker, so after the first failure either may
	// only be skipped: each one fails open, by an error or a skip.
	metrics.AssertGT(t, "proxy claims failed open with Redis down (errors + skipped)", proxyErrs+proxySkipped)
	metrics.AssertGT(t, "retransmit claims failed open with Redis down (errors + skipped)", retransmitErrs+retransmitSkipped)
	metrics.AssertGT(t, "bre_dedup_errors_total in the first second Redis was down", errsAtStop)
	metrics.AssertGT(t, "bre_dedup_skipped_total with Redis down", proxySkipped+retransmitSkipped)
	metrics.AssertLT(t, fmt.Sprintf("claim errors in the %s after (one probe per breaker window)", probedFor),
		proxyErrs+retransmitErrs-errsAtStop, breakerProbeLimit(probedFor))
	metrics.AssertGT(t, "proxied recoveries with Redis down (fail-open)", down["bre_proxy_recovered_total"])
	metrics.AssertGT(t, "retransmits into the domain with Redis down (fail-open)", down["bre_retransmits_total"])
	metrics.AssertGT(t, "consumer gaps detected", c["bsl_gaps_detected_total"])
	metrics.AssertGT(t, "consumer gaps recovered", c["bsl_gaps_suppressed_total"])
	metrics.AssertZero(t, "consumer gaps lost (unrecovered minus late-filled)",
		c["bsl_gaps_unrecovered_total"]-c["bsl_gaps_late_filled_total"])
}

// settleNode scrapes one listener until its gaps are all closed or booked
// unrecovered and its gap counters hold still across two scrapes a second
// apart, then returns that scrape.
func settleNode(t *testing.T, e *env.Env, ctx context.Context, name string, before map[string]float64, timeout time.Duration) map[string]float64 {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var prev map[string]float64
	for {
		cur := metrics.ScrapeOrFail(t, e.MetricsURL(ctx, name))
		d := metrics.DeltaMap(before, cur)
		pending := d["bsl_gaps_detected_total"] - d["bsl_gaps_suppressed_total"] - d["bsl_gaps_unrecovered_total"]
		if pending == 0 && prev != nil &&
			prev["bsl_gaps_suppressed_total"] == cur["bsl_gaps_suppressed_total"] &&
			prev["bsl_gaps_unrecovered_total"] == cur["bsl_gaps_unrecovered_total"] &&
			prev["bsl_frames_forwarded_total"] == cur["bsl_frames_forwarded_total"] {
			return cur
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s gaps did not settle within %s (pending %.0f)", name, timeout, pending)
		}
		prev = cur
		time.Sleep(time.Second)
	}
}
