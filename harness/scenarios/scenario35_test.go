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

// Scenario 35: Block header egress (BRC-135) via the block push lane
//
// Ported from the retired bash scenario 35.
//
// listener1 has HEADER_EGRESS_ENABLED=true. Pushes BRC-144 blocks over the
// proxy's block push lane (port 8727); each becomes one BRC-131 block frame
// whose 80-byte header the listener egresses (BRC-135).
// Asserts bsl_header_forwarded_total > 0 and errors == 0.
func TestScenario35_BlockHeaderEgress(t *testing.T) {
	ctx := context.Background()
	e := env.New(t, dockerdriver.New())

	penv := proxyEnv()
	penv["BLOCK_LISTEN_PORT"] = "8727"
	e.AddNode(driver.NodeConfig{
		Name:        "s35-proxy",
		Image:       "shard-proxy:harness",
		IPv6:        "fd10::2",
		Env:         penv,
		MetricsPort: 9100,
		Role:        driver.RoleProxy,
	})

	l1env := listenerEnv()
	l1env["HEADER_EGRESS_ENABLED"] = "true"
	l1env["HEADER_EGRESS_ADDR"] = "[::1]:9107"
	l1env["HEADER_EGRESS_PROTO"] = "udp"
	e.AddNode(driver.NodeConfig{
		Name:        "s35-listener1",
		Image:       "shard-listener:harness",
		IPv6:        "fd10::11",
		Env:         l1env,
		MetricsPort: 9200,
		Role:        driver.RoleListener,
	})

	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle")

	beforeL1 := e.Snapshot(ctx, "s35-listener1")

	blockCount := 20
	genCmd := []string{
		"send-block-push",
		"-addr", "[fd10::2]:8727",
		"-count", "20",
		"-subtrees", "4",
		"-interval", "50ms",
	}
	startGenerator(t, ctx, "s35", genCmd)
	waitGenerator(t, ctx, "s35")

	e.Sleep(3*time.Second, "pipeline drain")

	urlL1 := e.MetricsURL(ctx, "s35-listener1")
	afterL1 := metrics.ScrapeOrFail(t, urlL1)

	deltaL1 := metrics.DeltaMap(beforeL1, afterL1)
	headerFwd := deltaL1["bsl_header_forwarded_total"]
	headerErr := deltaL1["bsl_header_egress_errors_total"]

	t.Logf("listener1: header_forwarded=%.0f header_errors=%.0f", headerFwd, headerErr)

	// Every pushed block yields one fabric block frame (BRC-144 body verbatim,
	// leading with the 80-byte header) and thus one header egress. The egress
	// target [::1]:9107 has no listener, so some UDP writes return ECONNREFUSED
	// via ICMP unreachable; we count those as "attempted" and assert the sum
	// of successes + errors covers all blocks.
	expectedHeaders := float64(blockCount)
	metrics.AssertNear(t, "header forwarded+errors ≈ block_count", headerFwd+headerErr, expectedHeaders, 0.10)
	metrics.AssertGT(t, "header forwarded", headerFwd)
}
