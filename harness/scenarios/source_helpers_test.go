package scenarios

import (
	"context"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lightwebinc/multicast-test/harness/driver"
	dockerdriver "github.com/lightwebinc/multicast-test/harness/driver/docker"
	"github.com/lightwebinc/multicast-test/harness/env"
	"github.com/lightwebinc/multicast-test/harness/metrics"
)

// startSourceAt starts a traffic source with its own container name and
// fabric address, for scenarios that run more than one at a time
// (startGenerator always uses fd10::3). cmd[0] may name a dedicated harness
// binary, as with startGenerator. The container is removed at test end.
func startSourceAt(t *testing.T, ctx context.Context, name, ipv6 string, cmd []string) {
	t.Helper()
	image := "subtx-generator:harness"
	entryCmd := cmd
	if len(cmd) > 0 {
		if img, ok := specialBinaries[cmd[0]]; ok {
			image = img
			entryCmd = cmd[1:]
		}
	}
	drv := dockerdriver.New()
	if err := drv.Start(ctx, driver.NodeConfig{
		Name:  name,
		Image: image,
		IPv6:  ipv6,
		Cmd:   entryCmd,
		Role:  driver.RoleGenerator,
	}); err != nil {
		t.Fatalf("start %s: %v", name, err)
	}
	t.Cleanup(func() {
		drv.Stop(context.Background(), name) //nolint:errcheck
	})
}

// waitSource waits up to 2 minutes for the named source container to exit.
func waitSource(t *testing.T, ctx context.Context, name string) {
	t.Helper()
	exitCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	code, err := dockerdriver.New().WaitExit(exitCtx, name)
	if err != nil {
		t.Fatalf("wait %s: %v", name, err)
	}
	t.Logf("%s exited with code %d", name, code)
}

var sentLine = regexp.MustCompile(`done: sent=(\d+)`)

// sourceSent returns the frames a subtx-gen container reports on its closing
// "done: sent=N" line. The container must have exited.
func sourceSent(t *testing.T, ctx context.Context, name string) float64 {
	t.Helper()
	out, err := exec.CommandContext(ctx, "docker", "logs", name).CombinedOutput()
	if err != nil {
		t.Fatalf("docker logs %s: %v", name, err)
	}
	m := sentLine.FindSubmatch(out)
	if m == nil {
		t.Fatalf("%s: no \"done: sent=N\" line in its output", name)
	}
	n, _ := strconv.ParseFloat(string(m[1]), 64)
	return n
}

// redisNodeAt is a Redis container on the given fabric address, bound to IPv6
// with protected mode off (the fabric is its only network).
func redisNodeAt(name, ipv6 string) driver.NodeConfig {
	return driver.NodeConfig{
		Name:  name,
		Image: "redis:7-alpine",
		IPv6:  ipv6,
		Cmd:   []string{"redis-server", "--bind", "::", "--protected-mode", "no"},
		Role:  driver.RoleAux,
	}
}

// redisKeys counts the keys matching pattern in the named Redis container.
func redisKeys(t *testing.T, ctx context.Context, e *env.Env, name, pattern string) float64 {
	t.Helper()
	out, err := e.Driver.Exec(ctx, name, "redis-cli", "-h", "::1", "--raw", "EVAL",
		"return #redis.call('KEYS', ARGV[1])", "0", pattern)
	if err != nil {
		t.Fatalf("count %s keys in %s: %v\n%s", pattern, name, err, out)
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(out), 64)
	if err != nil {
		t.Fatalf("count %s keys in %s: parse %q: %v", pattern, name, out, err)
	}
	return n
}

// dedupErrors returns retry-endpoint's bre_dedup_errors_total for one claim
// ("retransmit" or "proxy"): shared-backend claims that failed open.
func dedupErrors(t *testing.T, ctx context.Context, e *env.Env, name, claim string) float64 {
	t.Helper()
	return metrics.LabelledValue(t, e.MetricsURL(ctx, name), "bre_dedup_errors_total", "claim", claim)
}

// dedupSkipped returns retry-endpoint's bre_dedup_skipped_total for one claim:
// claims not sent to the shared backend because a recent one failed and the
// claim breaker is open. Each one fails open at once.
func dedupSkipped(t *testing.T, ctx context.Context, e *env.Env, name, claim string) float64 {
	t.Helper()
	return metrics.LabelledValue(t, e.MetricsURL(ctx, name), "bre_dedup_skipped_total", "claim", claim)
}

// breakerWindow is retry-endpoint's -dedup-breaker-window default: once a
// claim fails, the endpoint skips claims for this long, then lets one probe
// through.
const breakerWindow = 5 * time.Second

// breakerProbeLimit is the exclusive upper bound on the claim errors an
// endpoint behind the claim breaker books in d, counted from once the breaker
// is open and the claims in flight when it opened have failed (how many those
// are depends on the endpoint's workers): one probe per window, and one more
// for the edges of d.
func breakerProbeLimit(d time.Duration) float64 {
	return float64(d/breakerWindow) + 2
}
