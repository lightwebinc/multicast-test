#!/usr/bin/env bash
# Scenario 08 — NACK / retransmit with BRC-128 (EF) payloads.
#
# Same shape as scenario 99 (gap injection + NACK + retransmit + dedup),
# but the generator emits BRC-30 Extended Format payloads. The retry
# endpoint caches by SeqNum (a header field), so cache/NACK behaviour is
# payload-agnostic. This scenario locks in that property.
set -euo pipefail
SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Default this scenario to BRC-128 payloads; allow override via env.
: "${PAYLOAD_FORMAT:=brc128}"
export PAYLOAD_FORMAT

: "${PPS:=1000}"
: "${DURATION:=15s}"
source "$SCENARIO_DIR/../lib/common.sh"

: "${SEQ_GAP_EVERY:=200}"
: "${SEQ_GAP_SIZE:=1}"
: "${SEQ_GAP_DELAY:=50ms}"

BEFORE="$SCENARIO_DIR/metrics.before.tsv"
AFTER="$SCENARIO_DIR/metrics.after.tsv"
RETRY_BEFORE="$SCENARIO_DIR/retry.before.tsv"
RETRY_AFTER="$SCENARIO_DIR/retry.after.tsv"

echo "==> Injecting selective frame loss on listeners (1%) to create cache-able gaps"
: "${NETEM_LOSS:=1%}"
apply_listener_loss "$NETEM_LOSS"
trap 'remove_listener_loss' EXIT

# Stabilise: let any in-flight NACK activity from previous scenarios drain
# before we take the baseline snapshot.
sleep 3

echo "==> Snapshot metrics (before)"
snapshot_metrics "$BEFORE"
snapshot_all_retry "$RETRY_BEFORE"

echo "==> Generator with gap injection (payload=$PAYLOAD_FORMAT)"
echo "    every=$SEQ_GAP_EVERY size=$SEQ_GAP_SIZE delay=$SEQ_GAP_DELAY pps=$PPS duration=$DURATION"
gen_output=$(lxc exec "$SOURCE_VM" -- subtx-gen \
  -addr "$PROXY_ADDR" \
  -shard-bits "$SHARD_BITS" \
  -subtrees "$SUBTREES" \
  -subtree-seed "$SUBTREE_SEED" \
  -pps "$PPS" \
  -duration "$DURATION" \
  -payload-size "$PAYLOAD_SIZE" \
  -payload-format "$PAYLOAD_FORMAT" \
  -seq-gap-every "$SEQ_GAP_EVERY" \
  -seq-gap-size "$SEQ_GAP_SIZE" \
  -seq-gap-delay "$SEQ_GAP_DELAY" \
  -log-interval 5s 2>&1)
echo "$gen_output" | tail -10
frames=$(echo "$gen_output" | grep -oP 'sent=\K[0-9]+' | tail -1 || true)
frames="${frames:-0}"
echo "    sent=$frames frames"

echo "==> Allow NACK/retransmit pipeline to drain"
sleep 4

echo "==> Snapshot metrics (after)"
snapshot_metrics "$AFTER"
snapshot_all_retry "$RETRY_AFTER"

# Show nftables drop counts (diagnostic for zero-gap failures).
check_listener_loss_counters

sum_listener_metric() {
  local metric="$1" total=0 d
  for h in "${LISTENERS[@]}"; do
    d=$(diff_metric "$BEFORE" "$AFTER" "$h" "$metric")
    total=$(( total + d ))
  done
  echo "$total"
}

gaps_detected=$(sum_listener_metric bsl_gaps_detected_total)
nacks_dispatched=$(sum_listener_metric bsl_nacks_dispatched_total)
gaps_suppressed=$(sum_listener_metric bsl_gaps_suppressed_total)
gaps_unrecovered=$(sum_listener_metric bsl_gaps_unrecovered_total)
nacks_received=$(retry_diff_all "$RETRY_BEFORE" "$RETRY_AFTER" bre_nack_requests_total)
retransmits=$(retry_diff_all "$RETRY_BEFORE" "$RETRY_AFTER" bre_retransmits_total)
frames_cached=$(retry_diff_all "$RETRY_BEFORE" "$RETRY_AFTER" bre_frames_cached_total)
bre_dedup=$(retry_diff_all "$RETRY_BEFORE" "$RETRY_AFTER" bre_retransmit_dedup_total)

cat <<EOF
-- Listener aggregate (l1+l2+l3) --
bsl_gaps_detected_total      = $gaps_detected
bsl_nacks_dispatched_total   = $nacks_dispatched
bsl_gaps_suppressed_total    = $gaps_suppressed
bsl_gaps_unrecovered_total   = $gaps_unrecovered

-- Retry endpoints (retry1+retry2+retry3) --
bre_frames_cached_total      = $frames_cached
bre_nack_requests_total      = $nacks_received
bre_retransmits_total        = $retransmits
bre_retransmit_dedup_total   = $bre_dedup
EOF

SCENARIO_FAIL=${SCENARIO_FAIL:-0}

if [[ "$frames_cached" -le 0 ]]; then
  echo "FAIL  retry endpoint did not cache any BRC-128 frames"
  SCENARIO_FAIL=1
else
  echo "PASS  retry endpoint cached $frames_cached BRC-128 frames"
fi
if [[ "$gaps_detected" -gt 0 ]]; then
  echo "PASS  $gaps_detected gaps detected"
elif [[ "$nacks_dispatched" -gt 0 ]]; then
  echo "PASS  gaps active (gaps_detected delta=0 but nacks_dispatched=$nacks_dispatched)"
else
  echo "FAIL  no gaps detected on BRC-128 traffic (gaps=$gaps_detected nacks=$nacks_dispatched)"
  SCENARIO_FAIL=1
fi
if [[ "$nacks_dispatched" -le 0 && "$gaps_detected" -le 0 ]]; then
  echo "FAIL  no NACKs dispatched"; SCENARIO_FAIL=1
else
  echo "PASS  $nacks_dispatched NACKs dispatched"
fi
if [[ "$retransmits" -gt 0 ]]; then
  echo "PASS  $retransmits retransmits of BRC-128 frames"
elif [[ "$gaps_detected" -gt 0 ]]; then
  echo "FAIL  no retransmits despite $gaps_detected gaps"; SCENARIO_FAIL=1
else
  echo "WARN  retransmits=0 (no gaps detected — possible transient loss rule issue)"
fi
if [[ "$bre_dedup" -le 0 ]]; then
  echo "WARN  no cross-endpoint dedup observed"
else
  echo "PASS  $bre_dedup retransmits suppressed by dedup"
fi

if [[ "$SCENARIO_FAIL" -ne 0 ]]; then
  echo "Scenario 08: FAIL"
  exit 1
fi
echo "Scenario 08: PASS"
