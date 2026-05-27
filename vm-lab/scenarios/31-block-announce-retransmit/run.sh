#!/usr/bin/env bash
# Scenario 31 — BRC-131 block announcement: NACK retransmission
#
# Injects 10% packet loss on listeners, then sends block announcement pairs.
# Retry endpoints cache the V4 frames because they also join FF0E::B:FFFE.
# Listeners detect SeqNum gaps on the control flow and dispatch NACKs;
# retransmissions are routed back to FF0E::B:FFFE (not a shard group).
#
# Expectations:
#   bsl_gaps_detected_total     > 0   (loss produces gaps)
#   bsl_nacks_dispatched_total  > 0   (gaps trigger NACKs)
#   bsl_gaps_unrecovered_total  == 0  (retransmit fills them within tolerance)
#   brc131 received             ≈ expected_frames on each listener
#
# Prerequisites:
#   - All services running with current binaries (built from this branch).
#   - At least one retry endpoint reachable (retry1 / retry2 / retry3).
#   - Proxy TCP enabled (TCP_LISTEN_PORT=9002).
set -euo pipefail
SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCENARIO_DIR/../lib/common.sh"

: "${PROXY_TCP_ADDR:=[2001:db8:ffff::1]:9002}"
: "${BLOCK_COUNT:=50}"
: "${SUBTREES_PER_BLOCK:=4}"
: "${LOSS_PCT:=10}"

BEFORE="$SCENARIO_DIR/metrics.before.tsv"
AFTER="$SCENARIO_DIR/metrics.after.tsv"
RETRY_BEFORE="$SCENARIO_DIR/retry.before.tsv"
RETRY_AFTER="$SCENARIO_DIR/retry.after.tsv"

# Ensure proxy TCP is enabled on ALL proxies; restore on exit.
_TCP_RESTORED_VMS=()
restore() {
  remove_listener_loss
  for vm in "${_TCP_RESTORED_VMS[@]+"${_TCP_RESTORED_VMS[@]}"}"; do
    echo "==> [cleanup] Disabling TCP ingress on $vm"
    lxc exec "$vm" -- bash -c "
      sed -i 's/^TCP_LISTEN_PORT=.*/TCP_LISTEN_PORT=0/' /etc/shard-proxy/config.env
      systemctl restart shard-proxy
    " || true
  done
}
trap restore EXIT

for _pvm in "${PROXY_VMS[@]}"; do
  _was_zero=$(lxc exec "$_pvm" -- bash -c "
    grep -q '^TCP_LISTEN_PORT=0' /etc/shard-proxy/config.env && echo 1 || echo 0
  " 2>/dev/null || echo 0)
  if [[ "$_was_zero" -eq 1 ]]; then
    echo "==> Enabling TCP ingress on $_pvm (port 9002)"
    lxc exec "$_pvm" -- bash -c "
      sed -i 's/^TCP_LISTEN_PORT=.*/TCP_LISTEN_PORT=9002/' /etc/shard-proxy/config.env
      systemctl restart shard-proxy
    "
    _TCP_RESTORED_VMS+=("$_pvm")
  fi
done
if [[ ${#_TCP_RESTORED_VMS[@]} -gt 0 ]]; then sleep 3; fi

echo "==> Drain residual frames from prior scenario (3s)"
sleep 3

echo "==> Injecting ${LOSS_PCT}% packet loss on all listeners"
apply_listener_loss "${LOSS_PCT}%"

echo "==> Snapshot metrics (before)"
snapshot_metrics "$BEFORE"
snapshot_all_retry "$RETRY_BEFORE"

echo "==> Sending $BLOCK_COUNT block announcement pairs via TCP → $PROXY_TCP_ADDR"
lxc exec "$SOURCE_VM" -- send-block-announce \
  -addr     "$PROXY_TCP_ADDR" \
  -blocks   "$BLOCK_COUNT" \
  -subtrees "$SUBTREES_PER_BLOCK" \
  -coinbase=true \
  -interval 80ms

echo "==> Remove loss rules"
remove_listener_loss

echo "==> Allow retransmission pipeline to drain (15s)"
sleep 15

echo "==> Snapshot metrics (after)"
snapshot_metrics "$AFTER"
snapshot_all_retry "$RETRY_AFTER"

# --- Expected totals ---
expected_frames=$(( BLOCK_COUNT * 2 ))

# --- Assertions ----------------------------------------------------------------
echo ""
echo "Expected frames per listener: $expected_frames"
echo ""

for host in "${LISTENERS[@]}"; do
  recv=$(diff_metric "$BEFORE" "$AFTER" "$host" 'bsl_frames_received_total|version="brc131"')
  gaps=$(diff_metric "$BEFORE" "$AFTER" "$host" 'bsl_gaps_detected_total|flow="brc131"')
  nacks=$(diff_metric "$BEFORE" "$AFTER" "$host" 'bsl_nacks_dispatched_total|flow="brc131"')
  unrecovered=$(diff_metric "$BEFORE" "$AFTER" "$host" 'bsl_gaps_unrecovered_total|flow="brc131"')
  echo "$host: brc131_received=$recv  gaps=$gaps  nacks=$nacks  unrecovered=$unrecovered"
done

total_retransmits=$(retry_diff_all "$RETRY_BEFORE" "$RETRY_AFTER" bre_retransmits_total)
total_cache_hits=$(retry_diff_all  "$RETRY_BEFORE" "$RETRY_AFTER" bre_cache_hits_total)
echo "retry endpoints: retransmits=$total_retransmits  cache_hits=$total_cache_hits"
echo ""

# All listeners: block frames received close to expected (retransmit fills gaps).
for host in "${LISTENERS[@]}"; do
  recv=$(diff_metric "$BEFORE" "$AFTER" "$host" 'bsl_frames_received_total|version="brc131"')
  assert_near "$host brc131_received ≈ expected" "$recv" "$expected_frames" 0.25
done

# At least one listener must have detected gaps (10% loss → some gaps expected).
total_gaps=0
for host in "${LISTENERS[@]}"; do
  g=$(diff_metric "$BEFORE" "$AFTER" "$host" 'bsl_gaps_detected_total|flow="brc131"')
  total_gaps=$(( total_gaps + g ))
done
if (( total_gaps > 0 )); then
  echo "PASS  gaps_detected > 0 across listeners ($total_gaps total)"
else
  echo "FAIL  gaps_detected == 0; loss injection may not have fired"
  SCENARIO_FAIL=1
fi

# Retry endpoints must have served retransmits.
if (( total_retransmits > 0 )); then
  echo "PASS  retry retransmits=$total_retransmits > 0"
else
  echo "FAIL  retry retransmits=0; block frames may not be reaching retry endpoints"
  SCENARIO_FAIL=1
fi

# Unrecovered gaps must be very low (< 5% of expected frames).
total_unrecovered=0
for host in "${LISTENERS[@]}"; do
  u=$(diff_metric "$BEFORE" "$AFTER" "$host" 'bsl_gaps_unrecovered_total|flow="brc131"')
  total_unrecovered=$(( total_unrecovered + u ))
done
max_unrecovered=$(( expected_frames * 5 / 100 ))
if (( total_unrecovered <= max_unrecovered )); then
  echo "PASS  total_unrecovered=$total_unrecovered <= $max_unrecovered (5% of $expected_frames)"
else
  echo "FAIL  total_unrecovered=$total_unrecovered > $max_unrecovered"
  SCENARIO_FAIL=1
fi

if [[ "$SCENARIO_FAIL" -ne 0 ]]; then
  echo ""
  echo "Scenario 31: FAIL"
  exit 1
fi
echo ""
echo "Scenario 31: PASS"
