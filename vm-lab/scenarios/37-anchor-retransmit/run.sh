#!/usr/bin/env bash
# Scenario 37 — BRC-134 anchor transaction: NACK retransmission
#
# Injects 10% packet loss on listeners, then sends anchor frames via UDP.
# Retry endpoints cache V6 frames because they join FF0E::B:FFFE.
# Listeners detect SeqNum gaps on the anchor flow and dispatch NACKs;
# retransmissions are routed back to FF0E::B:FFFE (not a shard group).
#
# Expectations:
#   bsl_gaps_detected_total{flow="brc134"}     > 0   (loss produces gaps)
#   bsl_nacks_dispatched_total{flow="brc134"}  > 0   (gaps trigger NACKs)
#   bsl_gaps_unrecovered_total{flow="brc134"}  == 0  (retransmit fills them)
#   brc134 received                            ≈ ANCHOR_COUNT on each listener
#
# Prerequisites:
#   - All services running with current binaries (built from this branch).
#   - At least one retry endpoint reachable (retry1 / retry2 / retry3).
#   - send-anchor-frame binary installed on SOURCE_VM.
set -euo pipefail
SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCENARIO_DIR/../lib/common.sh"

: "${PROXY_UDP_ADDR:=[2001:db8:ffff::1]:9000}"
: "${ANCHOR_COUNT:=50}"
: "${LOSS_PCT:=10}"

BEFORE="$SCENARIO_DIR/metrics.before.tsv"
AFTER="$SCENARIO_DIR/metrics.after.tsv"
RETRY_BEFORE="$SCENARIO_DIR/retry.before.tsv"
RETRY_AFTER="$SCENARIO_DIR/retry.after.tsv"

restore() {
  remove_listener_loss
}
trap restore EXIT

echo "==> Injecting ${LOSS_PCT}% packet loss on all listeners"
apply_listener_loss "${LOSS_PCT}%"

echo "==> Snapshot metrics (before)"
snapshot_metrics "$BEFORE"
snapshot_all_retry "$RETRY_BEFORE"

echo "==> Sending $ANCHOR_COUNT anchor frames via UDP → $PROXY_UDP_ADDR"
lxc exec "$SOURCE_VM" -- send-anchor-frame \
  -addr     "$PROXY_UDP_ADDR" \
  -count    "$ANCHOR_COUNT" \
  -interval 80ms

echo "==> Remove loss rules"
remove_listener_loss

echo "==> Allow retransmission pipeline to drain (15s)"
sleep 15

echo "==> Snapshot metrics (after)"
snapshot_metrics "$AFTER"
snapshot_all_retry "$RETRY_AFTER"

# --- Assertions ----------------------------------------------------------------

echo ""
echo "Expected anchor frames per listener: $ANCHOR_COUNT"
echo ""

SCENARIO_FAIL=0
for host in "${LISTENERS[@]}"; do
  recv=$(diff_metric "$BEFORE" "$AFTER" "$host" 'bsl_frames_received_total|version="brc134"')
  gaps=$(diff_metric "$BEFORE" "$AFTER" "$host" 'bsl_gaps_detected_total|flow="brc134"')
  nacks=$(diff_metric "$BEFORE" "$AFTER" "$host" 'bsl_nacks_dispatched_total|flow="brc134"')
  unrecovered=$(diff_metric "$BEFORE" "$AFTER" "$host" 'bsl_gaps_unrecovered_total|flow="brc134"')
  echo "$host: brc134_received=$recv  gaps=$gaps  nacks=$nacks  unrecovered=$unrecovered"
done

total_retransmits=$(retry_diff_all "$RETRY_BEFORE" "$RETRY_AFTER" bre_retransmits_total)
total_cache_hits=$(retry_diff_all  "$RETRY_BEFORE" "$RETRY_AFTER" bre_cache_hits_total)
echo "retry endpoints: retransmits=$total_retransmits  cache_hits=$total_cache_hits"
echo ""

# All listeners: anchor frames received close to expected (retransmit fills gaps).
for host in "${LISTENERS[@]}"; do
  recv=$(diff_metric "$BEFORE" "$AFTER" "$host" 'bsl_frames_received_total|version="brc134"')
  assert_near "$host brc134_received ≈ expected" "$recv" "$ANCHOR_COUNT" 0.15
done

# At least one listener must have detected gaps (10% loss → some gaps expected).
total_gaps=0
for host in "${LISTENERS[@]}"; do
  g=$(diff_metric "$BEFORE" "$AFTER" "$host" 'bsl_gaps_detected_total|flow="brc134"')
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
  echo "FAIL  retry retransmits=0; anchor frames may not be reaching retry endpoints"
  SCENARIO_FAIL=1
fi

# Unrecovered gaps must be very low (< 5% of expected frames).
total_unrecovered=0
for host in "${LISTENERS[@]}"; do
  u=$(diff_metric "$BEFORE" "$AFTER" "$host" 'bsl_gaps_unrecovered_total|flow="brc134"')
  total_unrecovered=$(( total_unrecovered + u ))
done
max_unrecovered=$(( ANCHOR_COUNT * 5 / 100 ))
if (( total_unrecovered <= max_unrecovered )); then
  echo "PASS  total_unrecovered=$total_unrecovered <= $max_unrecovered (5% of $ANCHOR_COUNT)"
else
  echo "FAIL  total_unrecovered=$total_unrecovered > $max_unrecovered"
  SCENARIO_FAIL=1
fi

if [[ "$SCENARIO_FAIL" -ne 0 ]]; then
  echo ""
  echo "Scenario 37: FAIL"
  exit 1
fi
echo ""
echo "Scenario 37: PASS"
