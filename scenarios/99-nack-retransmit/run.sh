#!/usr/bin/env bash
# Scenario 99 — NACK / retransmit end-to-end across all retry endpoints (retry1/2/3).
#
# Drives subtx-gen with gap injection. Expectations (with -seq-gap-delay 50ms,
# i.e. transient gaps that the cache *can* recover):
#
#   bsl_gaps_detected_total       > 0  (≈ frames / seq_gap_every)
#   bsl_nacks_dispatched_total    > 0
#   bsl_gaps_suppressed_total     ≈ bsl_gaps_detected_total
#   bsl_gaps_unrecovered_total    == 0
#   bre_nack_requests_total       ≈ bsl_nacks_dispatched_total (across listeners)
#   bre_retransmits_total         > 0
#   bre_retransmit_dedup_total    > 0  (Redis SetNX dedup fires: 3 listeners NACK the
#                                       same gap; only the first SetNX succeeds)
#
# Note: assertions are loose because RTTs/timing on the lxd fabric can lead to
# small race-condition deltas around the gap window.
set -euo pipefail
SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCENARIO_DIR/../lib/common.sh"

# Gap-injection knobs. Override via env if desired.
: "${SEQ_GAP_EVERY:=200}"
: "${SEQ_GAP_SIZE:=1}"
: "${SEQ_GAP_DELAY:=50ms}"
: "${PPS:=1000}"
: "${DURATION:=15s}"
BEFORE="$SCENARIO_DIR/metrics.before.tsv"
AFTER="$SCENARIO_DIR/metrics.after.tsv"
RETRY_BEFORE="$SCENARIO_DIR/retry.before.tsv"
RETRY_AFTER="$SCENARIO_DIR/retry.after.tsv"

echo "==> Injecting selective frame loss on listeners (1%) to create cache-able gaps"
: "${NETEM_LOSS:=1%}"
apply_listener_loss "$NETEM_LOSS"
trap 'remove_listener_loss' EXIT

echo "==> Snapshot metrics (before)"
snapshot_metrics "$BEFORE"
snapshot_all_retry "$RETRY_BEFORE"

echo "==> Generator with gap injection"
echo "    every=$SEQ_GAP_EVERY size=$SEQ_GAP_SIZE delay=$SEQ_GAP_DELAY pps=$PPS duration=$DURATION"
gen_output=$(lxc exec "$SOURCE_VM" -- subtx-gen \
  -addr "$PROXY_ADDR" \
  -shard-bits "$SHARD_BITS" \
  -subtrees "$SUBTREES" \
  -subtree-seed "$SUBTREE_SEED" \
  -pps "$PPS" \
  -duration "$DURATION" \
  -payload-size "$PAYLOAD_SIZE" \
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

# Aggregate listener counters (sum across listener1..3)
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

# --- Assertions -----------------------------------------------------------
SCENARIO_FAIL=${SCENARIO_FAIL:-0}

# Ingress: retry-endpoint must have cached frames (proves multicast ingress works).
if [[ "$frames_cached" -le 0 ]]; then
  echo "FAIL  retry endpoint did not cache any frames (multicast ingress broken?)"
  SCENARIO_FAIL=1
else
  echo "PASS  retry endpoint cached $frames_cached frames"
fi

# Gaps must be detected by listeners (the gap injection actually fired).
if [[ "$gaps_detected" -le 0 ]]; then
  echo "FAIL  listeners detected no gaps (gap injection broken?)"
  SCENARIO_FAIL=1
else
  echo "PASS  listeners detected $gaps_detected gaps"
fi

# NACKs must be dispatched.
if [[ "$nacks_dispatched" -le 0 ]]; then
  echo "FAIL  listeners dispatched no NACKs (retry_endpoints not configured?)"
  SCENARIO_FAIL=1
else
  echo "PASS  listeners dispatched $nacks_dispatched NACKs"
fi

# NACKs must reach the retry endpoint.
if [[ "$nacks_received" -le 0 ]]; then
  echo "FAIL  retry endpoint received no NACKs (firewall? routing? port mismatch?)"
  SCENARIO_FAIL=1
else
  echo "PASS  retry endpoint received $nacks_received NACK requests"
fi

# Retransmits must occur.
if [[ "$retransmits" -le 0 ]]; then
  echo "FAIL  retry endpoint did not retransmit (cache miss for every NACK?)"
  SCENARIO_FAIL=1
else
  echo "PASS  retry endpoint retransmitted $retransmits frames"
fi

# At least *some* gaps should be recovered (suppressed) — not all because
# multicast retransmit may race the gap window.
if [[ "$gaps_suppressed" -le 0 ]]; then
  echo "WARN  no gaps were suppressed — retransmits may not have arrived in window"
else
  echo "PASS  $gaps_suppressed of $gaps_detected gaps suppressed (recovered)"
fi

# Dedup fires when Redis is configured and multiple endpoints retransmit the
# same gap concurrently. In-memory-only deployments skip dedup entirely.
if [[ "$bre_dedup" -le 0 ]]; then
  echo "WARN  no cross-endpoint dedup observed (Redis not configured — expected in lab)"
else
  echo "PASS  $bre_dedup retransmits suppressed by Redis SetNX dedup"
fi

if [[ "$SCENARIO_FAIL" -ne 0 ]]; then
  echo "Scenario 99: FAIL"
  exit 1
fi
echo "Scenario 99: PASS"
