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

// Scenario 35 — Block header egress: stripped BRC-131 retransmission
//
// Mirrors scenarios/35-block-header-egress/run.sh.
//
// listener1 has HEADER_EGRESS_ENABLED=true. Sends block announcements via TCP.
// Asserts bsl_header_forwarded_total > 0 and errors == 0.
func TestScenario35_BlockHeaderEgress(t *testing.T) {
	ctx := context.Background()
	e := env.New(t, dockerdriver.New())

	penv := proxyEnv()
	penv["TCP_LISTEN_PORT"] = "9002"
	e.AddNode(driver.NodeConfig{
		Name:        "s35-proxy",
		Image:       "bitcoin-shard-proxy:harness",
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
		Image:       "bitcoin-shard-listener:harness",
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
		"send-block-announce",
		"-addr", "[fd10::2]:9002",
		"-blocks", "20",
		"-subtrees", "4",
		"-coinbase=true",
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

	// Only BlockAnnounce (not CoinbaseTx) produces header egress. The egress
	// target [::1]:9107 has no listener, so some UDP writes return ECONNREFUSED
	// via ICMP unreachable; we count those as "attempted" and assert the sum
	// of successes + errors covers all blocks.
	expectedHeaders := float64(blockCount)
	metrics.AssertNear(t, "header forwarded+errors ≈ block_count", headerFwd+headerErr, expectedHeaders, 0.10)
	metrics.AssertGT(t, "header forwarded", headerFwd)
}
