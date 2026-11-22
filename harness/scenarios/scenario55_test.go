package scenarios

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/lightwebinc/multicast-test/harness/driver"
	dockerdriver "github.com/lightwebinc/multicast-test/harness/driver/docker"
	"github.com/lightwebinc/multicast-test/harness/env"
	"github.com/lightwebinc/multicast-test/harness/metrics"
)

// Scenario 55: retransmit dedup fails open when its Redis fails
//
// retry1 claims each multicast retransmit in a shared Redis (SET NX), the
// cross-instance dedup that keeps sibling endpoints from repairing one gap
// twice. While Redis is up, a listener that missed the one copy and asks again
// gets another: its repeat NACK goes out past the claim
// (bre_retransmit_repeats_total). Redis is stopped mid-run. A claim the
// backend cannot answer must not stop the repair: retry1 retransmits anyway
// and counts the claim in bre_dedup_errors_total{claim="retransmit"}. Nor may
// it slow the repair: once a claim fails, the claim breaker skips claims
// (bre_dedup_skipped_total) and lets one probe through per window. Every gap
// the netem loss opened, before and after the stop, is repaired.
func TestScenario55_RetransmitDedupRedisFailure(t *testing.T) {
	ctx := context.Background()
	e := env.New(t, dockerdriver.New())

	// Redis first: retry1 connects at startup, and runs without cross-instance
	// dedup if it cannot.
	e.AddNode(redisNodeAt("s55-redis", "fd10::30"))

	e.AddNode(driver.NodeConfig{
		Name:        "s55-proxy",
		Image:       "shard-proxy:harness",
		IPv6:        "fd10::2",
		Env:         proxyEnv(),
		MetricsPort: 9100,
		Role:        driver.RoleProxy,
	})
	listeners := []string{"s55-listener1", "s55-listener2", "s55-listener3"}
	for i, name := range listeners {
		lenv := listenerEnv()
		lenv["RETRY_ENDPOINTS"] = "[fd10::20]:9300"
		e.AddNode(driver.NodeConfig{
			Name:        name,
			Image:       "shard-listener:harness",
			IPv6:        fmt.Sprintf("fd10::1%d", i+1),
			Env:         lenv,
			MetricsPort: 9200,
			Role:        driver.RoleListener,
		})
	}
	renv := retryEnv()
	renv["REDIS_ADDR"] = "[fd10::30]:6379" // memory frame cache, Redis for the dedup claims
	e.AddNode(driver.NodeConfig{
		Name:        "s55-retry1",
		Image:       "retry-endpoint:harness",
		IPv6:        "fd10::20",
		Env:         renv,
		MetricsPort: 9400,
		Role:        driver.RoleRetry,
	})

	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle")

	for _, l := range listeners {
		// 2% loss on every listener: enough copies go missing while Redis
		// is up that repeat NACKs always occur.
		if err := env.ApplyNetemLoss(ctx, l, 2.0); err != nil {
			t.Fatalf("netem loss %s: %v", l, err)
		}
		t.Cleanup(func() { env.RemoveNetemLoss(ctx, l) }) //nolint:errcheck
	}

	var beforeL [3]map[string]float64
	for i, l := range listeners {
		beforeL[i] = e.Snapshot(ctx, l)
	}
	beforeR := e.Snapshot(ctx, "s55-retry1")

	genCmd := subtxGenCmd("[fd10::2]:8725")
	genCmd = append(genCmd, "-duration", "20s")
	startGenerator(t, ctx, "s55", genCmd)

	e.Sleep(8*time.Second, "repairs with Redis up")
	claims := redisKeys(t, ctx, e, "s55-redis", "bre:dedup:*")
	midR := metrics.ScrapeOrFail(t, e.MetricsURL(ctx, "s55-retry1"))
	errsUp := dedupErrors(t, ctx, e, "s55-retry1", "retransmit")
	skippedUp := dedupSkipped(t, ctx, e, "s55-retry1", "retransmit")
	if err := e.Driver.Stop(ctx, "s55-redis"); err != nil {
		t.Fatalf("stop redis: %v", err)
	}
	t.Logf("redis stopped with %.0f retransmit claims and requester marks in it", claims)
	// The first claim to fail opens the breaker, and the claims already in
	// flight fail within the claim timeout. From then on only probes reach
	// the dead backend.
	e.Sleep(time.Second, "claims in flight at the stop fail")
	errsAtStop := dedupErrors(t, ctx, e, "s55-retry1", "retransmit") - errsUp
	opened := time.Now()

	waitGenerator(t, ctx, "s55")
	afterL := settleRecovery(t, e, ctx, "s55", beforeL, 60*time.Second)
	afterR := metrics.ScrapeOrFail(t, e.MetricsURL(ctx, "s55-retry1"))
	errsDown := dedupErrors(t, ctx, e, "s55-retry1", "retransmit") - errsUp
	skippedDown := dedupSkipped(t, ctx, e, "s55-retry1", "retransmit") - skippedUp
	probedFor := time.Since(opened).Round(time.Second)

	up := metrics.DeltaMap(beforeR, midR)
	down := metrics.DeltaMap(midR, afterR)
	t.Logf("retry1 with Redis up: retransmits=%.0f claim_and_mark_keys=%.0f dedup_errors=%.0f dedup_skipped=%.0f retransmit_dedup=%.0f retransmit_repeats=%.0f cache_misses=%.0f",
		up["bre_retransmits_total"], claims, errsUp, skippedUp, up["bre_retransmit_dedup_total"], up["bre_retransmit_repeats_total"], up["bre_cache_misses_total"])
	t.Logf("retry1 with Redis down: retransmits=%.0f retransmit_dedup=%.0f retransmit_repeats=%.0f dedup_errors{claim=retransmit}=%.0f (%.0f in the first second, %.0f in the %s after) dedup_skipped{claim=retransmit}=%.0f",
		down["bre_retransmits_total"], down["bre_retransmit_dedup_total"], down["bre_retransmit_repeats_total"], errsDown, errsAtStop, errsDown-errsAtStop, probedFor, skippedDown)

	var detected, suppressed, unrecovered, lateFilled float64
	for i, l := range listeners {
		d := metrics.DeltaMap(beforeL[i], afterL[i])
		url := e.MetricsURL(ctx, l)
		t.Logf("%s: detected=%.0f suppressed=%.0f unrecovered=%.0f late_filled=%.0f (given up: retries=%.0f restart=%.0f)", l,
			d["bsl_gaps_detected_total"], d["bsl_gaps_suppressed_total"], d["bsl_gaps_unrecovered_total"], d["bsl_gaps_late_filled_total"],
			metrics.LabelledValue(t, url, "bsl_gaps_abandoned_total", "reason", "retries"),
			metrics.LabelledValue(t, url, "bsl_gaps_abandoned_total", "reason", "restart"))
		detected += d["bsl_gaps_detected_total"]
		suppressed += d["bsl_gaps_suppressed_total"]
		unrecovered += d["bsl_gaps_unrecovered_total"]
		lateFilled += d["bsl_gaps_late_filled_total"]
	}

	metrics.AssertGT(t, "retransmit claims and marks written while Redis was up", claims)
	// A requester that asks again inside the dedup window did not get the
	// copy; its repeat is sent past the claim, or the gap would be given up.
	metrics.AssertGT(t, "bre_retransmit_repeats_total with Redis up", up["bre_retransmit_repeats_total"])
	metrics.AssertGT(t, "retransmits with Redis up", up["bre_retransmits_total"])
	metrics.AssertGT(t, "retransmits with Redis down (fail-open)", down["bre_retransmits_total"])
	metrics.AssertGT(t, "bre_dedup_errors_total{claim=retransmit} in the first second Redis was down", errsAtStop)
	// The claim breaker: after a failed claim the endpoint stops asking the
	// dead backend and fails the rest open at once, so it waits on one probe
	// claim per window instead of on every repair.
	metrics.AssertGT(t, "bre_dedup_skipped_total{claim=retransmit} with Redis down", skippedDown)
	metrics.AssertLT(t, fmt.Sprintf("claim errors in the %s after (one probe per breaker window)", probedFor),
		errsDown-errsAtStop, breakerProbeLimit(probedFor))
	metrics.AssertGT(t, "gaps detected", detected)
	metrics.AssertGT(t, "gaps repaired", suppressed)
	// A gap given up on whose frame then arrived is booked late-filled, so
	// unrecovered minus late-filled is the frames never delivered.
	metrics.AssertZero(t, "gaps lost (unrecovered minus late-filled)", unrecovered-lateFilled)
}
