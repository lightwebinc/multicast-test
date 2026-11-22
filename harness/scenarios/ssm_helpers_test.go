package scenarios

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lightwebinc/multicast-test/harness/build"
	"github.com/lightwebinc/multicast-test/harness/driver"
	"github.com/lightwebinc/multicast-test/harness/env"
	"github.com/lightwebinc/multicast-test/harness/metrics"
)

// manifestImage builds the shard-manifest announcer's image from the sibling
// checkout the first time a scenario needs it. Only the scenarios that run an
// announcer need the checkout, so TestMain does not build it.
func manifestImage(t *testing.T, ctx context.Context) string {
	t.Helper()
	spec := build.ImageSpec{
		RepoDir: filepath.Join(repoRoot, "shard-manifest"),
		MainPkg: ".",
		Binary:  "shard-manifest",
		Tag:     "shard-manifest:harness",
	}
	if err := build.Build(ctx, spec, false); err != nil {
		t.Fatalf("build %s: %v", spec.Tag, err)
	}
	return spec.Tag
}

// announcerNode is a BRC-139 shard-manifest announcer on the fabric at ipv6,
// announcing every second on UDP 9001 for the harness's shard layout; extra is
// merged into its environment.
func announcerNode(t *testing.T, ctx context.Context, name, ipv6 string, extra map[string]string) driver.NodeConfig {
	t.Helper()
	aenv := map[string]string{
		"IFACE":             "eth0",
		"PORT":              "9001",
		"SHARD_BITS":        "2",
		"MC_GROUP_ID":       "0x000B",
		"MANIFEST_SCOPE":    "site",
		"ANNOUNCE_INTERVAL": "1s",
		"INSTANCE_ID":       name,
		"LOG_FORMAT":        "text",
	}
	for k, v := range extra {
		aenv[k] = v
	}
	return driver.NodeConfig{
		Name:  name,
		Image: manifestImage(t, ctx),
		IPv6:  ipv6,
		Env:   aenv,
		Role:  driver.RoleAux,
	}
}

// workersReady returns a listener's /readyz worker counts: the receive
// workers that bound their sockets and entered their loops, and how many it
// runs.
func workersReady(t *testing.T, ctx context.Context, e *env.Env, name string) (ready, total float64) {
	t.Helper()
	url := strings.Replace(e.MetricsURL(ctx, name), "/metrics", "/readyz", 1)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("readyz %s: %v", name, err)
	}
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("readyz %s: %v", name, err)
	}
	defer resp.Body.Close() //nolint:errcheck
	var body struct {
		Ready int `json:"workers_ready"`
		Total int `json:"workers_total"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("readyz %s: decode: %v", name, err)
	}
	return float64(body.Ready), float64(body.Total)
}

// listenerDelivered returns the frames a listener delivered downstream: the
// harness egress is a blackhole, so forwarded plus egress errors.
func listenerDelivered(d map[string]float64) float64 {
	return d["bsl_frames_forwarded_total"] + d["bsl_egress_errors_total"]
}

// shardFilterDrops returns a listener's frames dropped by its shard filter.
func shardFilterDrops(t *testing.T, ctx context.Context, e *env.Env, name string) float64 {
	t.Helper()
	return metrics.LabelledValue(t, e.MetricsURL(ctx, name), "bsl_frames_dropped_total", "reason", "shard_filter")
}

// waitGauge polls a node's gauge until pred holds, failing after timeout.
func waitGauge(t *testing.T, ctx context.Context, e *env.Env, name, metric string, pred func(float64) bool, timeout time.Duration, what string) float64 {
	t.Helper()
	v, ok := metrics.WaitFor(e.MetricsURL(ctx, name), metric, pred, timeout, 250*time.Millisecond)
	if !ok {
		t.Fatalf("%s: %s not reached within %s (%s = %.0f)", name, what, timeout, metric, v)
	}
	t.Logf("%s: %s (%s = %.0f)", name, what, metric, v)
	return v
}
