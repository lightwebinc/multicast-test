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

// Scenario 65: SSM subtree data from the publishers
//
// The proxy emits BRC-132 subtree data (0xFFFB) from its own source. With
// only SSM_PUBLISHERS_STATIC set (no SSM_BOOTSTRAP_SUBTREE_ANNOUNCE), the
// listener must join 0xFFFB for the publishers. A decoy proxy at another
// source also emits subtree data. Joined for the publishers, the listener
// takes exactly the publisher's frames. Joined (*,G), as with no source list,
// it takes the decoy's too here, since the one bridge forwards every sender;
// across a PIM-SSM fabric a (*,G) join would receive nothing at all.
func TestScenario65_SSMSubtreeDataPublishers(t *testing.T) {
	const (
		publisher = "fd10::2"
		decoy     = "fd10::6"
		pushes    = 30 // to the publisher
		decoyN    = 30 // to the decoy
	)
	ctx := context.Background()
	e := env.New(t, dockerdriver.New())

	for _, p := range []struct{ name, ip string }{{"s65-proxy", publisher}, {"s65-decoy", decoy}} {
		penv := proxyEnv()
		penv["SOURCE_MODE"] = "ssm"
		penv["BIND_SOURCE"] = p.ip
		penv["SUBTREE_LISTEN_PORT"] = "8726"
		e.AddNode(driver.NodeConfig{
			Name:        p.name,
			Image:       "shard-proxy:harness",
			IPv6:        p.ip,
			Env:         penv,
			MetricsPort: 9100,
			Role:        driver.RoleProxy,
		})
	}
	lenv := listenerEnv()
	lenv["SOURCE_MODE"] = "ssm"
	lenv["SSM_PUBLISHERS_STATIC"] = publisher
	lenv["SUBTREE_DATA_ENABLED"] = "true"
	e.AddNode(driver.NodeConfig{
		Name:        "s65-listener1",
		Image:       "shard-listener:harness",
		IPv6:        "fd10::11",
		Env:         lenv,
		MetricsPort: 9200,
		Role:        driver.RoleListener,
	})

	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle")

	before := e.Snapshot(ctx, "s65-listener1")
	push := func(proxy string, count int, seed string) []string {
		return []string{
			"send-subtree-push",
			"-addr", "[" + proxy + "]:8726",
			"-count", strconv.Itoa(count),
			"-nodes", "8",
			"-interval", "50ms",
			"-seed", seed,
		}
	}
	startSourceAt(t, ctx, "s65-source", "fd10::3", push(publisher, pushes, "s65-publisher"))
	startSourceAt(t, ctx, "s65-decoy-source", "fd10::7", push(decoy, decoyN, "s65-decoy"))
	waitSource(t, ctx, "s65-source")
	waitSource(t, ctx, "s65-decoy-source")
	e.Sleep(3*time.Second, "multicast pipeline drain")

	d := metrics.DeltaMap(before, metrics.ScrapeOrFail(t, e.MetricsURL(ctx, "s65-listener1")))
	recv := d["bsl_frames_received_total"]
	t.Logf("listener1: subtree data frames received=%.0f delivered=%.0f (publisher sent %d, decoy %d)",
		recv, listenerDelivered(d), pushes, decoyN)

	metrics.AssertEq(t, "listener1 subtree data frames received == the publisher's", recv, pushes)
	metrics.AssertEq(t, "listener1 delivered == received", listenerDelivered(d), recv)
}
