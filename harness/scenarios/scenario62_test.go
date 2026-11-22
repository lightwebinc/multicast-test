package scenarios

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/lightwebinc/multicast-test/harness/driver"
	dockerdriver "github.com/lightwebinc/multicast-test/harness/driver/docker"
	"github.com/lightwebinc/multicast-test/harness/env"
	"github.com/lightwebinc/multicast-test/harness/metrics"
)

// Scenario 62: SSM block broadcast follows the publisher roster
//
// Under SSM a receiver joins each group for a list of sources. Block
// announces leave from the emitting proxy's own address (BRC-129, BRC-133),
// so the block-broadcast group (0xFFFE) must be joined for the publishers,
// the proxies, like the shard groups. A decoy at another source also emits
// block frames on 0xFFFE, and is the one address in the manifest-announcer
// list. The listener and the retry endpoint must take only the publisher's
// block frames: joined for the manifest announcers instead, they take the
// decoy's and miss every block the proxy sends.
//
// Source filtering happens in each receiver's kernel (MCAST_JOIN_SOURCE_GROUP);
// the bridge forwards the group to every member.
func TestScenario62_SSMBlockBroadcastPublishers(t *testing.T) {
	const (
		publisher = "fd10::2"
		decoy     = "fd10::6"
		blocks    = 20 // from the publisher
		decoyN    = 12 // from the decoy
	)
	ctx := context.Background()
	e := env.New(t, dockerdriver.New())

	for _, p := range []struct{ name, ip string }{{"s62-proxy", publisher}, {"s62-decoy", decoy}} {
		penv := proxyEnv()
		penv["SOURCE_MODE"] = "ssm"
		penv["BIND_SOURCE"] = p.ip
		penv["BLOCK_LISTEN_PORT"] = "8727"
		e.AddNode(driver.NodeConfig{
			Name:        p.name,
			Image:       "shard-proxy:harness",
			IPv6:        p.ip,
			Env:         penv,
			MetricsPort: 9100,
			Role:        driver.RoleProxy,
		})
	}

	ssm := map[string]string{
		"SOURCE_MODE":            "ssm",
		"SSM_PUBLISHERS_STATIC":  publisher,
		"SSM_BOOTSTRAP_MANIFEST": decoy,
	}
	lenv := listenerEnv()
	for k, v := range ssm {
		lenv[k] = v
	}
	e.AddNode(driver.NodeConfig{
		Name:        "s62-listener1",
		Image:       "shard-listener:harness",
		IPv6:        "fd10::11",
		Env:         lenv,
		MetricsPort: 9200,
		Role:        driver.RoleListener,
	})
	renv := retryEnv()
	for k, v := range ssm {
		renv[k] = v
	}
	e.AddNode(driver.NodeConfig{
		Name:        "s62-retry1",
		Image:       "retry-endpoint:harness",
		IPv6:        "fd10::20",
		Env:         renv,
		MetricsPort: 9400,
		Role:        driver.RoleRetry,
	})

	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle")

	beforeL := e.Snapshot(ctx, "s62-listener1")
	urlR := e.MetricsURL(ctx, "s62-retry1")
	storedPub := metrics.LabelledValue(t, urlR, "bre_cache_stored_total", "source", publisher)
	storedDecoy := metrics.LabelledValue(t, urlR, "bre_cache_stored_total", "source", decoy)

	push := func(proxy string, count int) []string {
		return []string{
			"send-block-push",
			"-addr", "[" + proxy + "]:8727",
			"-count", strconv.Itoa(count),
			"-subtrees", "4",
			"-interval", "50ms",
		}
	}
	startSourceAt(t, ctx, "s62-source", "fd10::3", push(publisher, blocks))
	startSourceAt(t, ctx, "s62-decoy-source", "fd10::7", push(decoy, decoyN))
	waitSource(t, ctx, "s62-source")
	waitSource(t, ctx, "s62-decoy-source")
	e.Sleep(3*time.Second, "multicast pipeline drain")

	d := metrics.DeltaMap(beforeL, metrics.ScrapeOrFail(t, e.MetricsURL(ctx, "s62-listener1")))
	recv := d["bsl_frames_received_total"]
	delivered := d["bsl_frames_forwarded_total"] + d["bsl_egress_errors_total"]
	storedPub = metrics.LabelledValue(t, urlR, "bre_cache_stored_total", "source", publisher) - storedPub
	storedDecoy = metrics.LabelledValue(t, urlR, "bre_cache_stored_total", "source", decoy) - storedDecoy

	t.Logf("listener1: block frames received=%.0f delivered=%.0f (publisher sent %d, decoy %d)",
		recv, delivered, blocks, decoyN)
	t.Logf("retry1: cached from the publisher=%.0f, from the decoy=%.0f", storedPub, storedDecoy)

	// One fabric frame per block (scenario 30): the listener takes exactly the
	// publisher's blocks, and none of the decoy's.
	metrics.AssertEq(t, "listener1 block frames received == the publisher's", recv, blocks)
	metrics.AssertEq(t, "listener1 delivered == received", delivered, recv)
	metrics.AssertEq(t, "retry1 cached the publisher's block frames", storedPub, blocks)
	metrics.AssertZero(t, "retry1 cached none of the decoy's", storedDecoy)
}
