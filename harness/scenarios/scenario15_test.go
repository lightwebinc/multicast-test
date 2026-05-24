package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/lightwebinc/bitcoin-multicast-test/harness/env"
	"github.com/lightwebinc/bitcoin-multicast-test/harness/metrics"
)

// Scenario 15 — Per-chain NACK rate limit
//
// Mirrors scenarios/15-chain-ratelimit/run.sh.
//
// Tight RL_CHAIN_RATE on retry1, high IP RL so only the chain tier fires.
// Gap injection creates real NACKs with non-zero ChainIDs.
func TestScenario15_ChainRatelimit(t *testing.T) {
	ctx := context.Background()
	e, _ := retryTopology(t, "s15")

	e.PatchEnv("s15-retry1", map[string]string{
		"RL_IP_RATE":        "50000",
		"RL_IP_BURST":       "10000",
		"RL_CHAIN_RATE":     "3",
		"RL_CHAIN_WINDOW":   "10s",
		"RL_SEQUENCE_MAX":   "1000",
		"RL_SEQUENCE_WINDOW": "60s",
		"RL_GROUP_RATE":     "10000",
		"RL_GROUP_BURST":    "5000",
	})

	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle")

	// 1% loss on listeners.
	for _, l := range []string{"s15-listener1", "s15-listener2", "s15-listener3"} {
		if err := env.ApplyNetemLoss(ctx, l, 1.0); err != nil {
			t.Fatalf("netem loss %s: %v", l, err)
		}
		t.Cleanup(func() { env.RemoveNetemLoss(ctx, l) }) //nolint:errcheck
	}

	beforeR := e.Snapshot(ctx, "s15-retry1")

	genCmd := subtxGenCmd("[fd10::2]:9000")
	genCmd = append(genCmd,
		"-pps", "200",
		"-duration", "15s",
		"-seq-gap-every", "30",
		"-seq-gap-size", "2",
		"-seq-gap-delay", "1s",
	)
	startGenerator(t, ctx, "s15", genCmd)
	waitGenerator(t, ctx, "s15")

	e.Sleep(10*time.Second, "NACK pipeline drain")

	urlR := e.MetricsURL(ctx, "s15-retry1")
	afterR := metrics.ScrapeOrFail(t, urlR)
	e.LogContainerOutput(ctx, "s15-source")

	deltaR := metrics.DeltaMap(beforeR, afterR)
	chainDrops := deltaR["bre_rate_limit_drops_total"]
	nacksReceived := deltaR["bre_nack_requests_total"]

	t.Logf("retry1: nacks_received=%.0f chain_drops=%.0f", nacksReceived, chainDrops)

	metrics.AssertGT(t, "NACKs received", nacksReceived)
	metrics.AssertGT(t, "chain rate limit drops", chainDrops)
}
