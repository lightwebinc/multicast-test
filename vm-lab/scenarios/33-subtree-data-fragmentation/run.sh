#!/usr/bin/env bash
# Scenario 33 — BRC-132 subtree data: BRC-130 fragmentation + reassembly
#
# Sends large BRC-132 SubtreeData frames that exceed FRAG_MTU. The proxy
# fragments them into BRC-130 datagrams with OrigFrameVer=0x05. Listeners
# reassemble and deliver via the SubtreeDataCallback.
#
# Expectations:
#   bsl_reassembly_started_total                        ≈ FRAME_COUNT
#   bsl_reassembly_completed_total                      ≈ started
#   bsl_reassembly_abandoned_total                      == 0
#   bsl_frames_received_total{version="brc132_reassembled"} ≈ FRAME_COUNT
#   bsl_frames_forwarded_total{proto="udp"}             ≥ completed
set -euo pipefail
SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

: "${PROXY_TCP_ADDR:=[2001:db8:ffff::1]:9002}"
: "${FRAG_MTU:=1500}"
: "${FRAME_COUNT:=20}"
: "${PAYLOAD_SIZE:=8192}"   # 7 fragments each at FRAG_MTU=1500
: "${MSG_TYPE:=hashes}"
: "${INTERVAL:=100ms}"

source "$SCENARIO_DIR/../lib/common.sh"

BEFORE="$SCENARIO_DIR/metrics.before.tsv"
AFTER="$SCENARIO_DIR/metrics.after.tsv"

enable_tcp_and_frag() {
  for vm in "${PROXY_VMS[@]}"; do
    lxc exec "$vm" -- bash -c "
      cp ${PROXY_ENV_FILE} ${PROXY_ENV_FILE}.bak
      if grep -q '^TCP_LISTEN_PORT=' ${PROXY_ENV_FILE}; then
        sed -i 's|^TCP_LISTEN_PORT=.*|TCP_LISTEN_PORT=9002|' ${PROXY_ENV_FILE}
      else
        echo 'TCP_LISTEN_PORT=9002' >> ${PROXY_ENV_FILE}
      fi
      if grep -q '^FRAG_MTU=' ${PROXY_ENV_FILE}; then
        sed -i 's|^FRAG_MTU=.*|FRAG_MTU=${FRAG_MTU}|' ${PROXY_ENV_FILE}
      else
        echo 'FRAG_MTU=${FRAG_MTU}' >> ${PROXY_ENV_FILE}
      fi
      systemctl restart shard-proxy
    "
    echo "     $vm restarted with TCP_LISTEN_PORT=9002 FRAG_MTU=$FRAG_MTU"
  done
  sleep 3
}

restore_proxy() {
  for vm in "${PROXY_VMS[@]}"; do
    lxc exec "$vm" -- bash -c "
      if [ -f ${PROXY_ENV_FILE}.bak ]; then
        mv ${PROXY_ENV_FILE}.bak ${PROXY_ENV_FILE}
        systemctl restart shard-proxy
      fi
    " || true
  done
}
trap restore_proxy EXIT

echo "==> Enabling proxy TCP + fragmentation"
enable_tcp_and_frag

echo "==> Ensuring listeners have SUBTREE_DATA_ENABLED=true and resetting state"
for vm in "${LISTENERS[@]}"; do
  lxc exec "$vm" -- bash -c "
    if grep -q '^SUBTREE_DATA_ENABLED=' /etc/shard-listener/config.env; then
      sed -i 's/^SUBTREE_DATA_ENABLED=.*/SUBTREE_DATA_ENABLED=true/' /etc/shard-listener/config.env
    else
      echo 'SUBTREE_DATA_ENABLED=true' >> /etc/shard-listener/config.env
    fi
    systemctl restart shard-listener
  "
done
sleep 3

echo "==> Snapshot metrics (before)"
snapshot_metrics "$BEFORE"

echo "==> Sending $FRAME_COUNT BRC-132 frames (payload=${PAYLOAD_SIZE}B, frag_mtu=$FRAG_MTU)"
lxc exec "$SOURCE_VM" -- send-subtree-data \
  -addr         "$PROXY_TCP_ADDR" \
  -frames       "$FRAME_COUNT" \
  -msg-type     "$MSG_TYPE" \
  -payload-size "$PAYLOAD_SIZE" \
  -interval     "$INTERVAL"

echo "==> Allow reassembly pipeline to drain (12s)"
sleep 12

echo "==> Snapshot metrics (after)"
snapshot_metrics "$AFTER"

# --- Per-listener helper -------------------------------------------------------
sum_metric() {
  local metric="$1" total=0 d
  for h in "${LISTENERS[@]}"; do
    d=$(diff_metric "$BEFORE" "$AFTER" "$h" "$metric")
    total=$(( total + d ))
  done
  echo "$total"
}

# Spot-check listener1 for reassembly metrics (all listeners should be similar).
started_l1=$(diff_metric "$BEFORE" "$AFTER" listener1 bsl_reassembly_started_total)
completed_l1=$(diff_metric "$BEFORE" "$AFTER" listener1 bsl_reassembly_completed_total)
reassembled_l1=$(diff_metric "$BEFORE" "$AFTER" listener1 \
  'bsl_frames_received_total|version="brc132_reassembled"')
fwd_l1=$(diff_metric "$BEFORE" "$AFTER" listener1 'bsl_frames_forwarded_total|proto="udp"')
abandoned_all=$(sum_metric bsl_reassembly_abandoned_total)

echo ""
echo "listener1: started=$started_l1 completed=$completed_l1 reassembled=$reassembled_l1 forwarded=$fwd_l1"
echo "abandoned (all listeners): $abandoned_all"
echo ""

if [[ "$started_l1" -gt 0 ]]; then
  echo "PASS  reassembly_started_l1 > 0 ($started_l1) — fragmentation working"
else
  echo "FAIL  reassembly_started_l1 == 0 — proxy may not be fragmenting V5 frames"
  SCENARIO_FAIL=1
fi

assert_near "reassembly_abandoned == 0"              "$abandoned_all"  0              0.00
assert_near "listener1 completed ≈ started"          "$completed_l1"   "$started_l1"  0.10
assert_near "listener1 reassembled ≈ $FRAME_COUNT"   "$reassembled_l1" "$FRAME_COUNT" 0.10
assert_near "listener1 forwarded ≥ completed"        "$fwd_l1"         "$completed_l1" 0.10

if [[ "$SCENARIO_FAIL" -ne 0 ]]; then
  echo ""
  echo "Scenario 33: FAIL"
  exit 1
fi
echo ""
echo "Scenario 33: PASS"
