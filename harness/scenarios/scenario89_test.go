package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/lightwebinc/multicast-test/harness/driver"
	dockerdriver "github.com/lightwebinc/multicast-test/harness/driver/docker"
	"github.com/lightwebinc/multicast-test/harness/env"
	"github.com/lightwebinc/multicast-test/harness/metrics"
)

// TestScenario89_RebucketLossRecovery is the re-bucketing hardening regression:
// a generation MISMATCH (proxy coalesces at SHARD_BITS=1, the listener runs
// SHARD_BITS=2) forces every received bundle through the re-bucketer, whose
// re-stamped child SeqNums are LOCAL (a dropped parent leaves no hole in any
// child stream). Before the hardening this made upstream loss invisible and
// unrecoverable (zero gaps). Now the listener gap-tracks the PARENT bundle
// stream (survivorship-gated) on the identity the origin's retry cached, so:
//   - the guard fires: bsl_rebucket_unguarded_total > 0 (listener is NOT a
//     declared -rebucket-relay; the mismatch is flagged loudly), and
//   - upstream loss is DETECTED on the parent stream and NACK-recovered through
//     the retry, exactly as the matched-generation path (scenario 91).
//
// netem loss is applied to the listener only; the retry receives every parent
// bundle and keeps a warm cache, so missed parents recover.
func TestScenario89_RebucketLossRecovery(t *testing.T) {
	ctx := context.Background()
	e := env.New(t, dockerdriver.New())

	// Proxy coalesces at SHARD_BITS=1 (coarse parent bundles), single worker/flow.
	penv := proxyEnv()
	penv["SHARD_BITS"] = "1"
	penv["NUM_WORKERS"] = "1"
	penv["COALESCE"] = "true"
	penv["COALESCE_MAX_BYTES"] = "1400"
	e.AddNode(driver.NodeConfig{
		Name:        "s89-proxy",
		Image:       "shard-proxy:harness",
		IPv6:        "fd10::2",
		Env:         penv,
		MetricsPort: 9100,
		Role:        driver.RoleProxy,
	})

	// Listener runs SHARD_BITS=2, a MISMATCH vs the proxy's coarse bundles, so
	// every received bundle is re-bucketed. NOT declared a relay (default), so the
	// unguarded-rebucket alarm must fire. RETRY_ENDPOINTS points at the retry that
	// caches the parent bundles.
	lenv := listenerEnv()
	lenv["SHARD_BITS"] = "2"
	lenv["RETRY_ENDPOINTS"] = "[fd10::20]:9300"
	e.AddNode(driver.NodeConfig{
		Name:        "s89-listener1",
		Image:       "shard-listener:harness",
		IPv6:        "fd10::11",
		Env:         lenv,
		MetricsPort: 9200,
		Role:        driver.RoleListener,
	})

	// Retry receives the proxy's parent bundles and caches them by (HashKey,
	// SeqNum), the identity the listener NACKs after re-bucketing.
	renv := retryEnv()
	e.AddNode(driver.NodeConfig{
		Name:        "s89-retry1",
		Image:       "retry-endpoint:harness",
		IPv6:        "fd10::20",
		Env:         renv,
		MetricsPort: 9400,
		Role:        driver.RoleRetry,
	})

	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle + multicast group joins")

	// Drop ~3% of inbound multicast at the listener → parent-stream gaps → NACKs.
	if err := env.ApplyNetemLoss(ctx, "s89-listener1", 3.0); err != nil {
		t.Fatalf("apply netem loss: %v", err)
	}
	t.Cleanup(func() { env.RemoveNetemLoss(context.Background(), "s89-listener1") }) //nolint:errcheck

	beforeL := e.Snapshot(ctx, "s89-listener1")
	beforeR := e.Snapshot(ctx, "s89-retry1")

	// Low, steady, single-flow rate for deterministic recovery.
	genCmd := []string{
		"-addr", "[fd10::2]:8725",
		"-shard-bits", "1",
		"-subtrees", "1",
		"-subtree-seed", "multicast-lab-bsv",
		"-pps", "300",
		"-duration", "12s",
		"-payload-size", "200",
		"-log-interval", "3s",
	}
	startGenerator(t, ctx, "s89", genCmd)
	waitGenerator(t, ctx, "s89")

	// Remove loss before the recovery drain so retransmitted parents are not
	// re-dropped (mirrors scenario 91).
	env.RemoveNetemLoss(ctx, "s89-listener1") //nolint:errcheck
	e.Sleep(7*time.Second, "NACK + parent-bundle retransmit recovery drain (loss removed)")

	afterL := metrics.ScrapeOrFail(t, e.MetricsURL(ctx, "s89-listener1"))
	afterR := metrics.ScrapeOrFail(t, e.MetricsURL(ctx, "s89-retry1"))

	deltaL := metrics.DeltaMap(beforeL, afterL)
	deltaR := metrics.DeltaMap(beforeR, afterR)

	rebucketed := deltaL["bsl_bundles_rebucketed_total"]
	unguarded := deltaL["bsl_rebucket_unguarded_total"]
	gapsDetected := deltaL["bsl_gaps_detected_total"]
	nacksDispatched := deltaL["bsl_nacks_dispatched_total"]
	gapsUnrecovered := deltaL["bsl_gaps_unrecovered_total"]
	recovered := gapsDetected - gapsUnrecovered

	nackRequests := deltaR["bre_nack_requests_total"]
	cacheHits := deltaR["bre_cache_hits_total"]
	retransmits := deltaR["bre_retransmits_total"]

	t.Logf("re-bucket: rebucketed=%.0f unguarded=%.0f | gaps=%.0f nacks=%.0f recovered=%.0f unrecovered=%.0f | retry: nackReq=%.0f hits=%.0f retx=%.0f",
		rebucketed, unguarded, gapsDetected, nacksDispatched, recovered, gapsUnrecovered, nackRequests, cacheHits, retransmits)

	// The mismatch actually engaged the re-bucketer.
	metrics.AssertGT(t, "bundles re-bucketed (generation mismatch fired)", rebucketed)
	// Guard: a non-relay listener re-bucketing raises the alarm.
	metrics.AssertGT(t, "unguarded re-bucket alarm (non-relay listener)", unguarded)
	// THE FIX: upstream loss is detected on the PARENT stream (was 0 before hardening).
	metrics.AssertGT(t, "parent-stream gaps detected under loss", gapsDetected)
	metrics.AssertGT(t, "NACKs dispatched", nacksDispatched)
	// Recovery closed the loop through the retry's parent-bundle cache.
	metrics.AssertGT(t, "retry NACK requests received", nackRequests)
	metrics.AssertGT(t, "retry cache hits (parent bundle found)", cacheHits)
	metrics.AssertGT(t, "retry parent-bundle retransmits", retransmits)
	metrics.AssertGT(t, "gaps recovered via parent retransmit", recovered)
}
