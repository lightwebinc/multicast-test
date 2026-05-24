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

// proxyEnv returns a baseline Env map for bitcoin-shard-proxy.
func proxyEnv() map[string]string {
	return map[string]string{
		"MULTICAST_IF":    "eth0",
		"UDP_LISTEN_PORT": "9000",
		"EGRESS_PORT":     "9001",
		"SHARD_BITS":      "2",
		"MC_SCOPE":        "site",
		"MC_GROUP_ID":     "0x000B",
		"METRICS_ADDR":    ":9100",
	}
}

// listenerEnv returns a baseline Env map for bitcoin-shard-listener.
func listenerEnv() map[string]string {
	return map[string]string{
		"MULTICAST_IF": "eth0",
		"LISTEN_PORT":  "9001",
		"SHARD_BITS":   "2",
		"MC_SCOPE":     "site",
		"MC_GROUP_ID":  "0x000B",
		"NUM_WORKERS":  "1",
		"EGRESS_ADDR":  "127.0.0.1:9100",
		"METRICS_ADDR": ":9200",
	}
}

// retryEnv returns a baseline Env map for bitcoin-retry-endpoint.
func retryEnv() map[string]string {
	return map[string]string{
		"MULTICAST_IF": "eth0",
		"LISTEN_PORT":  "9001",
		"NACK_PORT":    "9300",
		"EGRESS_PORT":  "9001",
		"SHARD_BITS":   "2",
		"MC_SCOPE":     "site",
		"MC_GROUP_ID":  "0x000B",
		"METRICS_ADDR": ":9400",
	}
}

// subtxGenCmd returns the default subtx-gen CLI flags.
func subtxGenCmd(proxyAddr string) []string {
	return []string{
		"-addr", proxyAddr,
		"-shard-bits", "2",
		"-subtrees", "8",
		"-subtree-seed", "multicast-lab-bsv",
		"-pps", "1000",
		"-duration", "10s",
		"-payload-size", "256",
		"-log-interval", "2s",
	}
}

// basicTopology creates the standard 5-node topology used by most scenarios:
//
//	proxy (fd10::2), listener1 (fd10::11), listener2 (fd10::12),
//	listener3 (fd10::13), and returns the env.Env ready for StartAll.
//
// The prefix is used for container names (e.g. "s02" → "s02-proxy").
// Listeners are configured with the standard shard/subtree filters from the
// lab's listener-hosts.yml unless overrides are applied after calling this.
func basicTopology(t *testing.T, prefix string) (*env.Env, map[string]string, map[string]string, map[string]string) {
	t.Helper()
	e := env.New(t, dockerdriver.New())

	e.AddNode(driver.NodeConfig{
		Name:        prefix + "-proxy",
		Image:       "bitcoin-shard-proxy:harness",
		IPv6:        "fd10::2",
		Env:         proxyEnv(),
		MetricsPort: 9100,
		Role:        driver.RoleProxy,
	})

	l1env := listenerEnv()
	e.AddNode(driver.NodeConfig{
		Name:        prefix + "-listener1",
		Image:       "bitcoin-shard-listener:harness",
		IPv6:        "fd10::11",
		Env:         l1env,
		MetricsPort: 9200,
		Role:        driver.RoleListener,
	})

	l2env := listenerEnv()
	l2env["SHARD_INCLUDE"] = "0,1"
	l2env["SUBTREE_EXCLUDE"] = subtreeExcludeL2
	e.AddNode(driver.NodeConfig{
		Name:        prefix + "-listener2",
		Image:       "bitcoin-shard-listener:harness",
		IPv6:        "fd10::12",
		Env:         l2env,
		MetricsPort: 9200,
		Role:        driver.RoleListener,
	})

	l3env := listenerEnv()
	l3env["SUBTREE_INCLUDE"] = subtreeIncludeL3
	e.AddNode(driver.NodeConfig{
		Name:        prefix + "-listener3",
		Image:       "bitcoin-shard-listener:harness",
		IPv6:        "fd10::13",
		Env:         l3env,
		MetricsPort: 9200,
		Role:        driver.RoleListener,
	})

	return e, l1env, l2env, l3env
}

// specialBinaries lists the harness images that have their own ENTRYPOINT and
// are invoked by passing the binary name as cmd[0] from the test.
var specialBinaries = map[string]string{
	"send-block-announce": "send-block-announce:harness",
	"send-subtree-data":   "send-subtree-data:harness",
	"send-anchor-frame":   "send-anchor-frame:harness",
}

// startGenerator starts the source container and registers cleanup.
// If cmd[0] is the name of a dedicated harness binary (e.g. "send-block-announce"),
// that binary's image is used and cmd[0] is stripped so it is not passed as an
// argument to the ENTRYPOINT. Otherwise bitcoin-subtx-generator:harness is used.
// Returns after the container starts; caller should WaitForExit to wait for it.
func startGenerator(t *testing.T, ctx context.Context, prefix string, cmd []string) {
	t.Helper()
	drv := dockerdriver.New()
	name := prefix + "-source"
	image := "bitcoin-subtx-generator:harness"
	entryCmd := cmd
	if len(cmd) > 0 {
		if img, ok := specialBinaries[cmd[0]]; ok {
			image = img
			entryCmd = cmd[1:]
		}
	}
	if err := drv.Start(ctx, driver.NodeConfig{
		Name:  name,
		Image: image,
		IPv6:  "fd10::3",
		Cmd:   entryCmd,
		Role:  driver.RoleGenerator,
	}); err != nil {
		t.Fatalf("start source: %v", err)
	}
	t.Cleanup(func() {
		drv.Stop(context.Background(), name) //nolint:errcheck
	})
}

// waitGenerator waits for the generator container to exit and logs exit code.
func waitGenerator(t *testing.T, ctx context.Context, prefix string) {
	t.Helper()
	exitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	code, err := dockerdriver.New().WaitExit(exitCtx, prefix+"-source")
	if err != nil {
		t.Logf("source wait: %v", err)
	}
	t.Logf("source exited with code %d", code)
}

// runTrafficAndSnapshot runs the standard traffic generation flow:
// start all → MLD settle → snapshot before → start generator → wait exit → drain → snapshot after.
// Returns the before/after snapshots for each listener.
func runTrafficAndSnapshot(t *testing.T, e *env.Env, prefix string, genCmd []string) (
	beforeL1, afterL1, beforeL2, afterL2, beforeL3, afterL3 map[string]float64,
) {
	t.Helper()
	ctx := context.Background()

	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle + multicast group joins")

	beforeL1 = e.Snapshot(ctx, prefix+"-listener1")
	beforeL2 = e.Snapshot(ctx, prefix+"-listener2")
	beforeL3 = e.Snapshot(ctx, prefix+"-listener3")

	startGenerator(t, ctx, prefix, genCmd)
	waitGenerator(t, ctx, prefix)

	e.Sleep(2*time.Second, "egress pipeline drain")

	urlL1 := e.MetricsURL(ctx, prefix+"-listener1")
	urlL2 := e.MetricsURL(ctx, prefix+"-listener2")
	urlL3 := e.MetricsURL(ctx, prefix+"-listener3")

	afterL1 = scrapeOrFail(t, urlL1)
	afterL2 = scrapeOrFail(t, urlL2)
	afterL3 = scrapeOrFail(t, urlL3)

	e.LogContainerOutput(ctx, prefix+"-source")
	return
}

// retryTopology creates a topology with proxy, 3 listeners, 1 retry endpoint,
// and returns the env ready for StartAll. The retry endpoint joins multicast
// groups and listens for NACKs on port 9300.
func retryTopology(t *testing.T, prefix string) (*env.Env, map[string]string) {
	t.Helper()
	e := env.New(t, dockerdriver.New())

	e.AddNode(driver.NodeConfig{
		Name:        prefix + "-proxy",
		Image:       "bitcoin-shard-proxy:harness",
		IPv6:        "fd10::2",
		Env:         proxyEnv(),
		MetricsPort: 9100,
		Role:        driver.RoleProxy,
	})

	for i, suffix := range []string{"1", "2", "3"} {
		lenv := listenerEnv()
		lenv["RETRY_ENDPOINTS"] = "[fd10::20]:9300"
		switch suffix {
		case "2":
			lenv["SHARD_INCLUDE"] = "0,1"
			lenv["SUBTREE_EXCLUDE"] = subtreeExcludeL2
		case "3":
			lenv["SUBTREE_INCLUDE"] = subtreeIncludeL3
		}
		e.AddNode(driver.NodeConfig{
			Name:        prefix + "-listener" + suffix,
			Image:       "bitcoin-shard-listener:harness",
			IPv6:        fmt.Sprintf("fd10::1%d", i+1),
			Env:         lenv,
			MetricsPort: 9200,
			Role:        driver.RoleListener,
		})
	}

	renv := retryEnv()
	e.AddNode(driver.NodeConfig{
		Name:        prefix + "-retry1",
		Image:       "bitcoin-retry-endpoint:harness",
		IPv6:        "fd10::20",
		Env:         renv,
		MetricsPort: 9400,
		Role:        driver.RoleRetry,
	})

	return e, renv
}

// sumListenerDelta aggregates a metric across all 3 listeners.
func sumListenerDelta(prefix, metric string, before, after [3]map[string]float64) float64 {
	var sum float64
	for i := 0; i < 3; i++ {
		delta := metrics.DeltaMap(before[i], after[i])
		sum += delta[metric]
	}
	return sum
}

// snapshotListeners takes before snapshots of all 3 listeners.
func snapshotListeners(t *testing.T, e *env.Env, ctx context.Context, prefix string) [3]map[string]float64 {
	t.Helper()
	var snaps [3]map[string]float64
	for i, suffix := range []string{"1", "2", "3"} {
		snaps[i] = e.Snapshot(ctx, prefix+"-listener"+suffix)
		_ = suffix
	}
	return snaps
}

// scrapeListeners scrapes after-metrics for all 3 listeners.
func scrapeListeners(t *testing.T, e *env.Env, ctx context.Context, prefix string) [3]map[string]float64 {
	t.Helper()
	var snaps [3]map[string]float64
	for i, suffix := range []string{"1", "2", "3"} {
		url := e.MetricsURL(ctx, prefix+"-listener"+suffix)
		snaps[i] = metrics.ScrapeOrFail(t, url)
		_ = suffix
	}
	return snaps
}

// multiRetryTopology creates a topology with proxy, 3 listeners, 3 retry
// endpoints (retry1=T0/P128, retry2=T0/P64, retry3=T1/P128), for testing
// MISS escalation and multi-endpoint rate limiting. Listeners use beacon
// discovery to learn endpoint ordering.
func multiRetryTopology(t *testing.T, prefix string) *env.Env {
	t.Helper()
	e := env.New(t, dockerdriver.New())

	e.AddNode(driver.NodeConfig{
		Name:        prefix + "-proxy",
		Image:       "bitcoin-shard-proxy:harness",
		IPv6:        "fd10::2",
		Env:         proxyEnv(),
		MetricsPort: 9100,
		Role:        driver.RoleProxy,
	})

	retryList := "[fd10::20]:9300,[fd10::21]:9300,[fd10::22]:9300"
	for i, suffix := range []string{"1", "2", "3"} {
		lenv := listenerEnv()
		lenv["RETRY_ENDPOINTS"] = retryList
		switch suffix {
		case "2":
			lenv["SHARD_INCLUDE"] = "0,1"
			lenv["SUBTREE_EXCLUDE"] = subtreeExcludeL2
		case "3":
			lenv["SUBTREE_INCLUDE"] = subtreeIncludeL3
		}
		e.AddNode(driver.NodeConfig{
			Name:        prefix + "-listener" + suffix,
			Image:       "bitcoin-shard-listener:harness",
			IPv6:        fmt.Sprintf("fd10::1%d", i+1),
			Env:         lenv,
			MetricsPort: 9200,
			Role:        driver.RoleListener,
		})
	}

	// retry1: Tier 0, Preference 128 (highest within tier 0).
	r1env := retryEnv()
	r1env["BEACON_TIER"] = "0"
	r1env["BEACON_PREFERENCE"] = "128"
	e.AddNode(driver.NodeConfig{
		Name:        prefix + "-retry1",
		Image:       "bitcoin-retry-endpoint:harness",
		IPv6:        "fd10::20",
		Env:         r1env,
		MetricsPort: 9400,
		Role:        driver.RoleRetry,
	})

	// retry2: Tier 0, Preference 64.
	r2env := retryEnv()
	r2env["BEACON_TIER"] = "0"
	r2env["BEACON_PREFERENCE"] = "64"
	r2env["NACK_PORT"] = "9300"
	r2env["METRICS_ADDR"] = ":9401"
	e.AddNode(driver.NodeConfig{
		Name:        prefix + "-retry2",
		Image:       "bitcoin-retry-endpoint:harness",
		IPv6:        "fd10::21",
		Env:         r2env,
		MetricsPort: 9401,
		Role:        driver.RoleRetry,
	})

	// retry3: Tier 1, Preference 128 (fallback tier).
	r3env := retryEnv()
	r3env["BEACON_TIER"] = "1"
	r3env["BEACON_PREFERENCE"] = "128"
	r3env["NACK_PORT"] = "9300"
	r3env["METRICS_ADDR"] = ":9402"
	e.AddNode(driver.NodeConfig{
		Name:        prefix + "-retry3",
		Image:       "bitcoin-retry-endpoint:harness",
		IPv6:        "fd10::22",
		Env:         r3env,
		MetricsPort: 9402,
		Role:        driver.RoleRetry,
	})

	return e
}

// snapshotRetries takes before snapshots of all 3 retry endpoints.
func snapshotRetries(t *testing.T, e *env.Env, ctx context.Context, prefix string) [3]map[string]float64 {
	t.Helper()
	var snaps [3]map[string]float64
	names := []string{prefix + "-retry1", prefix + "-retry2", prefix + "-retry3"}
	for i, name := range names {
		snaps[i] = e.Snapshot(ctx, name)
	}
	return snaps
}

// scrapeRetries scrapes after-metrics for all 3 retry endpoints.
func scrapeRetries(t *testing.T, e *env.Env, ctx context.Context, prefix string) [3]map[string]float64 {
	t.Helper()
	var snaps [3]map[string]float64
	names := []string{prefix + "-retry1", prefix + "-retry2", prefix + "-retry3"}
	for i, name := range names {
		url := e.MetricsURL(ctx, name)
		snaps[i] = metrics.ScrapeOrFail(t, url)
	}
	return snaps
}

// retryDelta returns delta for a single retry index (0..2).
func retryDelta(idx int, metric string, before, after [3]map[string]float64) float64 {
	delta := metrics.DeltaMap(before[idx], after[idx])
	return delta[metric]
}

// scrapeOrFail is a local alias used in topology helpers.
func scrapeOrFail(t *testing.T, url string) map[string]float64 {
	t.Helper()
	return metrics.ScrapeOrFail(t, url)
}
