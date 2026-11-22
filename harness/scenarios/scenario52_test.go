package scenarios

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/lightwebinc/multicast-test/harness/driver"
	dockerdriver "github.com/lightwebinc/multicast-test/harness/driver/docker"
	"github.com/lightwebinc/multicast-test/harness/env"
	"github.com/lightwebinc/multicast-test/harness/metrics"
)

// Scenario 52: TxID dedup: Redis failure (fail-open)
//
// Ported from the retired bash scenario 52.
//
// Three HA sibling listeners (one DEPLOYMENT_ID) share Redis. Phase 1 runs
// with Redis up and shows the backend is live: claim keys land in Redis and
// the siblings suppress each other's copies. Redis is then stopped. In phase
// 2 every claim fails and each listener must fail open: count the error,
// forward the frame anyway and suppress nothing.
func TestScenario52_TxIDDedupRedisFailure(t *testing.T) {
	ctx := context.Background()
	e := env.New(t, dockerdriver.New())

	e.AddNode(dedupRedisNode("s52-redis"))

	e.AddNode(driver.NodeConfig{
		Name:        "s52-proxy",
		Image:       "shard-proxy:harness",
		IPv6:        "fd10::2",
		Env:         proxyEnv(),
		MetricsPort: 9100,
		Role:        driver.RoleProxy,
	})

	names := []string{"s52-listener1", "s52-listener2", "s52-listener3"}
	for i, suffix := range []string{"1", "2", "3"} {
		lenv := dedupListenerEnv("s52")
		switch suffix {
		case "2":
			lenv["SHARD_INCLUDE"] = "0,1"
			lenv["SUBTREE_EXCLUDE"] = subtreeExcludeL2
		case "3":
			lenv["SUBTREE_INCLUDE"] = subtreeIncludeL3
		}
		e.AddNode(driver.NodeConfig{
			Name:        names[i],
			Image:       "shard-listener:harness",
			IPv6:        fmt.Sprintf("fd10::1%d", i+1),
			Env:         lenv,
			MetricsPort: 9200,
			Role:        driver.RoleListener,
		})
	}

	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier + Redis settle")

	// Phase 1: traffic with Redis up.
	before1 := snapshotListeners(t, e, ctx, "s52")
	genCmd := subtxGenCmd("[fd10::2]:8725")
	genCmd = append(genCmd, "-duration", "5s")
	startGenerator(t, ctx, "s52", genCmd)
	waitGenerator(t, ctx, "s52")
	e.Sleep(2*time.Second, "drain phase 1")
	after1 := settleDedupListeners(t, ctx, e, names, 30*time.Second)
	keys := redisClaimKeys(t, ctx, e, "s52-redis")

	var delivered1, dedup1, claimErr1 float64
	for i := range names {
		d := metrics.DeltaMap(before1[i], after1[i])
		delivered1 += d["bsl_frames_forwarded_total"] + d["bsl_egress_errors_total"]
		dedup1 += d["bsl_frames_tx_deduped_total"]
		claimErr1 += d["bsl_txid_dedup_errors_total"]
	}
	recvL1 := metrics.DeltaMap(before1[0], after1[0])["bsl_frames_received_total"]
	t.Logf("phase 1 (Redis up): l1_received=%.0f siblings_delivered=%.0f tx_deduped=%.0f claim_errors=%.0f redis_claim_keys=%.0f",
		recvL1, delivered1, dedup1, claimErr1, keys)

	metrics.AssertGT(t, "phase 1: claim keys in Redis", keys)
	metrics.AssertGT(t, "phase 1: siblings suppress each other's copies (tx_deduped > 0)", dedup1)
	metrics.AssertZero(t, "phase 1: Redis claim errors == 0", claimErr1)
	// listener1 has no filters, so its receive count is every TxID sent; each
	// goes to egress once across the siblings (egress errors are forward
	// attempts: the egress target is a blackhole).
	metrics.AssertNear(t, "phase 1: siblings' forwarded + egress errors ≈ l1 received (each TxID once)",
		delivered1, recvL1, 0.05)

	// Stop Redis mid-test.
	if err := e.Driver.Stop(ctx, "s52-redis"); err != nil {
		t.Fatalf("stop redis: %v", err)
	}
	e.Sleep(2*time.Second, "Redis down settle")

	// Phase 2: traffic with Redis down. Snapshot before this phase.
	before2 := snapshotListeners(t, e, ctx, "s52")

	genCmd2 := subtxGenCmd("[fd10::2]:8725")
	genCmd2 = append(genCmd2, "-duration", "5s")
	startGenerator(t, ctx, "s52-phase2", genCmd2)
	waitGenerator(t, ctx, "s52-phase2")

	e.Sleep(2*time.Second, "drain phase 2")
	after2 := settleDedupListeners(t, ctx, e, names, 60*time.Second)

	// Every frame reaching the claim gate gets a backend error and is
	// forwarded anyway: claim errors and forward attempts match one for one.
	for i, label := range []string{"listener1", "listener2", "listener3"} {
		d := metrics.DeltaMap(before2[i], after2[i])
		recv := d["bsl_frames_received_total"]
		fwd := d["bsl_frames_forwarded_total"]
		delivered := fwd + d["bsl_egress_errors_total"]
		dedup := d["bsl_frames_tx_deduped_total"]
		claimErr := d["bsl_txid_dedup_errors_total"]
		t.Logf("%s phase 2 (Redis down): received=%.0f forwarded=%.0f delivered=%.0f tx_deduped=%.0f claim_errors=%.0f",
			label, recv, fwd, delivered, dedup, claimErr)
		metrics.AssertGT(t, label+" phase 2: Redis claim errors counted", claimErr)
		metrics.AssertZero(t, label+" phase 2: tx_deduped == 0 (nothing suppressed)", dedup)
		metrics.AssertGT(t, label+" phase 2: forwarded (fail-open)", fwd)
		metrics.AssertEq(t, label+" phase 2: forwarded + egress errors == claim errors", delivered, claimErr)
	}
	recvL1p2 := metrics.DeltaMap(before2[0], after2[0])["bsl_frames_received_total"]
	t.Logf("listener1 received: phase 1 %.0f, phase 2 %.0f (same generator run)", recvL1, recvL1p2)
}
