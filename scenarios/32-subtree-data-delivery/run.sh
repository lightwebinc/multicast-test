#!/usr/bin/env bash
# Scenario 32 — BRC-132 subtree data: basic delivery
#
# Sends inline (unfragmented) BRC-132 SubtreeData frames via TCP to the proxy.
# The proxy stamps HashKey/SeqNum and forwards to CtrlGroupSubtreeAnnounce
# (FF0X::B:FFFB). All listeners with SUBTREE_DATA_ENABLED=true must receive
# and forward every frame.
#
# Expectations:
#   bsl_frames_received_total{version="brc132"}  ≈ FRAME_COUNT on every listener
#   bsl_frames_forwarded_total{proto="udp"}       ≥ received
#   bsl_gaps_detected_total{flow="brc132"}        == 0
#   bsl_reassembly_started_total                  == 0 (no fragmentation)
set -euo pipefail
SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCENARIO_DIR/../lib/common.sh"

: "${PROXY_TCP_ADDR:=[fd20::2]:9002}"
: "${FRAME_COUNT:=30}"
: "${NODES:=8}"          # 8 hashes × 32B = 256B payload — fits in one datagram
: "${MSG_TYPE:=hashes}"
: "${INTERVAL:=50ms}"

BEFORE="$SCENARIO_DIR/metrics.before.tsv"
AFTER="$SCENARIO_DIR/metrics.after.tsv"

# Ensure proxy TCP is enabled; restore on exit.
PROXY_TCP_WAS_ZERO=0
restore_proxy() {
  if [[ "$PROXY_TCP_WAS_ZERO" -eq 1 ]]; then
    lxc exec proxy -- bash -c "
      sed -i 's/^TCP_LISTEN_PORT=.*/TCP_LISTEN_PORT=0/' /etc/bitcoin-shard-proxy/config.env
      systemctl restart bitcoin-shard-proxy
    " || true
  fi
}
trap restore_proxy EXIT

PROXY_TCP_WAS_ZERO=$(lxc exec proxy -- bash -c "
  grep -q '^TCP_LISTEN_PORT=0' /etc/bitcoin-shard-proxy/config.env && echo 1 || echo 0
" 2>/dev/null || echo 0)
if [[ "$PROXY_TCP_WAS_ZERO" -eq 1 ]]; then
  echo "==> Enabling proxy TCP ingress (port 9002)"
  lxc exec proxy -- bash -c "
    sed -i 's/^TCP_LISTEN_PORT=.*/TCP_LISTEN_PORT=9002/' /etc/bitcoin-shard-proxy/config.env
    systemctl restart bitcoin-shard-proxy
  "
  sleep 3
fi

echo "==> Verifying listeners have SUBTREE_DATA_ENABLED=true"
for vm in "${LISTENERS[@]}"; do
  enabled=$(lxc exec "$vm" -- bash -c "
    grep -q 'SUBTREE_DATA_ENABLED=true' /etc/bitcoin-shard-listener/config.env \
      && echo yes || echo no
  " 2>/dev/null || echo no)
  if [[ "$enabled" != "yes" ]]; then
    echo "WARN  $vm: SUBTREE_DATA_ENABLED not set; enabling now"
    lxc exec "$vm" -- bash -c "
      if grep -q '^SUBTREE_DATA_ENABLED=' /etc/bitcoin-shard-listener/config.env; then
        sed -i 's/^SUBTREE_DATA_ENABLED=.*/SUBTREE_DATA_ENABLED=true/' /etc/bitcoin-shard-listener/config.env
      else
        echo 'SUBTREE_DATA_ENABLED=true' >> /etc/bitcoin-shard-listener/config.env
      fi
      systemctl restart bitcoin-shard-listener
    "
    sleep 3
  fi
done

echo "==> Snapshot metrics (before)"
snapshot_metrics "$BEFORE"

echo "==> Sending $FRAME_COUNT BRC-132 frames (msg=$MSG_TYPE nodes=$NODES) via TCP → $PROXY_TCP_ADDR"
lxc exec "$SOURCE_VM" -- send-subtree-data \
  -addr      "$PROXY_TCP_ADDR" \
  -frames    "$FRAME_COUNT" \
  -msg-type  "$MSG_TYPE" \
  -nodes     "$NODES" \
  -interval  "$INTERVAL"

echo "==> Allow multicast pipeline to drain (3s)"
sleep 3

echo "==> Snapshot metrics (after)"
snapshot_metrics "$AFTER"

# --- Assertions ----------------------------------------------------------------
echo ""
echo "Expected frames per listener: $FRAME_COUNT"
echo ""

for host in "${LISTENERS[@]}"; do
  recv=$(diff_metric "$BEFORE" "$AFTER" "$host" 'bsl_frames_received_total|version="brc132"')
  fwd=$(diff_metric  "$BEFORE" "$AFTER" "$host" 'bsl_frames_forwarded_total|proto="udp"')
  gaps=$(diff_metric "$BEFORE" "$AFTER" "$host" 'bsl_gaps_detected_total|flow="brc132"')
  started=$(diff_metric "$BEFORE" "$AFTER" "$host" bsl_reassembly_started_total)
  echo "$host: brc132_received=$recv  forwarded_udp=$fwd  gaps=$gaps  reassembly_started=$started"

  assert_near "$host brc132_received ≈ $FRAME_COUNT" \
    "$recv" "$FRAME_COUNT" 0.05

  if (( fwd >= recv )); then
    echo "PASS  $host forwarded ($fwd) >= received ($recv)"
  else
    echo "FAIL  $host forwarded ($fwd) < received ($recv)"
    SCENARIO_FAIL=1
  fi

  assert_near "$host gaps_detected == 0" "$gaps" 0 0.00
  assert_near "$host reassembly_started == 0" "$started" 0 0.00
done

if [[ "$SCENARIO_FAIL" -ne 0 ]]; then
  echo ""
  echo "Scenario 32: FAIL"
  exit 1
fi
echo ""
echo "Scenario 32: PASS"
