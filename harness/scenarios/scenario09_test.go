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

// Scenario 09 — Listener payload hash verification
//
// Mirrors scenarios/09-listener-payload-verification/run.sh.
//
// listener1 has VERIFY_PAYLOAD_HASH=true. The generator runs with
// -corrupt-txid-rate 50 so ~50% of frames have corrupted TxIDs.
// Assertions:
//   - invalid_payload ≈ 50% of received (±20%)
//   - forwarded ≈ 50% of received (±20%)
func TestScenario09_ListenerPayloadVerification(t *testing.T) {
	ctx := context.Background()
	e := env.New(t, dockerdriver.New())

	e.AddNode(driver.NodeConfig{
		Name:        "s09-proxy",
		Image:       "bitcoin-shard-proxy:harness",
		IPv6:        "fd10::2",
		Env:         proxyEnv(),
		MetricsPort: 9100,
		Role:        driver.RoleProxy,
	})

	l1env := listenerEnv()
	l1env["VERIFY_PAYLOAD_HASH"] = "true"
	e.AddNode(driver.NodeConfig{
		Name:        "s09-listener1",
		Image:       "bitcoin-shard-listener:harness",
		IPv6:        "fd10::11",
		Env:         l1env,
		MetricsPort: 9200,
		Role:        driver.RoleListener,
	})

	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle")

	beforeL1 := e.Snapshot(ctx, "s09-listener1")

	genCmd := subtxGenCmd("[fd10::2]:9000")
	genCmd = append(genCmd, "-corrupt-txid-rate", "50")
	startGenerator(t, ctx, "s09", genCmd)
	waitGenerator(t, ctx, "s09")

	e.Sleep(2*time.Second, "egress pipeline drain")

	urlL1 := e.MetricsURL(ctx, "s09-listener1")
	afterL1 := metrics.ScrapeOrFail(t, urlL1)
	e.LogContainerOutput(ctx, "s09-source")

	deltaL1 := metrics.DeltaMap(beforeL1, afterL1)
	recvL1 := deltaL1["bsl_frames_received_total"]
	invalidL1 := deltaL1["bsl_frames_invalid_payload_total"]
	passL1 := recvL1 - deltaL1["bsl_frames_dropped_total"]

	t.Logf("listener1: received=%.0f invalid=%.0f passed=%.0f", recvL1, invalidL1, passL1)

	// ~50% should be invalid (±20%).
	metrics.AssertNear(t, "listener1 invalid_payload (≈50%)", invalidL1, recvL1/2, 0.20)
	// ~50% should be forwarded (±20%).
	metrics.AssertNear(t, "listener1 forwarded (≈50%)", passL1, recvL1/2, 0.20)

	metrics.AssertGTE(t, "listener1 received > 2000", recvL1, 2000)
}
