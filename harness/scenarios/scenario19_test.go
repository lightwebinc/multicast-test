package scenarios

import (
	"context"
	"testing"
	"time"

	"github.com/lightwebinc/multicast-test/harness/driver"
	dockerdriver "github.com/lightwebinc/multicast-test/harness/driver/docker"
	"github.com/lightwebinc/multicast-test/harness/env"
	"github.com/lightwebinc/multicast-test/harness/metrics"
)

// Scenario 19: Listener retry-tee feeds a join-less cache (tee-only repair)
//
// The retry-endpoint runs MC_JOIN_ENABLED=false: it never joins a multicast
// group and never binds the shared wildcard data port, the collapsed-node
// co-bind end-state. Its ONLY frame feed is the co-resident listener's
// -retry-tee, which mirrors every fabric-received data frame over loopback
// ([::1]:9002) in a teewire envelope carrying the original datagram source.
// Co-residency is real: the tee listener container joins the retry
// container's network namespace (ShareNetNS), sharing loopback exactly like
// a collapsed edge.
//
// Two lossy remote listeners then NACK the retry and must be repaired from
// that tee-fed cache. This is the failure mode the tee exists to prevent,
// proven in the repairing direction: had the tee not fed remote-origin
// frames, every cross-node NACK would MISS silently (the "dedicated tee port
// without a listener feed" trap). Asserted:
//
//   - bre_tee_datagrams_total{form="encap"} > 0 and form="raw" == 0, the
//     cache was fed by the listener envelope path alone.
//   - bre_cache_stored_total{source="<proxy fabric addr>"} > 0 and
//     {source="::1"} == 0, the envelope preserved per-source attribution
//     (the RetryCacheSourceStarved alerting contract) instead of collapsing
//     the tee feed onto the loopback label.
//   - bre_unicast_retransmits_total > 0, repairs actually SERVED from the
//     tee-fed cache (testing.md criterion #2).
//   - The full criterion-#1 shortfall identity on every listener, and
//     delivered == sent (clean regime: the 2% netem loss fully repaired).
//
// Preconditions follow scenario 17 (unicast-only repair, no -seq-gap-*,
// lossless warmup and trailer, rate limits out of the way); the tee node
// itself takes no netem loss so the cache feed is complete.
func TestScenario19_ListenerTeeFedJoinlessRepair(t *testing.T) {
	ctx := context.Background()
	e := env.New(t, dockerdriver.New())

	e.AddNode(driver.NodeConfig{
		Name:        "s19-proxy",
		Image:       "shard-proxy:harness",
		IPv6:        "fd10::2",
		Env:         proxyEnv(),
		MetricsPort: 9100,
		Role:        driver.RoleProxy,
	})

	// Tee-only retry: no multicast join, no wildcard data-port bind. Fed
	// exclusively via [::1]:9002 by the co-resident listener below. Unicast
	// repair, rate limits high enough that the clean-regime identity holds
	// (a silently rate-dropped NACK burns a retry round; see scenario 17).
	renv := retryEnv()
	renv["MC_JOIN_ENABLED"] = "false"
	renv["TEE_LISTEN"] = "[::1]:9002"
	renv["BEACON_FLAGS_MULTICAST"] = "false"
	renv["BEACON_FLAGS_UNICAST"] = "true"
	renv["RL_IP_RATE"] = "50000"
	renv["RL_IP_BURST"] = "10000"
	renv["RL_CHAIN_RATE"] = "10000"
	renv["RL_CHAIN_WINDOW"] = "60s"
	renv["RL_SEQUENCE_MAX"] = "10000"
	renv["RL_SEQUENCE_WINDOW"] = "60s"
	renv["RL_GROUP_RATE"] = "10000"
	renv["RL_GROUP_BURST"] = "10000"
	e.AddNode(driver.NodeConfig{
		Name:        "s19-retry1",
		Image:       "retry-endpoint:harness",
		IPv6:        "fd10::20",
		Env:         renv,
		MetricsPort: 9400,
		Role:        driver.RoleRetry,
	})

	// listener1 = the co-resident tee feeder, sharing the retry's network
	// namespace (one node, shared loopback, the collapsed-edge shape). No
	// filters: the cache must cover every group. Registered AFTER the retry
	// so its namespace exists at start.
	l1env := listenerEnv()
	l1env["RETRY_TEE"] = "[::1]:9002"
	l1env["RETRY_ENDPOINTS"] = "[fd10::20]:9300"
	e.AddNode(driver.NodeConfig{
		Name:        "s19-listener1",
		Image:       "shard-listener:harness",
		Env:         l1env,
		MetricsPort: 9200,
		Role:        driver.RoleListener,
		ShareNetNS:  "s19-retry1",
	})

	// listener2/3: ordinary remote listeners that will take loss and depend
	// on the tee-fed cache for repair. Unfiltered so the shortfall identity
	// is exact.
	for i, suffix := range []string{"2", "3"} {
		lenv := listenerEnv()
		lenv["RETRY_ENDPOINTS"] = "[fd10::20]:9300"
		e.AddNode(driver.NodeConfig{
			Name:        "s19-listener" + suffix,
			Image:       "shard-listener:harness",
			IPv6:        []string{"fd10::12", "fd10::13"}[i],
			Env:         lenv,
			MetricsPort: 9200,
			Role:        driver.RoleListener,
		})
	}

	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle")

	// Lossless warmup: baseline every flow before loss exists.
	warm := subtxGenCmd("[fd10::2]:8725")
	warm = append(warm, "-pps", "500", "-duration", "3s")
	startGenerator(t, ctx, "s19", warm)
	waitGenerator(t, ctx, "s19")
	e.Sleep(2*time.Second, "warmup drain (quiet point)")

	beforeL := snapshotListeners(t, e, ctx, "s19")
	beforeP := e.Snapshot(ctx, "s19-proxy")
	beforeR := e.Snapshot(ctx, "s19-retry1")

	// Loss on the two remote listeners only, never on the tee node, whose
	// receive stream IS the cache feed.
	for _, l := range []string{"s19-listener2", "s19-listener3"} {
		if err := env.ApplyNetemLoss(ctx, l, 2.0); err != nil {
			t.Fatalf("netem loss %s: %v", l, err)
		}
		t.Cleanup(func() { env.RemoveNetemLoss(ctx, l) }) //nolint:errcheck
	}

	genCmd := subtxGenCmd("[fd10::2]:8725")
	genCmd = append(genCmd, "-pps", "1000", "-duration", "10s")
	startGenerator(t, ctx, "s19", genCmd)
	waitGenerator(t, ctx, "s19")

	for _, l := range []string{"s19-listener2", "s19-listener3"} {
		env.RemoveNetemLoss(ctx, l) //nolint:errcheck
	}

	// Lossless trailer: extend every chain past its last loss.
	trail := subtxGenCmd("[fd10::2]:8725")
	trail = append(trail, "-pps", "500", "-duration", "3s")
	startGenerator(t, ctx, "s19", trail)
	waitGenerator(t, ctx, "s19")

	afterL := settleRecovery(t, e, ctx, "s19", beforeL, 30*time.Second)
	afterP := metrics.ScrapeOrFail(t, e.MetricsURL(ctx, "s19-proxy"))
	retryURL := e.MetricsURL(ctx, "s19-retry1")
	afterR := metrics.ScrapeOrFail(t, retryURL)
	e.LogContainerOutput(ctx, "s19-source")

	deltaP := metrics.DeltaMap(beforeP, afterP)
	sent := deltaP["bsp_packets_forwarded_total"]
	deltaR := metrics.DeltaMap(beforeR, afterR)
	gapsDetected := sumListenerDelta("s19", "bsl_gaps_detected_total", beforeL, afterL)

	teeEncap, err := metrics.ScrapeWithLabel(retryURL, "bre_tee_datagrams_total", "form", "encap")
	if err != nil {
		t.Fatalf("scrape tee form=encap: %v", err)
	}
	teeRaw, err := metrics.ScrapeWithLabel(retryURL, "bre_tee_datagrams_total", "form", "raw")
	if err != nil {
		t.Fatalf("scrape tee form=raw: %v", err)
	}
	storedFromProxy, err := metrics.ScrapeWithLabel(retryURL, "bre_cache_stored_total", "source", "fd10::2")
	if err != nil {
		t.Fatalf("scrape stored source=proxy: %v", err)
	}
	storedFromLoopback, err := metrics.ScrapeWithLabel(retryURL, "bre_cache_stored_total", "source", "::1")
	if err != nil {
		t.Fatalf("scrape stored source=::1: %v", err)
	}

	teeSent := sumListenerDelta("s19", "bsl_retry_tee_frames_total", beforeL, afterL)
	teeErrs := sumListenerDelta("s19", "bsl_retry_tee_errors_total", beforeL, afterL)

	t.Logf("proxy: sent=%.0f", sent)
	t.Logf("listener tee: mirrored=%.0f errors=%.0f", teeSent, teeErrs)
	t.Logf("retry (join-less): tee_encap=%.0f tee_raw=%.0f cached=%.0f stored{proxy}=%.0f stored{::1}=%.0f",
		teeEncap, teeRaw, deltaR["bre_frames_cached_total"], storedFromProxy, storedFromLoopback)
	t.Logf("retry: nacks=%.0f unicast_retransmits=%.0f misses=%.0f rate_drops=%.0f",
		deltaR["bre_nack_requests_total"], deltaR["bre_unicast_retransmits_total"],
		deltaR["bre_cache_misses_total"], deltaR["bre_rate_limit_drops_total"])

	metrics.AssertGT(t, "proxy sent frames", sent)
	metrics.AssertGT(t, "gaps detected (loss must be real)", gapsDetected)
	metrics.AssertZero(t, "proxy post-stamp drops", deltaP["bsp_packets_dropped_total"])

	// The cache's feed was the listener tee, envelope form, and nothing else.
	metrics.AssertGT(t, "listener frames mirrored to tee", teeSent)
	metrics.AssertZero(t, "tee mirror write errors", teeErrs)
	metrics.AssertGT(t, "retry tee datagrams (encap)", teeEncap)
	metrics.AssertZero(t, "retry tee datagrams (raw, nothing else feeds this cache)", teeRaw)

	// Source preservation: tee-fed frames are attributed to the true fabric
	// source, never to the loopback the mirror rode in on.
	metrics.AssertGT(t, `bre_cache_stored_total{source="fd10::2"}`, storedFromProxy)
	metrics.AssertZero(t, `bre_cache_stored_total{source="::1"}`, storedFromLoopback)

	// Repairs actually served from the join-less cache (criterion #2)…
	metrics.AssertGT(t, "unicast retransmits served", deltaR["bre_unicast_retransmits_total"])

	// …and the full criterion-#1 identity, clean regime, on every listener.
	for i, name := range []string{"listener1", "listener2", "listener3"} {
		assertRecoveryIdentity(t, name, beforeL[i], afterL[i], sent)
		d := metrics.DeltaMap(beforeL[i], afterL[i])
		delivered := d["bsl_frames_forwarded_total"] + d["bsl_egress_errors_total"]
		metrics.AssertEq(t, name+" delivered == sent (loss fully repaired)", delivered, sent)
	}
}
