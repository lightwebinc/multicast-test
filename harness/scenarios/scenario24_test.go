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

// Scenario 24 — BRC-130 fragmentation: payload hash verification
//
// Mirrors scenarios/24-fragmentation-hash-verify/run.sh.
//
// Proxy with FRAG_MTU=1500. listener1 has VERIFY_PAYLOAD_HASH=true.
// Honest payloads → 0 hash mismatches, forwarded ≈ completed.
func TestScenario24_FragmentationHashVerify(t *testing.T) {
	ctx := context.Background()
	e := env.New(t, dockerdriver.New())

	penv := proxyEnv()
	penv["FRAG_MTU"] = "1500"
	e.AddNode(driver.NodeConfig{
		Name:        "s24-proxy",
		Image:       "bitcoin-shard-proxy:harness",
		IPv6:        "fd10::2",
		Env:         penv,
		MetricsPort: 9100,
		Role:        driver.RoleProxy,
	})

	l1env := listenerEnv()
	l1env["VERIFY_PAYLOAD_HASH"] = "true"
	e.AddNode(driver.NodeConfig{
		Name:        "s24-listener1",
		Image:       "bitcoin-shard-listener:harness",
		IPv6:        "fd10::11",
		Env:         l1env,
		MetricsPort: 9200,
		Role:        driver.RoleListener,
	})

	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle")

	beforeL1 := e.Snapshot(ctx, "s24-listener1")

	genCmd := subtxGenCmd("[fd10::2]:9000")
	genCmd = append(genCmd, "-payload-size", "2048")
	startGenerator(t, ctx, "s24", genCmd)
	waitGenerator(t, ctx, "s24")

	e.Sleep(12*time.Second, "reassembly pipeline drain")

	urlL1 := e.MetricsURL(ctx, "s24-listener1")
	afterL1 := metrics.ScrapeOrFail(t, urlL1)

	deltaL1 := metrics.DeltaMap(beforeL1, afterL1)
	completed := deltaL1["bsl_reassembly_completed_total"]
	mismatch := deltaL1["bsl_reassembly_hash_mismatch_total"]
	fwd := deltaL1["bsl_frames_forwarded_total"]

	t.Logf("listener1: completed=%.0f hash_mismatch=%.0f fwd=%.0f", completed, mismatch, fwd)

	metrics.AssertGT(t, "reassembly completed", completed)
	metrics.AssertZero(t, "hash mismatch", mismatch)
	metrics.AssertNear(t, "forwarded ≈ completed", fwd, completed, 0.10)
}
