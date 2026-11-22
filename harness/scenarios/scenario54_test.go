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

// Scenario 54: egress dedup keeps two senders' frames apart
//
// Two proxies each stamp their own flows: HashKey = XXH64(sender ∥ group ∥
// subtree), and every flow's SeqNum starts at 1. Frames from the two
// therefore share (group, subtree, SeqNum) and differ only in HashKey. The
// listener runs its local egress dedup (EGRESS_DEDUP_CAP), which suppresses a
// retransmit that duplicates a frame it already forwarded; keyed without the
// HashKey it took the second sender's frame for such a duplicate and dropped
// it. Every frame of both senders must be delivered and none deduplicated.
func TestScenario54_EgressDedupTwoSenders(t *testing.T) {
	ctx := context.Background()
	e := env.New(t, dockerdriver.New())

	for _, p := range []struct{ name, ip string }{{"s54-proxy-a", "fd10::2"}, {"s54-proxy-b", "fd10::4"}} {
		e.AddNode(driver.NodeConfig{
			Name:        p.name,
			Image:       "shard-proxy:harness",
			IPv6:        p.ip,
			Env:         proxyEnv(),
			MetricsPort: 9100,
			Role:        driver.RoleProxy,
		})
	}

	lenv := listenerEnv()
	lenv["EGRESS_DEDUP_CAP"] = "262144"
	e.AddNode(driver.NodeConfig{
		Name:        "s54-listener1",
		Image:       "shard-listener:harness",
		IPv6:        "fd10::11",
		Env:         lenv,
		MetricsPort: 9200,
		Role:        driver.RoleListener,
	})

	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle")

	before := e.Snapshot(ctx, "s54-listener1")

	// Same rate and subtree pool from both, so the two proxies' flows advance
	// side by side and their SeqNums meet well inside the dedup TTL (2s).
	cmdA := subtxGenCmd("[fd10::2]:8725")
	cmdB := subtxGenCmd("[fd10::4]:8725")
	startSourceAt(t, ctx, "s54-source-a", "fd10::3", cmdA)
	startSourceAt(t, ctx, "s54-source-b", "fd10::5", cmdB)
	waitSource(t, ctx, "s54-source-a")
	waitSource(t, ctx, "s54-source-b")
	e.Sleep(2*time.Second, "egress pipeline drain")

	after := metrics.ScrapeOrFail(t, e.MetricsURL(ctx, "s54-listener1"))
	sentA := sourceSent(t, ctx, "s54-source-a")
	sentB := sourceSent(t, ctx, "s54-source-b")

	d := metrics.DeltaMap(before, after)
	recv := d["bsl_frames_received_total"]
	deduped := d["bsl_frames_deduped_total"]
	delivered := d["bsl_frames_forwarded_total"] + d["bsl_egress_errors_total"]

	t.Logf("sources: a sent=%.0f, b sent=%.0f", sentA, sentB)
	t.Logf("listener1: received=%.0f deduped=%.0f delivered=%.0f", recv, deduped, delivered)

	metrics.AssertNear(t, "listener1 received both senders' frames", recv, sentA+sentB, 0.05)
	metrics.AssertZero(t, "frames suppressed by the egress dedup", deduped)
	// The harness egress target is a blackhole: refused writes count as
	// egress errors, and as forward attempts.
	metrics.AssertEq(t, "delivered (forwarded + egress errors) == received", delivered, recv)
}
