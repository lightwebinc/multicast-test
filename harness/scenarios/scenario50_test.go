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

// Scenario 50 — TxID dedup basic: cross-listener deduplication
//
// Mirrors scenarios/50-txid-dedup-basic/run.sh.
//
// All 3 listeners share a Redis. Each TxID forwarded once total
// (first-writer wins). sum(forwarded) ≈ received_l1.
func TestScenario50_TxIDDedupBasic(t *testing.T) {
	ctx := context.Background()
	e := env.New(t, dockerdriver.New())

	// Redis container.
	e.AddNode(driver.NodeConfig{
		Name:  "s50-redis",
		Image: "redis:7-alpine",
		IPv6:  "fd10::30",
		Cmd:   []string{"redis-server", "--bind", "::", "--protected-mode", "no"},
		Role:  driver.RoleAux,
	})

	e.AddNode(driver.NodeConfig{
		Name:        "s50-proxy",
		Image:       "bitcoin-shard-proxy:harness",
		IPv6:        "fd10::2",
		Env:         proxyEnv(),
		MetricsPort: 9100,
		Role:        driver.RoleProxy,
	})

	redisAddr := "[fd10::30]:6379"
	for i, suffix := range []string{"1", "2", "3"} {
		lenv := listenerEnv()
		lenv["TXID_DEDUP_ADDR"] = redisAddr
		switch suffix {
		case "2":
			lenv["SHARD_INCLUDE"] = "0,1"
			lenv["SUBTREE_EXCLUDE"] = subtreeExcludeL2
		case "3":
			lenv["SUBTREE_INCLUDE"] = subtreeIncludeL3
		}
		e.AddNode(driver.NodeConfig{
			Name:        "s50-listener" + suffix,
			Image:       "bitcoin-shard-listener:harness",
			IPv6:        fmt.Sprintf("fd10::1%d", i+1),
			Env:         lenv,
			MetricsPort: 9200,
			Role:        driver.RoleListener,
		})
	}

	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier + Redis settle")

	beforeL := snapshotListeners(t, e, ctx, "s50")

	genCmd := subtxGenCmd("[fd10::2]:9000")
	startGenerator(t, ctx, "s50", genCmd)
	waitGenerator(t, ctx, "s50")

	e.Sleep(2*time.Second, "egress pipeline drain")

	afterL := scrapeListeners(t, e, ctx, "s50")

	deltaL1 := metrics.DeltaMap(beforeL[0], afterL[0])
	deltaL2 := metrics.DeltaMap(beforeL[1], afterL[1])
	deltaL3 := metrics.DeltaMap(beforeL[2], afterL[2])

	recvL1 := deltaL1["bsl_frames_received_total"]
	fwdL1 := deltaL1["bsl_frames_forwarded_total"]
	egrErrL1 := deltaL1["bsl_egress_errors_total"]
	fwdL2 := deltaL2["bsl_frames_forwarded_total"]
	egrErrL2 := deltaL2["bsl_egress_errors_total"]
	fwdL3 := deltaL3["bsl_frames_forwarded_total"]
	egrErrL3 := deltaL3["bsl_egress_errors_total"]
	totalFwd := fwdL1 + fwdL2 + fwdL3
	totalEgrErr := egrErrL1 + egrErrL2 + egrErrL3

	dedupL1 := deltaL1["bsl_frames_tx_deduped_total"]
	dedupL2 := deltaL2["bsl_frames_tx_deduped_total"]
	dedupL3 := deltaL3["bsl_frames_tx_deduped_total"]
	totalDedup := dedupL1 + dedupL2 + dedupL3

	t.Logf("l1: recv=%.0f fwd=%.0f egrErr=%.0f dedup=%.0f", recvL1, fwdL1, egrErrL1, dedupL1)
	t.Logf("l2: fwd=%.0f egrErr=%.0f dedup=%.0f", fwdL2, egrErrL2, dedupL2)
	t.Logf("l3: fwd=%.0f egrErr=%.0f dedup=%.0f", fwdL3, egrErrL3, dedupL3)
	t.Logf("total_fwd=%.0f total_egrErr=%.0f total_dedup=%.0f", totalFwd, totalEgrErr, totalDedup)

	// Each TxID is claimed by one listener; that listener attempts egress → fwd+egrErr counts it.
	// Total egress attempts (fwd + egrErr) ≈ l1 received (each TxID claimed once).
	metrics.AssertNear(t, "total fwd+egrErr ≈ l1 received", totalFwd+totalEgrErr, recvL1, 0.15)
	// TxID dedup must fire: the other listeners are suppressed.
	metrics.AssertGT(t, "total dedup > 0", totalDedup)
}
