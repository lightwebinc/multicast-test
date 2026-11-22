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

// Scenario 64: SSM auto-join follows the BRC-139 pilots
//
// A listener subscribed to shard 0 (SHARD_INCLUDE=0) adds the shards the
// manifests' pilots claim (SHARD_INCLUDE_FROM_MANIFEST). Two pilot announcers
// claim shard 1. Under SSM the listener must join shard 1's group for the
// publishers and admit its transactions (not drop them as shard_filter), then
// stop delivering them once the pilots expire. Two reference listeners
// subscribed to shards 0 and 1, and to shard 0 alone, count what each phase
// must deliver. The manifest consumer listens on its own port here, so the
// shared-port bind of scenario 63 stays out of this one.
func TestScenario64_SSMAutoJoinPilots(t *testing.T) {
	const (
		publisher = "fd10::2"
		manifests = "fd10::60,fd10::61"
		auto      = "s64-auto"
		ref01     = "s64-ref01"
		ref0      = "s64-ref0"
	)
	ctx := context.Background()
	e := env.New(t, dockerdriver.New())

	penv := proxyEnv()
	penv["SOURCE_MODE"] = "ssm"
	penv["BIND_SOURCE"] = publisher
	e.AddNode(driver.NodeConfig{
		Name:        "s64-proxy",
		Image:       "shard-proxy:harness",
		IPv6:        publisher,
		Env:         penv,
		MetricsPort: 9100,
		Role:        driver.RoleProxy,
	})
	announcers := []struct{ name, ip string }{{"s64-announcer1", "fd10::60"}, {"s64-announcer2", "fd10::61"}}
	for _, a := range announcers {
		e.AddNode(announcerNode(t, ctx, a.name, a.ip, map[string]string{
			"PORT":          "9002",
			"PILOT_ONLY":    "true",
			"JOINED_GROUPS": "1",
			"TTL":           "3s",
			"SOURCE_MODE":   "ssm",
			"PUBLISHERS":    publisher,
		}))
	}
	listeners := []struct {
		name, ip string
		env      map[string]string
	}{
		{auto, "fd10::11", map[string]string{
			"SHARD_INCLUDE":               "0",
			"SHARD_INCLUDE_FROM_MANIFEST": "true",
			"MANIFEST_CONSUMER_ENABLED":   "true",
			"MANIFEST_BEACON_PORT":        "9002",
			"SSM_BOOTSTRAP_MANIFEST":      manifests,
		}},
		{ref01, "fd10::12", map[string]string{"SHARD_INCLUDE": "0,1"}},
		{ref0, "fd10::13", map[string]string{"SHARD_INCLUDE": "0"}},
	}
	for _, l := range listeners {
		lenv := listenerEnv()
		lenv["SOURCE_MODE"] = "ssm"
		lenv["SSM_PUBLISHERS_STATIC"] = publisher
		for k, v := range l.env {
			lenv[k] = v
		}
		e.AddNode(driver.NodeConfig{
			Name:        l.name,
			Image:       "shard-listener:harness",
			IPv6:        l.ip,
			Env:         lenv,
			MetricsPort: 9200,
			Role:        driver.RoleListener,
		})
	}

	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle")
	waitGauge(t, ctx, e, auto, "multicast_manifest_pilots_known", func(v float64) bool { return v >= 2 }, 20*time.Second, "both pilots known")
	e.Sleep(4*time.Second, "adoption hysteresis + auto-join")

	// traffic sends one burst through the proxy and returns what each
	// listener delivered, and what the auto-joining one dropped as shard_filter.
	traffic := func(phase string) (autoD, ref01D, ref0D, autoFiltered float64) {
		var before [3]map[string]float64
		for i, l := range listeners {
			before[i] = e.Snapshot(ctx, l.name)
		}
		filtered := shardFilterDrops(t, ctx, e, auto)
		gen := subtxGenCmd("[" + publisher + "]:8725")
		gen = append(gen, "-duration", "3s")
		startGenerator(t, ctx, "s64-"+phase, gen)
		waitGenerator(t, ctx, "s64-"+phase)
		e.Sleep(2*time.Second, "pipeline drain")
		var got [3]float64
		for i, l := range listeners {
			got[i] = listenerDelivered(metrics.DeltaMap(before[i], metrics.ScrapeOrFail(t, e.MetricsURL(ctx, l.name))))
		}
		filtered = shardFilterDrops(t, ctx, e, auto) - filtered
		t.Logf("%s: delivered auto=%.0f ref01=%.0f ref0=%.0f, auto shard_filter drops=%.0f", phase, got[0], got[1], got[2], filtered)
		return got[0], got[1], got[2], filtered
	}

	autoD, ref01D, ref0D, filtered := traffic("pilot")
	metrics.AssertGT(t, "pilot shard 1 traffic (ref01 - ref0)", ref01D-ref0D)
	metrics.AssertEq(t, "with the pilot, auto delivers shards 0 and 1 (== ref01)", autoD, ref01D)
	metrics.AssertZero(t, "with the pilot, auto drops nothing as shard_filter", filtered)

	for _, a := range announcers {
		if err := e.Driver.Stop(ctx, a.name); err != nil {
			t.Fatalf("stop %s: %v", a.name, err)
		}
	}
	waitGauge(t, ctx, e, auto, "multicast_manifest_pilots_known", func(v float64) bool { return v == 0 }, 30*time.Second, "pilots expired")
	e.Sleep(3*time.Second, "auto-join leaves the pilot group")

	// Left on expiry, the group's traffic no longer arrives; still joined, it
	// would be dropped as shard_filter. Either way it is not delivered.
	autoD, _, ref0D, _ = traffic("expired")
	metrics.AssertEq(t, "after expiry, auto delivers shard 0 alone (== ref0)", autoD, ref0D)
}
