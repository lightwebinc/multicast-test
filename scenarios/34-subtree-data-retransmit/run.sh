#!/usr/bin/env bash
# Scenario 34 — BRC-132 subtree data: NACK retransmission pipeline
#
# Injects 10% packet loss on listeners, sends BRC-132 SubtreeData frames,
# then removes loss and waits for NACK/retransmit recovery. Retry endpoints
# cache V5 frames because they join CtrlGroupSubtreeAnnounce (0xFFFB).
# The retransmitter routes cached V5 frames back to 0xFFFB (not a shard group).
#
# Expectations:
#   bsl_gaps_detected_total{flow="brc132"}     > 0   (loss fires)
#   bsl_nacks_dispatched_total{flow="brc132"}  > 0   (NACKs sent)
#   bsl_gaps_unrecovered_total{flow="brc132"}  ≈ 0   (retransmit succeeds)
#   bsl_frames_received_total{version="brc132"} ≈ FRAME_COUNT per listener
#   bre_retransmits_total (retry endpoints)    > 0
#
# Prerequisites:
#   - SUBTREE_DATA_ENABLED=true on all listeners AND retry endpoints.
#   - Proxy TCP ingress enabled (TCP_LISTEN_PORT=9002).
set -euo pipefail
SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

: "${PROXY_TCP_ADDR:=[fd20::2]:9002}"
: "${FRAME_COUNT:=60}"
: "${NODES:=8}"
: "${SUBTREE_COUNT:=8}"
: "${MSG_TYPE:=hashes}"
: "${LOSS_PCT:=10}"
: "${INTERVAL:=80ms}"

source "$SCENARIO_DIR/../lib/common.sh"

BEFORE="$SCENARIO_DIR/metrics.before.tsv"
AFTER="$SCENARIO_DIR/metrics.after.tsv"
RETRY_BEFORE="$SCENARIO_DIR/retry.before.tsv"
RETRY_AFTER="$SCENARIO_DIR/retry.after.tsv"

# Ensure proxy TCP is enabled.
PROXY_TCP_WAS_ZERO=0
restore() {
  remove_listener_loss
  if [[ "$PROXY_TCP_WAS_ZERO" -eq 1 ]]; then
    lxc exec proxy -- bash -c "
      sed -i 's/^TCP_LISTEN_PORT=.*/TCP_LISTEN_PORT=0/' /etc/bitcoin-shard-proxy/config.env
      systemctl restart bitcoin-shard-proxy
    " || true
  fi
}
trap restore EXIT

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

echo "==> Verifying retry endpoints have SUBTREE_DATA_ENABLED=true"
for vm in "${RETRY_VMS[@]}"; do
  enabled=$(lxc exec "$vm" -- bash -c "
    grep -q 'SUBTREE_DATA_ENABLED=true' /etc/bitcoin-retry-endpoint/config.env \
      && echo yes || echo no
  " 2>/dev/null || echo no)
  if [[ "$enabled" != "yes" ]]; then
    echo "WARN  $vm: SUBTREE_DATA_ENABLED not set; enabling now"
    lxc exec "$vm" -- bash -c "
      if grep -q '^SUBTREE_DATA_ENABLED=' /etc/bitcoin-retry-endpoint/config.env; then
        sed -i 's/^SUBTREE_DATA_ENABLED=.*/SUBTREE_DATA_ENABLED=true/' /etc/bitcoin-retry-endpoint/config.env
      else
        echo 'SUBTREE_DATA_ENABLED=true' >> /etc/bitcoin-retry-endpoint/config.env
      fi
      systemctl restart bitcoin-retry-endpoint
    "
    sleep 3
  fi
done

echo "==> Injecting ${LOSS_PCT}% packet loss on all listeners"
apply_listener_loss "${LOSS_PCT}%"

echo "==> Snapshot metrics (before)"
snapshot_metrics "$BEFORE"
snapshot_all_retry "$RETRY_BEFORE"

echo "==> Sending $FRAME_COUNT BRC-132 frames via TCP → $PROXY_TCP_ADDR"
lxc exec "$SOURCE_VM" -- send-subtree-data \
  -addr           "$PROXY_TCP_ADDR" \
  -frames         "$FRAME_COUNT" \
  -msg-type       "$MSG_TYPE" \
  -nodes          "$NODES" \
  -subtree-count  "$SUBTREE_COUNT" \
  -interval       "$INTERVAL"

echo "==> Remove loss rules"
remove_listener_loss

echo "==> Allow retransmission pipeline to drain (15s)"
sleep 15

echo "==> Snapshot metrics (after)"
snapshot_metrics "$AFTER"
snapshot_all_retry "$RETRY_AFTER"

# --- Summary ------------------------------------------------------------------
expected_frames=$FRAME_COUNT

echo ""
echo "Expected frames per listener: $expected_frames"
echo ""

for host in "${LISTENERS[@]}"; do
  recv=$(diff_metric "$BEFORE" "$AFTER" "$host" 'bsl_frames_received_total|version="brc132"')
  gaps=$(diff_metric "$BEFORE" "$AFTER" "$host" 'bsl_gaps_detected_total|flow="brc132"')
  nacks=$(diff_metric "$BEFORE" "$AFTER" "$host" 'bsl_nacks_dispatched_total|flow="brc132"')
  unrecovered=$(diff_metric "$BEFORE" "$AFTER" "$host" 'bsl_gaps_unrecovered_total|flow="brc132"')
  echo "$host: brc132_received=$recv  gaps=$gaps  nacks=$nacks  unrecovered=$unrecovered"
done

total_retransmits=$(retry_diff_all "$RETRY_BEFORE" "$RETRY_AFTER" bre_retransmits_total)
total_cache_hits=$(retry_diff_all  "$RETRY_BEFORE" "$RETRY_AFTER" bre_cache_hits_total)
echo "retry endpoints: retransmits=$total_retransmits  cache_hits=$total_cache_hits"
echo ""

# --- Assertions ---------------------------------------------------------------

# Delivery rate: each listener should receive close to expected frames.
for host in "${LISTENERS[@]}"; do
  recv=$(diff_metric "$BEFORE" "$AFTER" "$host" 'bsl_frames_received_total|version="brc132"')
  assert_near "$host brc132_received ≈ $expected_frames" "$recv" "$expected_frames" 0.15
done

# At least one listener must have detected gaps (10% loss → some expected).
total_gaps=0
for host in "${LISTENERS[@]}"; do
  g=$(diff_metric "$BEFORE" "$AFTER" "$host" 'bsl_gaps_detected_total|flow="brc132"')
  total_gaps=$(( total_gaps + g ))
done
if (( total_gaps > 0 )); then
  echo "PASS  brc132 gaps_detected > 0 across listeners ($total_gaps total)"
else
  echo "FAIL  brc132 gaps_detected == 0; loss injection may not have fired or flow tracking not working"
  SCENARIO_FAIL=1
fi

# At least one listener must have dispatched NACKs.
total_nacks=0
for host in "${LISTENERS[@]}"; do
  n=$(diff_metric "$BEFORE" "$AFTER" "$host" 'bsl_nacks_dispatched_total|flow="brc132"')
  total_nacks=$(( total_nacks + n ))
done
if (( total_nacks > 0 )); then
  echo "PASS  brc132 nacks_dispatched > 0 ($total_nacks total)"
else
  echo "FAIL  brc132 nacks_dispatched == 0; NACK pipeline may not be wired for V5 flow"
  SCENARIO_FAIL=1
fi

# Retry endpoints must have served retransmits.
if (( total_retransmits > 0 )); then
  echo "PASS  retry retransmits=$total_retransmits > 0"
else
  echo "FAIL  retry retransmits=0; V5 frames may not be reaching retry endpoints (check SUBTREE_DATA_ENABLED)"
  SCENARIO_FAIL=1
fi

# Unrecovered gaps must be very low (< 5% of expected frames per listener).
total_unrecovered=0
for host in "${LISTENERS[@]}"; do
  u=$(diff_metric "$BEFORE" "$AFTER" "$host" 'bsl_gaps_unrecovered_total|flow="brc132"')
  total_unrecovered=$(( total_unrecovered + u ))
done
max_unrecovered=$(( expected_frames * 3 * 5 / 100 ))  # 5% × 3 listeners
if (( total_unrecovered <= max_unrecovered )); then
  echo "PASS  total_unrecovered=$total_unrecovered <= $max_unrecovered (5% of ${expected_frames}×3)"
else
  echo "FAIL  total_unrecovered=$total_unrecovered > $max_unrecovered"
  SCENARIO_FAIL=1
fi

if [[ "$SCENARIO_FAIL" -ne 0 ]]; then
  echo ""
  echo "Scenario 34: FAIL"
  exit 1
fi
echo ""
echo "Scenario 34: PASS"
