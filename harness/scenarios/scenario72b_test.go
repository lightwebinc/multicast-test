package scenarios

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/lightwebinc/multicast-test/harness/driver"
	dockerdriver "github.com/lightwebinc/multicast-test/harness/driver/docker"
	"github.com/lightwebinc/multicast-test/harness/env"
	"github.com/lightwebinc/multicast-test/harness/metrics"
)

// Scenario 72b: the manifest consumer observes against its running config
//
// Scenario 72's divergence path in a running listener. The manifest consumer
// compares what authoritative announcers say with the configuration the
// process runs (SHARD_BITS here), never with a zero value, and only observes:
//
//   - two announcers that agree with SHARD_BITS: no adoption series
//     (multicast_manifest_adoption_total) and no warning;
//   - both at another ShardBits: exactly one divergence warning for the field,
//     and multicast_manifest_divergence_total keeps rising while it lasts;
//   - back in agreement: exactly one "manifest divergence cleared" line.
//
// The listener stays up throughout.
func TestScenario72b_ManifestRunningBaseline(t *testing.T) {
	const listener = "s72b-listener1"
	ctx := context.Background()
	e := env.New(t, dockerdriver.New())

	lenv := listenerEnv()
	lenv["MANIFEST_CONSUMER_ENABLED"] = "true"
	e.AddNode(driver.NodeConfig{
		Name:        listener,
		Image:       "shard-listener:harness",
		IPv6:        "fd10::11",
		Env:         lenv,
		MetricsPort: 9200,
		Role:        driver.RoleListener,
	})
	e.StartAll(ctx)

	// announce runs both announcers at shardBits, replacing any running
	// ones: the same addresses and instance IDs, so their registry entries
	// are overwritten rather than left to expire.
	announcers := []struct{ name, ip string }{{"s72b-announcer1", "fd10::60"}, {"s72b-announcer2", "fd10::61"}}
	announce := func(shardBits string) {
		for _, a := range announcers {
			_ = e.Driver.Stop(ctx, a.name)
			cfg := announcerNode(t, ctx, a.name, a.ip, map[string]string{
				"AUTHORITATIVE": "true",
				"SHARD_BITS":    shardBits,
				"TTL":           "3s",
			})
			if err := e.Driver.Start(ctx, cfg); err != nil {
				t.Fatalf("start %s: %v", a.name, err)
			}
		}
	}
	t.Cleanup(func() {
		for _, a := range announcers {
			_ = e.Driver.Stop(context.Background(), a.name)
		}
	})
	url := e.MetricsURL(ctx, listener)

	announce("2")
	waitGauge(t, ctx, e, listener, "multicast_manifest_pilots_known", func(v float64) bool { return v >= 2 }, 20*time.Second, "both announcers known")
	e.Sleep(4*time.Second, "agreeing announcers")
	m := metrics.ScrapeOrFail(t, url)
	_, adoption := m["multicast_manifest_adoption_total"]
	logs := containerLogs(t, ctx, listener)
	t.Logf("agree: adoption series=%v, manifest warnings=%d", adoption, manifestWarnings(logs))
	for _, w := range manifestWarningLines(logs) {
		t.Logf("agree: warning: %s", w)
	}
	metrics.AssertZero(t, "agree: multicast_manifest_adoption_total series", boolf(adoption))
	metrics.AssertZero(t, "agree: manifest warnings", float64(manifestWarnings(logs)))
	metrics.AssertZero(t, "agree: multicast_manifest_divergence_total", m["multicast_manifest_divergence_total"])

	announce("3")
	e.Sleep(5*time.Second, "disagreeing announcers")
	d1 := metrics.ScrapeOrFail(t, url)["multicast_manifest_divergence_total"]
	e.Sleep(3*time.Second, "divergence persists")
	d2 := metrics.ScrapeOrFail(t, url)["multicast_manifest_divergence_total"]
	logs = containerLogs(t, ctx, listener)
	t.Logf("disagree: divergence_total %.0f then %.0f; divergence warnings shard_bits=%d, all=%d",
		d1, d2, countLines(logs, "manifest divergence:", "field=shard_bits"), countLines(logs, "manifest divergence:"))
	metrics.AssertGT(t, "disagree: multicast_manifest_divergence_total", d1)
	metrics.AssertGT(t, "disagree: multicast_manifest_divergence_total keeps rising", d2-d1)
	metrics.AssertEq(t, "disagree: divergence warnings for shard_bits", float64(countLines(logs, "manifest divergence:", "field=shard_bits")), 1)
	metrics.AssertEq(t, "disagree: divergence warnings in all", float64(countLines(logs, "manifest divergence:")), 1)

	announce("2")
	e.Sleep(5*time.Second, "announcers agree again")
	logs = containerLogs(t, ctx, listener)
	t.Logf("agree again: cleared lines shard_bits=%d", countLines(logs, "manifest divergence cleared", "field=shard_bits"))
	metrics.AssertEq(t, "agree again: \"manifest divergence cleared\" for shard_bits", float64(countLines(logs, "manifest divergence cleared", "field=shard_bits")), 1)
	metrics.AssertEq(t, "agree again: divergence warnings still one", float64(countLines(logs, "manifest divergence:")), 1)

	// Still serving: an exited listener has no /readyz.
	if err := e.WaitReady(ctx, listener, 2*time.Second); err != nil {
		t.Errorf("FAIL  listener no longer ready: %v", err)
	}
}

// containerLogs returns a container's whole log output.
func containerLogs(t *testing.T, ctx context.Context, name string) string {
	t.Helper()
	out, err := exec.CommandContext(ctx, "docker", "logs", name).CombinedOutput()
	if err != nil {
		t.Fatalf("docker logs %s: %v", name, err)
	}
	return string(out)
}

// countLines counts the log lines that contain every one of subs.
func countLines(logs string, subs ...string) int {
	n := 0
	for _, line := range strings.Split(logs, "\n") {
		all := true
		for _, s := range subs {
			if !strings.Contains(line, s) {
				all = false
				break
			}
		}
		if all {
			n++
		}
	}
	return n
}

// manifestWarningLines returns the warnings the manifest consumer logged: the
// applier's own, and the adoption notices the process logs for it.
func manifestWarningLines(logs string) []string {
	var out []string
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, "level=WARN") &&
			(strings.Contains(line, "manifest") || strings.Contains(line, "auto-config")) {
			out = append(out, line)
		}
	}
	return out
}

// manifestWarnings counts manifestWarningLines.
func manifestWarnings(logs string) int { return len(manifestWarningLines(logs)) }

func boolf(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
