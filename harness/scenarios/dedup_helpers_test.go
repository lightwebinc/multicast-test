package scenarios

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lightwebinc/multicast-test/harness/driver"
	"github.com/lightwebinc/multicast-test/harness/env"
	"github.com/lightwebinc/multicast-test/harness/metrics"
)

// dedupRedisAddr is the TxID dedup scenarios' Redis on the fabric, in the
// bracketed host:port form the listener dials.
const dedupRedisAddr = "[fd10::30]:6379"

// dedupRedisNode is the Redis container of the TxID dedup scenarios. It binds
// IPv6 with protected mode off: the fabric is its only network.
func dedupRedisNode(name string) driver.NodeConfig {
	return driver.NodeConfig{
		Name:  name,
		Image: "redis:7-alpine",
		IPv6:  "fd10::30",
		Cmd:   []string{"redis-server", "--bind", "::", "--protected-mode", "no"},
		Role:  driver.RoleAux,
	}
}

// dedupListenerEnv returns listenerEnv with the per-deployment egress TxID
// dedup on the scenario Redis. A set EGRESS_DEDUP_REDIS_ADDR selects the redis
// backend; the listener has no separate enable switch. Listeners given the
// same non-empty deploymentID are HA siblings racing for one claim per TxID;
// an empty one leaves the default (the hostname), a key space of its own.
func dedupListenerEnv(deploymentID string) map[string]string {
	lenv := listenerEnv()
	lenv["EGRESS_DEDUP_REDIS_ADDR"] = dedupRedisAddr
	if deploymentID != "" {
		lenv["DEPLOYMENT_ID"] = deploymentID
	}
	return lenv
}

// redisClaimKeys counts the egress claim keys (bsl:egr:*) in the named Redis
// container: one per TxID a listener claimed through the redis backend. The
// server binds IPv6 only, so redis-cli dials ::1, not its 127.0.0.1 default.
func redisClaimKeys(t *testing.T, ctx context.Context, e *env.Env, name string) float64 {
	t.Helper()
	out, err := e.Driver.Exec(ctx, name, "redis-cli", "-h", "::1", "--raw", "EVAL",
		"return #redis.call('KEYS', ARGV[1])", "0", "bsl:egr:*")
	if err != nil {
		t.Fatalf("count claim keys in %s: %v\n%s", name, err, out)
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(out), 64)
	if err != nil {
		t.Fatalf("count claim keys in %s: parse %q: %v", name, out, err)
	}
	return n
}

// settleDedupListeners scrapes the named listeners until their frame and claim
// counters hold still across two scrapes a second apart, then returns that
// scrape. A listener whose claims wait on an unreachable backend drains its
// receive backlog after the generator stops, so a fixed sleep can scrape it
// between the claim-error count and the forward that follows it.
func settleDedupListeners(t *testing.T, ctx context.Context, e *env.Env, names []string, timeout time.Duration) []map[string]float64 {
	t.Helper()
	moving := []string{
		"bsl_frames_received_total",
		"bsl_frames_forwarded_total",
		"bsl_egress_errors_total",
		"bsl_frames_tx_deduped_total",
		"bsl_txid_dedup_errors_total",
	}
	deadline := time.Now().Add(timeout)
	var prev []map[string]float64
	for {
		cur := make([]map[string]float64, len(names))
		for i, n := range names {
			cur[i] = metrics.ScrapeOrFail(t, e.MetricsURL(ctx, n))
		}
		if prev != nil && countersEqual(prev, cur, moving) {
			return cur
		}
		if time.Now().After(deadline) {
			t.Fatalf("listener counters still moving after %s", timeout)
		}
		prev = cur
		time.Sleep(time.Second)
	}
}

func countersEqual(a, b []map[string]float64, keys []string) bool {
	for i := range a {
		for _, k := range keys {
			if a[i][k] != b[i][k] {
				return false
			}
		}
	}
	return true
}
