package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/lightwebinc/multicast-test/harness/metrics"
)

// Scenario 94: BRC-148 version filter + verbatim carriage
//
// The version (encoding-capability) filter: a listener electing only the
// BRC-62 v1 encoding forwards BEEF-v1 objects and drops BEEF-V2 and Atomic
// BEEF ones. Then the REAL BRC-62 specification example is submitted with
// listener-side ContentID verification enabled: byte-identical delivery of
// a genuine proof-carrying object (verbatim carriage, no re-encoding).
func TestScenario94_BeefVersionFilterVerbatim(t *testing.T) {
	ctx := context.Background()
	e, _, _, _ := basicTopology(t, "s94")
	e.PatchEnv("s94-proxy", map[string]string{"TCP_LISTEN_PORT": "9002"})
	for _, l := range []string{"s94-listener1", "s94-listener2", "s94-listener3"} {
		e.PatchEnv(l, map[string]string{
			"BEEF_TOPICS":         "tm_s94",
			"BEEF_VERSIONS":       "beef", // v1 capability only
			"BEEF_VERIFY_CONTENT": "true",
		})
	}
	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle")
	e.Sleep(3*time.Second, "drain residual")

	beforeL := snapshotListeners(t, e, ctx, "s94")
	for _, run := range []struct{ name, enc, count, seed string }{
		{"s94v1", "beef", "10", "1"},
		{"s94v2", "beefv2", "10", "2"},
		{"s94at", "atomic", "10", "3"},
		{"s94real", "real", "1", "4"},
	} {
		startGenerator(t, ctx, run.name, []string{
			"beef-gen", "-addr", "[fd10::2]:9002", "-topics", "tm_s94",
			"-encoding", run.enc, "-count", run.count, "-interval", "40ms", "-seed", run.seed,
		})
		waitGenerator(t, ctx, run.name)
	}
	e.Sleep(3*time.Second, "pipeline drain")
	afterL := scrapeListeners(t, e, ctx, "s94")

	for i, label := range []string{"listener1", "listener2", "listener3"} {
		delta := metrics.DeltaMap(beforeL[i], afterL[i])
		recv := delta["bsl_frames_received_total"]
		fwd := delta["bsl_frames_forwarded_total"]
		egrErr := delta["bsl_egress_errors_total"]
		dropped := delta["bsl_frames_dropped_total"]
		invalid := delta["bsl_frames_invalid_payload_total"]
		t.Logf("%s: received=%.0f forwarded=%.0f dropped=%.0f invalid=%.0f", label, recv, fwd, dropped, invalid)

		// 31 frames total; v1 synthetic (10) + the real spec vector (1) pass
		// the capability gate, V2 + Atomic (20) drop.
		metrics.AssertNear(t, label+" received all encodings", recv, 31, 0.10)
		metrics.AssertNear(t, label+" v1-capable forwards", fwd+egrErr, 11, 0.10)
		metrics.AssertNear(t, label+" version-filter drops", dropped, 20, 0.10)
		// ContentID verification was ON for every forwarded object; zero
		// mismatches proves byte-identical carriage (incl. the real vector).
		metrics.AssertZero(t, label+" content verification mismatches", invalid)
	}
}
