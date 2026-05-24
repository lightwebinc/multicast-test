package scenarios

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/lightwebinc/bitcoin-multicast-test/harness/driver"
	dockerdriver "github.com/lightwebinc/bitcoin-multicast-test/harness/driver/docker"
	"github.com/lightwebinc/bitcoin-multicast-test/harness/env"
	"github.com/lightwebinc/bitcoin-multicast-test/harness/metrics"
)

// Scenario 52 — TxID dedup: Redis failure (fail-open)
//
// Mirrors scenarios/52-txid-dedup-redis-failure/run.sh.
//
// Starts Redis, sends some traffic, stops Redis mid-test, sends more traffic.
// Listeners should fail-open (continue forwarding despite Redis down).
func TestScenario52_TxIDDedupRedisFailure(t *testing.T) {
	ctx := context.Background()
	e := env.New(t, dockerdriver.New())

	e.AddNode(driver.NodeConfig{
		Name:  "s52-redis",
		Image: "redis:7-alpine",
		IPv6:  "fd10::30",
		Role:  driver.RoleAux,
	})

	e.AddNode(driver.NodeConfig{
		Name:        "s52-proxy",
		Image:       "bitcoin-shard-proxy:harness",
		IPv6:        "fd10::2",
		Env:         proxyEnv(),
		MetricsPort: 9100,
		Role:        driver.RoleProxy,
	})

	redisAddr := "fd10::30:6379"
	for i, suffix := range []string{"1", "2", "3"} {
		lenv := listenerEnv()
		lenv["TXID_DEDUP_ENABLED"] = "true"
		lenv["TXID_DEDUP_REDIS_ADDR"] = redisAddr
		switch suffix {
		case "2":
			lenv["SHARD_INCLUDE"] = "0,1"
			lenv["SUBTREE_EXCLUDE"] = subtreeExcludeL2
		case "3":
			lenv["SUBTREE_INCLUDE"] = subtreeIncludeL3
		}
		e.AddNode(driver.NodeConfig{
			Name:        "s52-listener" + suffix,
			Image:       "bitcoin-shard-listener:harness",
			IPv6:        fmt.Sprintf("fd10::1%d", i+1),
			Env:         lenv,
			MetricsPort: 9200,
			Role:        driver.RoleListener,
		})
	}

	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier + Redis settle")

	// Phase 1: traffic with Redis up.
	genCmd := subtxGenCmd("[fd10::2]:9000")
	genCmd = append(genCmd, "-duration", "5s")
	startGenerator(t, ctx, "s52", genCmd)
	waitGenerator(t, ctx, "s52")
	e.Sleep(2*time.Second, "drain phase 1")

	// Stop Redis mid-test.
	if err := e.Driver.Stop(ctx, "s52-redis"); err != nil {
		t.Fatalf("stop redis: %v", err)
	}
	e.Sleep(2*time.Second, "Redis down settle")

	// Phase 2: traffic with Redis down. Snapshot before this phase.
	beforeL := snapshotListeners(t, e, ctx, "s52")

	genCmd2 := subtxGenCmd("[fd10::2]:9000")
	genCmd2 = append(genCmd2, "-duration", "5s")
	startGenerator(t, ctx, "s52-phase2", genCmd2)
	waitGenerator(t, ctx, "s52-phase2")

	e.Sleep(2*time.Second, "drain phase 2")

	afterL := scrapeListeners(t, e, ctx, "s52")

	// Listeners must still forward traffic (fail-open).
	for i, label := range []string{"listener1", "listener2", "listener3"} {
		delta := metrics.DeltaMap(beforeL[i], afterL[i])
		fwd := delta["bsl_frames_forwarded_total"]
		t.Logf("%s: forwarded=%.0f (Redis down)", label, fwd)
		metrics.AssertGT(t, label+" forwarded (fail-open)", fwd)
	}
}
