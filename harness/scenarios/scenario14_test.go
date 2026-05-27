package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/lightwebinc/multicast-test/harness/env"
	"github.com/lightwebinc/multicast-test/harness/metrics"
)

// Scenario 14 — Multi-endpoint rate limit defense
//
// Mirrors scenarios/14-multi-endpoint-ratelimit/run.sh.
//
// Topology: proxy + 3 listeners + 3 retry endpoints with tight per-IP rate
// limits. Gap injection drives legitimate NACKs through the escalation chain.
// The tight RL_IP_RATE ensures rate_limit_drops{level="ip"} fires on at least
// one endpoint under sustained NACK load from all 3 listeners.
func TestScenario14_MultiEndpointRatelimit(t *testing.T) {
	ctx := context.Background()
	e := multiRetryTopology(t, "s14")

	// Override retry endpoints with tight per-IP rate limits via topology
	// env override. multiRetryTopology sets default env; we patch before start.
	for _, name := range []string{"s14-retry1", "s14-retry2", "s14-retry3"} {
		e.PatchEnv(name, map[string]string{
			"RL_IP_RATE":     "5",
			"RL_IP_BURST":    "3",
			"RL_GROUP_RATE":  "1000",
			"RL_GROUP_BURST": "500",
		})
	}

	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle")

	// 1% loss on listeners.
	for _, l := range []string{"s14-listener1", "s14-listener2", "s14-listener3"} {
		if err := env.ApplyNetemLoss(ctx, l, 1.0); err != nil {
			t.Fatalf("netem loss %s: %v", l, err)
		}
		t.Cleanup(func() { env.RemoveNetemLoss(ctx, l) }) //nolint:errcheck
	}

	e.Sleep(12*time.Second, "beacon discovery convergence")

	beforeR := snapshotRetries(t, e, ctx, "s14")

	genCmd := subtxGenCmd("[fd10::2]:9000")
	genCmd = append(genCmd,
		"-pps", "500",
		"-duration", "15s",
		"-seq-gap-every", "20",
		"-seq-gap-size", "2",
		"-seq-gap-delay", "2s",
	)
	startGenerator(t, ctx, "s14", genCmd)
	waitGenerator(t, ctx, "s14")

	e.Sleep(20*time.Second, "backoff escalation drain")

	afterR := scrapeRetries(t, e, ctx, "s14")
	e.LogContainerOutput(ctx, "s14-source")

	// Check per-IP rate limit drops on all 3 endpoints.
	totalIPDrops := 0.0
	for i, label := range []string{"retry1", "retry2", "retry3"} {
		drops := retryDelta(i, "bre_rate_limit_drops_total", beforeR, afterR)
		t.Logf("%s: rate_limit_drops=%.0f", label, drops)
		totalIPDrops += drops
	}

	metrics.AssertGT(t, "total rate limit drops across endpoints", totalIPDrops)
}
