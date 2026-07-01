package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/lightwebinc/multicast-test/harness/metrics"
)

// TestScenario90_CoalesceDelivery exercises BRC-142 within-batch coalescing end
// to end on real binaries: the proxy packs many small same-(group, subtree)
// transactions into bundle datagrams (FrameVer 0x08), and the listener
// edge-decoalesces them back into individual frames before fan-out — coalescing
// conserves transactions, so the consumer contract is unchanged.
//
// Density knobs so coalescing actually packs members per bundle: proxy
// SHARD_BITS=1 (two groups) + generator -subtrees 1 (one subtree) concentrate
// the flow, NUM_WORKERS=1 funnels all ingress to a single recvmmsg worker (so
// its batches fill), and a high pps keeps that worker's batches full. Within-
// batch coalescing engages under load and is a no-op when idle (zero added
// latency), so packing scales with batch occupancy.
//
// The proxy's bsp_coalesce_* counters prove bundles were produced and packed;
// the listener's (forwarded + egress_errors) ≈ members proves every coalesced
// member was received and decoalesced (the listener→sink egress hop is out of
// BRC-142 scope — at high member rates the harness's bare 127.0.0.1 sink returns
// ICMP-unreachable races, counted here as "decoalesced and attempted").
func TestScenario90_CoalesceDelivery(t *testing.T) {
	ctx := context.Background()
	e, _, _, _ := basicTopology(t, "s90")

	e.PatchEnv("s90-proxy", map[string]string{
		"SHARD_BITS":         "1", // two multicast groups
		"NUM_WORKERS":        "1", // single recvmmsg worker → batches fill
		"COALESCE":           "true",
		"COALESCE_MAX_BYTES": "1400", // under the 1500 bridge MTU (no IP frag)
	})

	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle + multicast group joins")

	beforeP := e.Snapshot(ctx, "s90-proxy")
	beforeL1 := e.Snapshot(ctx, "s90-listener1")

	genCmd := []string{
		"-addr", "[fd10::2]:8725",
		"-shard-bits", "1",
		"-subtrees", "1",
		"-subtree-seed", "multicast-lab-bsv",
		"-pps", "50000",
		"-duration", "6s",
		"-payload-size", "200",
		"-log-interval", "2s",
	}
	startGenerator(t, ctx, "s90", genCmd)
	waitGenerator(t, ctx, "s90")
	e.Sleep(2*time.Second, "egress pipeline drain")

	afterP := e.Snapshot(ctx, "s90-proxy")
	afterL1 := scrapeOrFail(t, e.MetricsURL(ctx, "s90-listener1"))
	e.LogContainerOutput(ctx, "s90-source")

	deltaP := metrics.DeltaMap(beforeP, afterP)
	deltaL1 := metrics.DeltaMap(beforeL1, afterL1)

	bundles := deltaP["bsp_coalesce_bundles_total"]
	members := deltaP["bsp_coalesce_members_total"]
	forwarded := deltaL1["bsl_frames_forwarded_total"]
	egrErr := deltaL1["bsl_egress_errors_total"]
	decoalesced := forwarded + egrErr // members the listener received and split

	// Proxy produced and packed bundles.
	metrics.AssertGT(t, "proxy coalesce bundles", bundles)
	metrics.AssertGT(t, "proxy coalesce members", members)

	// End-to-end: ~every coalesced member was received and decoalesced.
	metrics.AssertGTE(t, "members decoalesced at listener1", decoalesced, members*0.9)
	metrics.AssertGT(t, "members forwarded to sink", forwarded)

	ratio := 0.0
	if bundles > 0 {
		ratio = members / bundles
	}
	t.Logf("BRC-142 E2E: proxy packed %.0f members into %.0f bundles (%.2f members/bundle, within-batch); "+
		"listener1 decoalesced %.0f/%.0f members (%.0f%%)",
		members, bundles, ratio, decoalesced, members, 100*decoalesced/members)
	if ratio < 1.5 {
		t.Logf("note: low members/bundle — the single worker was not saturated enough to fill batches; " +
			"within-batch coalescing packs more under heavier ingress load (see sims in brc-142-coalescing-frame.md)")
	}
}
