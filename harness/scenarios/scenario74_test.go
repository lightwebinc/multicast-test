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

// Scenario 74 — Cross-domain NACK proxying (BRC-126 Proxied flag)
//
// Validates that a retry-endpoint serving a downstream multicast domain
// recovers a domain-wide hole from an upstream endpoint.
//
// Topology (one fabric, two domains isolated by mc-group-id):
//
//	                upstream domain (gid 0x000B)        downstream domain (gid 0x00CC)
//	generator → proxy ─┬─▶ upstream-retry (caches all, no loss)
//	                   └─▶ bridge listener ──mc-egress(gid 0x00CC)──▶ ┬─▶ consumer (listener)
//	                       (ingress netem loss; NO retry config)      └─▶ downstream-retry
//	                                                                      (proxy-enabled → upstream-retry)
//
// The bridge listener has netem loss on its ingress and no NACK recovery, so
// frames it never receives it never re-emits — those SeqNums are absent from
// the *entire* downstream domain (both the consumer and the downstream retry),
// exactly the hole a downstream-only cache cannot fill. The upstream retry
// received those frames directly from the proxy. The consumer NACKs the
// downstream retry → local miss → proxied NACK to the upstream retry → the
// frame returns by unicast → the downstream retry re-caches and multicast-
// retransmits it into the downstream domain → the consumer's gap auto-fills.
func TestScenario74_NACKProxyCrossDomain(t *testing.T) {
	const (
		upstreamGID   = "0x000B"
		downstreamGID = "0x00CC"
		bridge        = "s74-bridge"
		consumer      = "s74-consumer"
		dsRetry       = "s74-ds-retry"
		usRetry       = "s74-us-retry"
	)
	ctx := context.Background()
	e := env.New(t, dockerdriver.New())

	// Upstream proxy (gid 0x000B).
	e.AddNode(driver.NodeConfig{
		Name:        "s74-proxy",
		Image:       "shard-proxy:harness",
		IPv6:        "fd10::2",
		Env:         proxyEnv(),
		MetricsPort: 9100,
		Role:        driver.RoleProxy,
	})

	// Upstream retry: caches everything from the proxy; the recovery source.
	usEnv := retryEnv()
	usEnv["NACK_ADDR"] = "fd10::20"
	e.AddNode(driver.NodeConfig{
		Name:        usRetry,
		Image:       "retry-endpoint:harness",
		IPv6:        "fd10::20",
		Env:         usEnv,
		MetricsPort: 9400,
		Role:        driver.RoleRetry,
	})

	// Bridge listener: ingress on the upstream domain, multicast egress into the
	// downstream domain. No RETRY_ENDPOINTS — it never recovers its own ingress
	// gaps, so a dropped frame is absent downstream.
	brEnv := listenerEnv()
	brEnv["MC_EGRESS_ENABLED"] = "true"
	brEnv["MC_EGRESS_GROUP_ID"] = downstreamGID
	brEnv["MC_EGRESS_PORT"] = "9001"
	brEnv["MC_EGRESS_SCOPE"] = "site"
	brEnv["MC_EGRESS_IFACE"] = "eth0"
	delete(brEnv, "RETRY_ENDPOINTS")
	e.AddNode(driver.NodeConfig{
		Name:        bridge,
		Image:       "shard-listener:harness",
		IPv6:        "fd10::30",
		Env:         brEnv,
		MetricsPort: 9200,
		Role:        driver.RoleListener,
	})

	// Downstream consumer: joins the downstream domain, NACKs the downstream retry.
	coEnv := listenerEnv()
	coEnv["MC_GROUP_ID"] = downstreamGID
	coEnv["RETRY_ENDPOINTS"] = "[fd10::40]:9300"
	e.AddNode(driver.NodeConfig{
		Name:        consumer,
		Image:       "shard-listener:harness",
		IPv6:        "fd10::31",
		Env:         coEnv,
		MetricsPort: 9200,
		Role:        driver.RoleListener,
	})

	// Downstream retry: serves the downstream domain; proxies misses upstream.
	dsEnv := retryEnv()
	dsEnv["MC_GROUP_ID"] = downstreamGID
	dsEnv["NACK_ADDR"] = "fd10::40"
	dsEnv["PROXY_ENABLED"] = "true"
	dsEnv["UPSTREAM_RETRY_ENDPOINTS"] = "[fd10::20]:9300"
	e.AddNode(driver.NodeConfig{
		Name:        dsRetry,
		Image:       "retry-endpoint:harness",
		IPv6:        "fd10::40",
		Env:         dsEnv,
		MetricsPort: 9400,
		Role:        driver.RoleRetry,
	})

	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle + multicast group joins")

	// Domain-wide hole: drop frames on the bridge's ingress. Because the bridge
	// never receives them and never recovers, they are absent from the whole
	// downstream domain — only the upstream retry has them.
	if err := env.ApplyNetemLoss(ctx, bridge, 5.0); err != nil {
		t.Fatalf("netem loss on %s: %v", bridge, err)
	}
	t.Cleanup(func() { env.RemoveNetemLoss(context.Background(), bridge) }) //nolint:errcheck

	beforeConsumer := e.Snapshot(ctx, consumer)
	beforeDS := e.Snapshot(ctx, dsRetry)
	beforeUS := e.Snapshot(ctx, usRetry)

	genCmd := subtxGenCmd("[fd10::2]:8725")
	genCmd = append(genCmd, "-duration", "15s")
	startGenerator(t, ctx, "s74", genCmd)
	waitGenerator(t, ctx, "s74")

	e.Sleep(5*time.Second, "NACK → proxy → recover → retransmit pipeline drain")

	afterConsumer := metrics.ScrapeOrFail(t, e.MetricsURL(ctx, consumer))
	afterDS := metrics.ScrapeOrFail(t, e.MetricsURL(ctx, dsRetry))
	afterUS := metrics.ScrapeOrFail(t, e.MetricsURL(ctx, usRetry))

	e.LogContainerOutput(ctx, "s74-source")

	consumerD := metrics.DeltaMap(beforeConsumer, afterConsumer)
	dsD := metrics.DeltaMap(beforeDS, afterDS)
	usD := metrics.DeltaMap(beforeUS, afterUS)

	gapsDetected := consumerD["bsl_gaps_detected_total"]
	nacksDispatched := consumerD["bsl_nacks_dispatched_total"]
	gapsSuppressed := consumerD["bsl_gaps_suppressed_total"]

	cacheMisses := dsD["bre_cache_misses_total"]
	proxyRequests := dsD["bre_proxy_requests_total"]
	proxyRecovered := dsD["bre_proxy_recovered_total"]
	dsRetransmits := dsD["bre_retransmits_total"]

	usNacks := usD["bre_nack_requests_total"]
	usUnicast := usD["bre_unicast_retransmits_total"]

	t.Logf("consumer: gaps_detected=%.0f nacks_dispatched=%.0f gaps_suppressed=%.0f",
		gapsDetected, nacksDispatched, gapsSuppressed)
	t.Logf("ds-retry: cache_misses=%.0f proxy_requests=%.0f proxy_recovered=%.0f retransmits=%.0f",
		cacheMisses, proxyRequests, proxyRecovered, dsRetransmits)
	t.Logf("us-retry: nack_requests=%.0f unicast_retransmits=%.0f", usNacks, usUnicast)

	// Consumer detected a domain-wide hole and NACKed the downstream retry.
	metrics.AssertGT(t, "consumer gaps detected", gapsDetected)
	metrics.AssertGT(t, "consumer NACKs dispatched", nacksDispatched)

	// Downstream retry missed locally, proxied upstream, and recovered.
	metrics.AssertGT(t, "downstream retry cache misses", cacheMisses)
	metrics.AssertGT(t, "downstream proxy requests", proxyRequests)
	metrics.AssertGT(t, "downstream proxy recovered", proxyRecovered)
	metrics.AssertGT(t, "downstream retransmits into the domain", dsRetransmits)

	// Upstream retry served proxied NACKs and returned frames by unicast.
	metrics.AssertGT(t, "upstream NACK requests (proxied)", usNacks)
	metrics.AssertGT(t, "upstream unicast retransmits", usUnicast)

	// The consumer's gaps were ultimately recovered.
	metrics.AssertGT(t, "consumer gaps suppressed (recovered)", gapsSuppressed)
}
