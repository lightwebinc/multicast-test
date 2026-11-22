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

// Scenario 63: SSM manifest consumer at the stock ports
//
// The BRC-139 manifest consumer's socket and the receive workers share UDP
// 9001: MANIFEST_BEACON_PORT and LISTEN_PORT both default to it. Under SSM with
// the consumer on, every worker must still bind (/readyz reports all of them
// ready), and unicast to the data port must reach the workers, never the
// manifest socket. The unicast comes from several sockets, so the kernel
// spreads it over every socket in the port's reuseport group.
func TestScenario63_SSMManifestConsumerStockPorts(t *testing.T) {
	const (
		publisher = "fd10::2"
		manifests = "fd10::60" // no announcer runs: the consumer's socket binds regardless
		listener  = "fd10::11"
		workers   = 4
	)
	ctx := context.Background()
	e := env.New(t, dockerdriver.New())

	penv := proxyEnv()
	penv["SOURCE_MODE"] = "ssm"
	penv["BIND_SOURCE"] = publisher
	e.AddNode(driver.NodeConfig{
		Name:        "s63-proxy",
		Image:       "shard-proxy:harness",
		IPv6:        publisher,
		Env:         penv,
		MetricsPort: 9100,
		Role:        driver.RoleProxy,
	})
	lenv := listenerEnv()
	for k, v := range map[string]string{
		"SOURCE_MODE":               "ssm",
		"SSM_PUBLISHERS_STATIC":     publisher,
		"SSM_BOOTSTRAP_MANIFEST":    manifests,
		"MANIFEST_CONSUMER_ENABLED": "true",
		"NUM_WORKERS":               strconv.Itoa(workers),
	} {
		lenv[k] = v
	}
	e.AddNode(driver.NodeConfig{
		Name:        "s63-listener1",
		Image:       "shard-listener:harness",
		IPv6:        listener,
		Env:         lenv,
		MetricsPort: 9200,
		Role:        driver.RoleListener,
	})

	// A worker that cannot bind beside the manifest socket keeps /readyz at
	// 503, and StartAll fails here.
	e.StartAll(ctx)
	ready, total := workersReady(t, ctx, e, "s63-listener1")
	t.Logf("listener1: workers ready=%.0f of %.0f", ready, total)
	metrics.AssertEq(t, "listener1 workers", total, workers)
	metrics.AssertEq(t, "listener1 workers ready", ready, workers)
	e.Sleep(4*time.Second, "MLD querier settle")

	before := e.Snapshot(ctx, "s63-listener1")
	startSourceAt(t, ctx, "s63-source", "fd10::3", []string{
		"-addr", "[" + listener + "]:9001",
		"-shard-bits", "2",
		"-workers", "8",
		"-pps", "500",
		"-duration", "4s",
		"-payload-size", "256",
		"-log-interval", "2s",
	})
	waitSource(t, ctx, "s63-source")
	sent := sourceSent(t, ctx, "s63-source")
	e.Sleep(2*time.Second, "pipeline drain")

	d := metrics.DeltaMap(before, metrics.ScrapeOrFail(t, e.MetricsURL(ctx, "s63-listener1")))
	recv := d["bsl_frames_received_total"]
	t.Logf("listener1: unicast sent=%.0f received=%.0f delivered=%.0f", sent, recv, listenerDelivered(d))

	metrics.AssertGT(t, "unicast frames sent to the data port", sent)
	metrics.AssertEq(t, "unicast frames the workers received == sent", recv, sent)
	metrics.AssertEq(t, "listener1 delivered == received", listenerDelivered(d), recv)
}
