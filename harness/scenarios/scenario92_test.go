package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/lightwebinc/multicast-test/harness/metrics"
)

// Scenario 92: BRC-148 BEEF plane: submit → deliver
//
// A beef-gen source submits BEEF submission records over the proxy's OPEN tx
// TCP port (the 0xBEEF record-tag grammar beside framed/bare-tx; BEEF is an
// open class, no dedicated port required), then again over the optional
// dedicated BEEF lane. The proxy expands each record into a stamped FrameVer
// 0x09 frame on the plane band (0x1000+) and every electing listener
// receives and forwards it.
func TestScenario92_BeefSubmitDeliver(t *testing.T) {
	ctx := context.Background()
	e, _, _, _ := basicTopology(t, "s92")
	e.PatchEnv("s92-proxy", map[string]string{
		"TCP_LISTEN_PORT":  "9002", // open tx port, TCP form (records ride it)
		"BEEF_LISTEN_PORT": "8728", // optional dedicated lane (flow separation)
	})
	for _, l := range []string{"s92-listener1", "s92-listener2", "s92-listener3"} {
		e.PatchEnv(l, map[string]string{"BEEF_TOPICS": "tm_s92"})
	}
	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle")
	e.Sleep(3*time.Second, "drain residual")

	// Phase 1: the open-port path (primary: least-friction public ingress).
	beforeP := e.Snapshot(ctx, "s92-proxy")
	beforeL := snapshotListeners(t, e, ctx, "s92")
	startGenerator(t, ctx, "s92", []string{
		"beef-gen", "-addr", "[fd10::2]:9002", "-topics", "tm_s92",
		"-count", "30", "-interval", "50ms",
	})
	waitGenerator(t, ctx, "s92")
	e.Sleep(3*time.Second, "pipeline drain")
	afterP := metrics.ScrapeOrFail(t, e.MetricsURL(ctx, "s92-proxy"))
	dp := metrics.DeltaMap(beforeP, afterP)
	t.Logf("proxy: beef_submissions=%.0f class_pkts=%.0f fwd=%.0f dropped=%.0f tcp_bytes=%.0f",
		dp["bsp_beef_submissions_total"], dp["bsp_ingress_class_packets_total"],
		dp["bsp_packets_forwarded_total"], dp["bsp_packets_dropped_total"], dp["bsp_tcp_bytes_received_total"])
	afterL := scrapeListeners(t, e, ctx, "s92")

	for i, label := range []string{"listener1", "listener2", "listener3"} {
		delta := metrics.DeltaMap(beforeL[i], afterL[i])
		recv := delta["bsl_frames_received_total"]
		fwd := delta["bsl_frames_forwarded_total"]
		egrErr := delta["bsl_egress_errors_total"]
		t.Logf("%s: beef received=%.0f forwarded=%.0f egrErr=%.0f", label, recv, fwd, egrErr)
		metrics.AssertNear(t, label+" beef received ≈ expected (open port)", recv, 30, 0.10)
		metrics.AssertNear(t, label+" forwarded+egrErr ≈ received", fwd+egrErr, recv, 0.10)
	}

	// Phase 2: dedicated-lane parity (same admission, different 5-tuple).
	before2 := scrapeListeners(t, e, ctx, "s92")
	startGenerator(t, ctx, "s92lane", []string{
		"beef-gen", "-addr", "[fd10::2]:8728", "-topics", "tm_s92",
		"-count", "15", "-interval", "50ms", "-seed", "2",
	})
	waitGenerator(t, ctx, "s92lane")
	e.Sleep(3*time.Second, "pipeline drain")
	after2 := scrapeListeners(t, e, ctx, "s92")

	for i, label := range []string{"listener1", "listener2", "listener3"} {
		delta := metrics.DeltaMap(before2[i], after2[i])
		metrics.AssertNear(t, label+" beef received ≈ expected (dedicated lane)",
			delta["bsl_frames_received_total"], 15, 0.10)
	}
}
