package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/lightwebinc/bitcoin-multicast-test/harness/driver"
	dockerdriver "github.com/lightwebinc/bitcoin-multicast-test/harness/driver/docker"
	"github.com/lightwebinc/bitcoin-multicast-test/harness/env"
	"github.com/lightwebinc/bitcoin-multicast-test/harness/metrics"
)

// Scenario 05 — Multicast egress bridge (group re-mapping)
//
// Mirrors scenarios/05-mc-egress-bridge/run.sh.
//
// listener1 re-emits frames from site-local to link-local.
// listener4 subscribes to link-local and must receive frames.
func TestScenario05_McEgressBridge(t *testing.T) {
	ctx := context.Background()
	e := env.New(t, dockerdriver.New())

	e.AddNode(driver.NodeConfig{
		Name:        "s05-proxy",
		Image:       "bitcoin-shard-proxy:harness",
		IPv6:        "fd10::2",
		Env:         proxyEnv(),
		MetricsPort: 9100,
		Role:        driver.RoleProxy,
	})

	l1env := listenerEnv()
	l1env["MC_EGRESS_ENABLED"] = "true"
	l1env["MC_EGRESS_SCOPE"] = "link"
	e.AddNode(driver.NodeConfig{
		Name:        "s05-listener1",
		Image:       "bitcoin-shard-listener:harness",
		IPv6:        "fd10::11",
		Env:         l1env,
		MetricsPort: 9200,
		Role:        driver.RoleListener,
	})

	l4env := listenerEnv()
	l4env["MC_SCOPE"] = "link"
	l4env["BEACON_ENABLED"] = "false"
	e.AddNode(driver.NodeConfig{
		Name:        "s05-listener4",
		Image:       "bitcoin-shard-listener:harness",
		IPv6:        "fd10::14",
		Env:         l4env,
		MetricsPort: 9201,
		Role:        driver.RoleListener,
	})

	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle")

	beforeL1 := e.Snapshot(ctx, "s05-listener1")
	beforeL4 := e.Snapshot(ctx, "s05-listener4")

	genCmd := subtxGenCmd("[fd10::2]:9000")
	startGenerator(t, ctx, "s05", genCmd)
	waitGenerator(t, ctx, "s05")

	e.Sleep(3*time.Second, "pipeline drain")

	urlL1 := e.MetricsURL(ctx, "s05-listener1")
	afterL1 := metrics.ScrapeOrFail(t, urlL1)
	urlL4 := e.MetricsURL(ctx, "s05-listener4")
	afterL4 := metrics.ScrapeOrFail(t, urlL4)

	deltaL1 := metrics.DeltaMap(beforeL1, afterL1)
	deltaL4 := metrics.DeltaMap(beforeL4, afterL4)

	l1Recv := deltaL1["bsl_frames_received_total"]
	l4Recv := deltaL4["bsl_frames_received_total"]
	l4Fwd := deltaL4["bsl_frames_forwarded_total"]

	t.Logf("listener1: received=%.0f", l1Recv)
	t.Logf("listener4: received=%.0f forwarded=%.0f", l4Recv, l4Fwd)

	metrics.AssertGT(t, "listener1 received", l1Recv)
	metrics.AssertGT(t, "listener4 received (via egress bridge)", l4Recv)
	metrics.AssertGT(t, "listener4 forwarded", l4Fwd)
	// l4 should receive roughly what l1 forwarded.
	metrics.AssertNear(t, "l4 recv ≈ l1 recv", l4Recv, l1Recv, 0.20)
}
