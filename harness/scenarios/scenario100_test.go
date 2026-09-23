package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/lightwebinc/multicast-test/harness/metrics"
)

// Scenario 100 — BRC-148 BEEF plane: a record naming several topics on the
// OPEN path.
//
// One record naming three topics is ONE frame on the fabric: the first topic
// is the shard key and the only deliverable one, every name rides in the
// payload, and nothing is rejected for topic count. Three listeners prove
// the delivery rule from the edge: one elects the first topic (delivers),
// one elects a later name only (a label; the frame is received and dropped
// by the topic filter, never forwarded), and one elects the first AND a
// later name (delivers exactly once, not once per matched name). Content
// verification stays on, so the record-carrying payload is proven to reach
// the edge byte-identical.
func TestScenario100_BeefMultiTopicOpenPath(t *testing.T) {
	ctx := context.Background()
	e, _, _, _ := basicTopology(t, "s100")
	e.PatchEnv("s100-proxy", map[string]string{"TCP_LISTEN_PORT": "9002"})
	e.PatchEnv("s100-listener1", map[string]string{"BEEF_TOPICS": "tm_s100a", "BEEF_VERIFY_CONTENT": "true"})
	e.PatchEnv("s100-listener2", map[string]string{"BEEF_TOPICS": "tm_s100c", "BEEF_VERIFY_CONTENT": "true"})
	e.PatchEnv("s100-listener3", map[string]string{"BEEF_TOPICS": "tm_s100a,tm_s100b", "BEEF_VERIFY_CONTENT": "true"})
	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle")
	e.Sleep(3*time.Second, "drain residual")

	beforeP := e.Snapshot(ctx, "s100-proxy")
	beforeL := snapshotListeners(t, e, ctx, "s100")
	startGenerator(t, ctx, "s100", []string{
		"beef-gen", "-addr", "[fd10::2]:9002", "-topics", "tm_s100a,tm_s100b,tm_s100c",
		"-count", "30", "-interval", "50ms",
	})
	waitGenerator(t, ctx, "s100")
	e.Sleep(3*time.Second, "pipeline drain")

	afterP := metrics.ScrapeOrFail(t, e.MetricsURL(ctx, "s100-proxy"))
	dp := metrics.DeltaMap(beforeP, afterP)
	t.Logf("proxy: beef_submissions=%.0f fwd=%.0f dropped=%.0f",
		dp["bsp_beef_submissions_total"], dp["bsp_packets_forwarded_total"], dp["bsp_packets_dropped_total"])
	// Every record admitted, and each one is exactly ONE frame: no fan-out.
	metrics.AssertNear(t, "proxy admitted every multi-topic record", dp["bsp_beef_submissions_total"], 30, 0.10)
	metrics.AssertNear(t, "one frame per record, not one per topic", dp["bsp_packets_forwarded_total"], 30, 0.10)

	afterL := scrapeListeners(t, e, ctx, "s100")
	type want struct {
		label   string
		forward float64
		dropped float64
	}
	for i, w := range []want{
		{"listener1 (first topic)", 30, 0},
		{"listener2 (label only)", 0, 30},
		{"listener3 (first + label)", 30, 0},
	} {
		delta := metrics.DeltaMap(beforeL[i], afterL[i])
		recv := delta["bsl_frames_received_total"]
		fwd := delta["bsl_frames_forwarded_total"]
		egrErr := delta["bsl_egress_errors_total"]
		dropped := delta["bsl_frames_dropped_total"]
		invalid := delta["bsl_frames_invalid_payload_total"]
		t.Logf("%s: received=%.0f forwarded=%.0f dropped=%.0f invalid=%.0f", w.label, recv, fwd, dropped, invalid)
		metrics.AssertNear(t, w.label+" receives every frame on the band", recv, 30, 0.10)
		if w.forward == 0 {
			metrics.AssertZero(t, w.label+" forwards nothing (a label is never delivered)", fwd+egrErr)
		} else {
			metrics.AssertNear(t, w.label+" forwards once per object", fwd+egrErr, w.forward, 0.10)
		}
		if w.dropped == 0 {
			metrics.AssertZero(t, w.label+" topic-filter drops", dropped)
		} else {
			metrics.AssertNear(t, w.label+" topic-filter drops", dropped, w.dropped, 0.10)
		}
		metrics.AssertZero(t, w.label+" content verification mismatches", invalid)
	}
}
