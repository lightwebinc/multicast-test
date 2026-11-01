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

// Scenario 09: Listener payload hash verification
//
// Ported from the retired bash scenario 09.
//
// listener1 has VERIFY_PAYLOAD_HASH=true. The generator runs with
// -corrupt-txid-rate 50 so ~50% of frames have corrupted TxIDs.
// Assertions:
//   - invalid_payload ≈ 50% of received (±20%)
//   - forwarded ≈ 50% of received (±20%)
func TestScenario09_ListenerPayloadVerification(t *testing.T) {
	ctx := context.Background()
	e := env.New(t, dockerdriver.New())

	e.AddNode(driver.NodeConfig{
		Name:        "s09-proxy",
		Image:       "shard-proxy:harness",
		IPv6:        "fd10::2",
		Env:         proxyEnv(),
		MetricsPort: 9100,
		Role:        driver.RoleProxy,
	})

	l1env := listenerEnv()
	l1env["VERIFY_PAYLOAD_HASH"] = "true"
	e.AddNode(driver.NodeConfig{
		Name:        "s09-listener1",
		Image:       "shard-listener:harness",
		IPv6:        "fd10::11",
		Env:         l1env,
		MetricsPort: 9200,
		Role:        driver.RoleListener,
	})

	e.StartAll(ctx)
	e.Sleep(4*time.Second, "MLD querier settle")

	beforeL1 := e.Snapshot(ctx, "s09-listener1")

	genCmd := subtxGenCmd("[fd10::2]:8725")
	// Pin brc124: the listener's -verify-payload-hash is EF-aware (it checks
	// the canonical TxID: SHA256d(payload) for raw, objfmt.TxID for EF), so
	// either format would pass honestly; the pin keeps deterministic RAW-lane
	// coverage of the corrupt-TxID drop path here (the EF verify path is
	// covered by shard-listener listener/reassembly unit tests).
	genCmd = append(genCmd, "-payload-format", "brc124", "-corrupt-txid-rate", "50")
	startGenerator(t, ctx, "s09", genCmd)
	waitGenerator(t, ctx, "s09")

	e.Sleep(2*time.Second, "egress pipeline drain")

	urlL1 := e.MetricsURL(ctx, "s09-listener1")
	afterL1 := metrics.ScrapeOrFail(t, urlL1)
	e.LogContainerOutput(ctx, "s09-source")

	deltaL1 := metrics.DeltaMap(beforeL1, afterL1)
	recvL1 := deltaL1["bsl_frames_received_total"]
	invalidL1 := deltaL1["bsl_frames_invalid_payload_total"]
	fwdL1 := deltaL1["bsl_frames_forwarded_total"]
	egrErrL1 := deltaL1["bsl_egress_errors_total"]

	t.Logf("listener1: received=%.0f invalid=%.0f fwd=%.0f egrErr=%.0f", recvL1, invalidL1, fwdL1, egrErrL1)

	// ~50% should be invalid (±20%).
	metrics.AssertNear(t, "listener1 invalid_payload (≈50%)", invalidL1, recvL1/2, 0.20)
	// ~50% should be forwarded (±20%); fwd+egrErr accounts for ICMP feedback on
	// the connected UDP egress socket when nothing is listening at EGRESS_ADDR.
	metrics.AssertNear(t, "listener1 forwarded+egrErr (≈50%)", fwdL1+egrErrL1, recvL1/2, 0.20)

	metrics.AssertGTE(t, "listener1 received > 2000", recvL1, 2000)
}
