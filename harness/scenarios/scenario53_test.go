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

// Scenario 53 — TxID dedup: sentinel failover
//
// Mirrors scenarios/53-txid-dedup-failover/run.sh.
//
// Two Redis containers (primary + replica). Kill primary, verify listeners
// reconnect to replica and continue dedup.
func TestScenario53_TxIDDedupFailover(t *testing.T) {
	ctx := context.Background()
	e := env.New(t, dockerdriver.New())

	// Redis primary.
	e.AddNode(driver.NodeConfig{
		Name:  "s53-redis-primary",
		Image: "redis:7-alpine",
		IPv6:  "fd10::30",
		Role:  driver.RoleAux,
	})

	// Redis replica (used after failover).
	e.AddNode(driver.NodeConfig{
		Name:  "s53-redis-replica",
		Image: "redis:7-alpine",
		IPv6:  "fd10::31",
		Cmd:   []string{"redis-server", "--replicaof", "fd10::30", "6379"},
		Role:  driver.RoleAux,
	})

	e.AddNode(driver.NodeConfig{
		Name:        "s53-proxy",
		Image:       "shard-proxy:harness",
		IPv6:        "fd10::2",
		Env:         proxyEnv(),
		MetricsPort: 9100,
		Role:        driver.RoleProxy,
	})

	// Listeners configured with both Redis addresses for failover.
	redisAddrs := "fd10::30:6379,fd10::31:6379"
	for i, suffix := range []string{"1", "2", "3"} {
		lenv := listenerEnv()
		lenv["TXID_DEDUP_ENABLED"] = "true"
		lenv["TXID_DEDUP_REDIS_ADDR"] = redisAddrs
		switch suffix {
		case "2":
			lenv["SHARD_INCLUDE"] = "0,1"
			lenv["SUBTREE_EXCLUDE"] = subtreeExcludeL2
		case "3":
			lenv["SUBTREE_INCLUDE"] = subtreeIncludeL3
		}
		e.AddNode(driver.NodeConfig{
			Name:        "s53-listener" + suffix,
			Image:       "shard-listener:harness",
			IPv6:        fmt.Sprintf("fd10::1%d", i+1),
			Env:         lenv,
			MetricsPort: 9200,
			Role:        driver.RoleListener,
		})
	}

	e.StartAll(ctx)
	e.Sleep(5*time.Second, "MLD + Redis replication settle")

	// Phase 1: traffic with primary up.
	genCmd1 := subtxGenCmd("[fd10::2]:8725")
	genCmd1 = append(genCmd1, "-duration", "5s")
	startGenerator(t, ctx, "s53", genCmd1)
	waitGenerator(t, ctx, "s53")
	e.Sleep(2*time.Second, "drain phase 1")

	// Kill primary.
	if err := e.Driver.Stop(ctx, "s53-redis-primary"); err != nil {
		t.Fatalf("stop primary redis: %v", err)
	}
	e.Sleep(3*time.Second, "failover settle")

	// Phase 2: traffic with only replica.
	beforeL := snapshotListeners(t, e, ctx, "s53")

	genCmd2 := subtxGenCmd("[fd10::2]:8725")
	genCmd2 = append(genCmd2, "-duration", "5s")
	startGenerator(t, ctx, "s53-phase2", genCmd2)
	waitGenerator(t, ctx, "s53-phase2")
	e.Sleep(2*time.Second, "drain phase 2")

	afterL := scrapeListeners(t, e, ctx, "s53")

	// Listeners must still forward traffic after failover.
	for i, label := range []string{"listener1", "listener2", "listener3"} {
		delta := metrics.DeltaMap(beforeL[i], afterL[i])
		fwd := delta["bsl_frames_forwarded_total"]
		t.Logf("%s: forwarded=%.0f (post-failover)", label, fwd)
		metrics.AssertGT(t, label+" forwarded (post-failover)", fwd)
	}
}
