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

// Scenario 51 — TxID dedup: collision (single listener, no dedup expected)
//
// Mirrors scenarios/51-txid-dedup-collision/run.sh.
//
// Single listener with dedup enabled. Since only one listener is writing,
// tx_deduped should be 0 (no collisions from other listeners).
func TestScenario51_TxIDDedupCollision(t *testing.T) {
	ctx := context.Background()
	e := env.New(t, dockerdriver.New())

	// Redis container.
	e.AddNode(driver.NodeConfig{
		Name:  "s51-redis",
		Image: "redis:7-alpine",
		IPv6:  "fd10::30",
		Role:  driver.RoleAux,
	})

	e.AddNode(driver.NodeConfig{
		Name:        "s51-proxy",
		Image:       "shard-proxy:harness",
		IPv6:        "fd10::2",
		Env:         proxyEnv(),
		MetricsPort: 9100,
		Role:        driver.RoleProxy,
	})

	l1env := listenerEnv()
	l1env["TXID_DEDUP_ENABLED"] = "true"
	l1env["TXID_DEDUP_REDIS_ADDR"] = "fd10::30:6379"
	e.AddNode(driver.NodeConfig{
		Name:        "s51-listener1",
		Image:       "shard-listener:harness",
		IPv6:        "fd10::11",
		Env:         l1env,
		MetricsPort: 9200,
		Role:        driver.RoleListener,
	})

	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier + Redis settle")

	beforeL1 := e.Snapshot(ctx, "s51-listener1")

	genCmd := subtxGenCmd("[fd10::2]:9000")
	startGenerator(t, ctx, "s51", genCmd)
	waitGenerator(t, ctx, "s51")

	e.Sleep(2*time.Second, "egress pipeline drain")

	urlL1 := e.MetricsURL(ctx, "s51-listener1")
	afterL1 := metrics.ScrapeOrFail(t, urlL1)

	deltaL1 := metrics.DeltaMap(beforeL1, afterL1)
	fwd := deltaL1["bsl_frames_forwarded_total"]
	dedup := deltaL1["bsl_frames_tx_deduped_total"]

	t.Logf("listener1: forwarded=%.0f tx_deduped=%.0f", fwd, dedup)

	metrics.AssertGT(t, "forwarded > 0", fwd)
	metrics.AssertZero(t, "tx_deduped == 0 (single listener)", dedup)
}
