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

// Scenario 51: TxID dedup: collision (single listener, no dedup expected)
//
// Ported from the retired bash scenario 51.
//
// One listener claims every TxID through the Redis backend. The generator's
// TxIDs are unique, so no claim may lose: nothing is suppressed and every
// received frame goes to egress. The claim keys in Redis prove the backend was
// on the path of every forwarded frame; a listener deduplicating on its local
// set alone passes the forwarding checks too.
func TestScenario51_TxIDDedupCollision(t *testing.T) {
	ctx := context.Background()
	e := env.New(t, dockerdriver.New())

	e.AddNode(dedupRedisNode("s51-redis"))

	e.AddNode(driver.NodeConfig{
		Name:        "s51-proxy",
		Image:       "shard-proxy:harness",
		IPv6:        "fd10::2",
		Env:         proxyEnv(),
		MetricsPort: 9100,
		Role:        driver.RoleProxy,
	})

	e.AddNode(driver.NodeConfig{
		Name:        "s51-listener1",
		Image:       "shard-listener:harness",
		IPv6:        "fd10::11",
		Env:         dedupListenerEnv(""),
		MetricsPort: 9200,
		Role:        driver.RoleListener,
	})

	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier + Redis settle")

	keysBefore := redisClaimKeys(t, ctx, e, "s51-redis")
	beforeL1 := e.Snapshot(ctx, "s51-listener1")

	genCmd := subtxGenCmd("[fd10::2]:8725")
	startGenerator(t, ctx, "s51", genCmd)
	waitGenerator(t, ctx, "s51")

	e.Sleep(2*time.Second, "egress pipeline drain")

	afterL1 := settleDedupListeners(t, ctx, e, []string{"s51-listener1"}, 30*time.Second)[0]
	keys := redisClaimKeys(t, ctx, e, "s51-redis") - keysBefore

	deltaL1 := metrics.DeltaMap(beforeL1, afterL1)
	recv := deltaL1["bsl_frames_received_total"]
	fwd := deltaL1["bsl_frames_forwarded_total"]
	egrErr := deltaL1["bsl_egress_errors_total"]
	dedup := deltaL1["bsl_frames_tx_deduped_total"]
	claimErr := deltaL1["bsl_txid_dedup_errors_total"]
	// The egress target is a blackhole: refused writes count as egress
	// errors, and a forward attempt either way.
	delivered := fwd + egrErr

	t.Logf("listener1: received=%.0f forwarded=%.0f egress_errors=%.0f tx_deduped=%.0f claim_errors=%.0f redis_claim_keys=%.0f",
		recv, fwd, egrErr, dedup, claimErr, keys)

	metrics.AssertGT(t, "forwarded > 0", fwd)
	metrics.AssertZero(t, "tx_deduped == 0 (single listener, unique TxIDs)", dedup)
	metrics.AssertZero(t, "Redis claim errors == 0", claimErr)
	metrics.AssertEq(t, "Redis claim keys == forwarded + egress errors", keys, delivered)
	metrics.AssertNear(t, "forwarded + egress errors ≈ received", delivered, recv, 0.05)
}
