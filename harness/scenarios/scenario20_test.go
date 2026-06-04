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

// Scenario 20 — BRC-127 subtree group announce: dynamic filtering
//
// Mirrors scenarios/20-subtree-group-announce/run.sh.
//
// source → TCP SubtreeGroupAnnounce → proxy → ff05::b:fffc → listener3 registry.
// listener3 has SUBTREE_GROUPS configured and no SUBTREE_INCLUDE, relying on
// the announce to populate its subtree registry for forwarding.
func TestScenario20_SubtreeGroupAnnounce(t *testing.T) {
	ctx := context.Background()
	e := env.New(t, dockerdriver.New())

	penv := proxyEnv()
	penv["TCP_LISTEN_PORT"] = "9002"
	e.AddNode(driver.NodeConfig{
		Name:        "s20-proxy",
		Image:       "shard-proxy:harness",
		IPv6:        "fd10::2",
		Env:         penv,
		MetricsPort: 9100,
		Role:        driver.RoleProxy,
	})

	l3env := listenerEnv()
	delete(l3env, "SUBTREE_INCLUDE")
	l3env["SUBTREE_GROUPS"] = "bfbfbfbfbfbfbfbfbfbfbfbfbfbfbfbf"
	l3env["ANNOUNCE_SCOPE"] = "site"
	e.AddNode(driver.NodeConfig{
		Name:        "s20-listener3",
		Image:       "shard-listener:harness",
		IPv6:        "fd10::13",
		Env:         l3env,
		MetricsPort: 9200,
		Role:        driver.RoleListener,
	})

	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle")

	// Send SubtreeGroupAnnounce so listener3 populates its registry.
	announceCmd := subtxGenCmd("[fd10::2]:9000")
	announceCmd = append(announceCmd,
		"-announce-addr", "[fd10::2]:9002",
		"-subtree-group", "bfbfbfbfbfbfbfbfbfbfbfbfbfbfbfbf",
		"-announce-interval", "1s",
		"-announce-ttl", "30",
	)
	startGenerator(t, ctx, "s20", announceCmd)

	// Wait for announce to propagate, then start measuring.
	e.Sleep(5*time.Second, "subtree announce propagation")
	beforeL3 := e.Snapshot(ctx, "s20-listener3")

	// s20-source runs both announce + traffic (subtxGenCmd included);
	// wait for it to finish — traffic runs for the remaining ~5 s.
	waitGenerator(t, ctx, "s20")

	e.Sleep(3*time.Second, "pipeline drain")

	urlL3 := e.MetricsURL(ctx, "s20-listener3")
	afterL3 := metrics.ScrapeOrFail(t, urlL3)

	deltaL3 := metrics.DeltaMap(beforeL3, afterL3)
	recv := deltaL3["bsl_frames_received_total"]
	fwd := deltaL3["bsl_frames_forwarded_total"]
	egrErr := deltaL3["bsl_egress_errors_total"]
	missed := deltaL3["bsl_frames_dropped_total"]

	t.Logf("listener3: received=%.0f forwarded=%.0f egrErr=%.0f dropped=%.0f", recv, fwd, egrErr, missed)

	metrics.AssertGT(t, "listener3 received", recv)
	metrics.AssertGT(t, "listener3 forwarded", fwd)
	// All 8 subtrees should be forwarded (announce covers all).
	metrics.AssertNear(t, "forwarded+egrErr ≈ received", fwd+egrErr, recv, 0.10)
}
