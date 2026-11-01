package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/lightwebinc/multicast-test/harness/metrics"
)

// Scenario 95: BRC-148 large-object fragmentation
//
// Objects exceeding the proxy's fragmentation MTU leave as BRC-130 fragments
// (OrigFrameVer 0x09, ContentID/TopicID in every fragment). Listeners
// reassemble before fan-out (filters evaluate on whole objects) and the
// ContentID doubles as the BRC-130 verification hash, checked on completion.
func TestScenario95_BeefFragmentation(t *testing.T) {
	ctx := context.Background()
	e, _, _, _ := basicTopology(t, "s95")
	e.PatchEnv("s95-proxy", map[string]string{
		"TCP_LISTEN_PORT": "9002",
		"FRAG_MTU":        "1280",
	})
	for _, l := range []string{"s95-listener1", "s95-listener2", "s95-listener3"} {
		e.PatchEnv(l, map[string]string{
			"BEEF_TOPICS":         "tm_s95",
			"VERIFY_PAYLOAD_HASH": "true", // arms the reassembly SHA-256d (= ContentID) check
		})
	}
	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle")
	e.Sleep(3*time.Second, "drain residual")

	objects := 10.0
	beforeL := snapshotListeners(t, e, ctx, "s95")
	startGenerator(t, ctx, "s95", []string{
		"beef-gen", "-addr", "[fd10::2]:9002", "-topics", "tm_s95",
		"-object-bytes", "4096", "-count", "10", "-interval", "80ms",
	})
	waitGenerator(t, ctx, "s95")
	e.Sleep(3*time.Second, "reassembly drain")
	afterL := scrapeListeners(t, e, ctx, "s95")

	for i, label := range []string{"listener1", "listener2", "listener3"} {
		delta := metrics.DeltaMap(beforeL[i], afterL[i])
		started := delta["bsl_reassembly_started_total"]
		completed := delta["bsl_reassembly_completed_total"]
		mismatch := delta["bsl_reassembly_hash_mismatch_total"]
		fwd := delta["bsl_frames_forwarded_total"]
		egrErr := delta["bsl_egress_errors_total"]
		t.Logf("%s: reasm started=%.0f completed=%.0f mismatch=%.0f forwarded=%.0f",
			label, started, completed, mismatch, fwd)

		metrics.AssertNear(t, label+" reassembly slots ≈ objects", started, objects, 0.10)
		metrics.AssertNear(t, label+" reassembly completed ≈ objects", completed, objects, 0.10)
		metrics.AssertZero(t, label+" ContentID verification failures", mismatch)
		metrics.AssertNear(t, label+" whole objects forwarded", fwd+egrErr, objects, 0.10)
	}
}
