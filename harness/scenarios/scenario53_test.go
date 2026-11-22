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

// Scenario 53: TxID dedup: HA sibling failover
//
// Ported from the retired bash scenario 53.
//
// Three HA sibling listeners (one DEPLOYMENT_ID) share Redis-backed TxID
// dedup, so each TxID goes to egress once across them. listener2 is stopped
// between two traffic phases. The survivors must take over its share: in
// phase 2, listener1 and listener3 still deliver every TxID exactly once and
// keep deduplicating against each other.
//
// The listener dials one Redis address and has no Sentinel client, so a
// Redis primary-to-replica failover is not something it can follow.
func TestScenario53_TxIDDedupFailover(t *testing.T) {
	ctx := context.Background()
	e := env.New(t, dockerdriver.New())

	e.AddNode(dedupRedisNode("s53-redis"))

	e.AddNode(driver.NodeConfig{
		Name:        "s53-proxy",
		Image:       "shard-proxy:harness",
		IPv6:        "fd10::2",
		Env:         proxyEnv(),
		MetricsPort: 9100,
		Role:        driver.RoleProxy,
	})

	names := []string{"s53-listener1", "s53-listener2", "s53-listener3"}
	for i, suffix := range []string{"1", "2", "3"} {
		lenv := dedupListenerEnv("s53")
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

	// Phase 1: all three siblings up.
	before1 := snapshotListeners(t, e, ctx, "s53")
	genCmd1 := subtxGenCmd("[fd10::2]:8725")
	genCmd1 = append(genCmd1, "-duration", "5s")
	startGenerator(t, ctx, "s53", genCmd1)
	waitGenerator(t, ctx, "s53")
	e.Sleep(2*time.Second, "drain phase 1")
	after1 := settleDedupListeners(t, ctx, e, names, 30*time.Second)

	var d1 [3]map[string]float64
	for i := range names {
		d1[i] = metrics.DeltaMap(before1[i], after1[i])
	}
	recvL1 := d1[0]["bsl_frames_received_total"]
	var total1, dedup1, claimErr1 float64
	for i, label := range []string{"listener1", "listener2", "listener3"} {
		delivered := d1[i]["bsl_frames_forwarded_total"] + d1[i]["bsl_egress_errors_total"]
		total1 += delivered
		dedup1 += d1[i]["bsl_frames_tx_deduped_total"]
		claimErr1 += d1[i]["bsl_txid_dedup_errors_total"]
		t.Logf("phase 1 %s: received=%.0f delivered=%.0f tx_deduped=%.0f",
			label, d1[i]["bsl_frames_received_total"], delivered, d1[i]["bsl_frames_tx_deduped_total"])
	}
	delivered2p1 := d1[1]["bsl_frames_forwarded_total"] + d1[1]["bsl_egress_errors_total"]

	// listener1 has no filters, so its receive count is every TxID sent.
	// Delivered = forwarded + egress errors: the egress target is a blackhole,
	// so a refused write is still a forward attempt.
	metrics.AssertNear(t, "phase 1: siblings' delivered ≈ l1 received (each TxID once)", total1, recvL1, 0.05)
	metrics.AssertGT(t, "phase 1: siblings suppress each other's copies (tx_deduped > 0)", dedup1)
	metrics.AssertGT(t, "phase 1: listener2 wins a share of the claims", delivered2p1)
	metrics.AssertZero(t, "phase 1: Redis claim errors == 0", claimErr1)

	// Fail listener2.
	if err := e.Driver.Stop(ctx, "s53-listener2"); err != nil {
		t.Fatalf("stop listener2: %v", err)
	}
	e.Sleep(2*time.Second, "listener2 down settle")

	// Phase 2: survivors only.
	survivors := []string{"s53-listener1", "s53-listener3"}
	before2 := []map[string]float64{e.Snapshot(ctx, survivors[0]), e.Snapshot(ctx, survivors[1])}
	genCmd2 := subtxGenCmd("[fd10::2]:8725")
	genCmd2 = append(genCmd2, "-duration", "5s")
	startGenerator(t, ctx, "s53-phase2", genCmd2)
	waitGenerator(t, ctx, "s53-phase2")
	e.Sleep(2*time.Second, "drain phase 2")
	after2 := settleDedupListeners(t, ctx, e, survivors, 30*time.Second)

	var total2, dedup2, claimErr2 float64
	var recvL1p2 float64
	for i, label := range []string{"listener1", "listener3"} {
		d := metrics.DeltaMap(before2[i], after2[i])
		if i == 0 {
			recvL1p2 = d["bsl_frames_received_total"]
		}
		delivered := d["bsl_frames_forwarded_total"] + d["bsl_egress_errors_total"]
		total2 += delivered
		dedup2 += d["bsl_frames_tx_deduped_total"]
		claimErr2 += d["bsl_txid_dedup_errors_total"]
		t.Logf("phase 2 %s: received=%.0f delivered=%.0f tx_deduped=%.0f",
			label, d["bsl_frames_received_total"], delivered, d["bsl_frames_tx_deduped_total"])
	}

	metrics.AssertNear(t, "phase 2: survivors' delivered ≈ l1 received (listener2's share taken over)", total2, recvL1p2, 0.05)
	metrics.AssertGT(t, "phase 2: survivors still suppress each other's copies (tx_deduped > 0)", dedup2)
	metrics.AssertZero(t, "phase 2: Redis claim errors == 0", claimErr2)
}
