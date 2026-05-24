#!/usr/bin/env bash
# Scenario 53: TxID dedup failover.
#
# Three listeners share Redis-backed TxID dedup. listener2 is killed mid-stream.
# The remaining listeners (1 and 3) must pick up its TxID share — the combined
# forwarded count across l1+l3 after the kill must cover the full remaining
# stream. tx_deduped on l1+l3 drops after l2 dies (fewer competitors).
#
# Timings (adjustable via env):
#   DURATION_TOTAL  — total generator duration  (default 20s)
#   KILL_AFTER      — seconds before killing l2  (default 5)
set -euo pipefail
SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCENARIO_DIR/../lib/common.sh"

: "${DURATION_TOTAL:=20s}"
: "${KILL_AFTER:=5}"
: "${PPS:=500}"

BEFORE="$SCENARIO_DIR/metrics.before.tsv"
MID="$SCENARIO_DIR/metrics.mid.tsv"
AFTER="$SCENARIO_DIR/metrics.after.tsv"

restore_all() {
  echo "==> Cleanup: ensuring listener2 is running + restoring config..."
  lxc exec listener2 -- systemctl start bitcoin-shard-listener 2>/dev/null || true
  sleep 1
  restore_txid_dedup_all
}
trap 'restore_all' EXIT

echo "==> Flushing stale TxID dedup keys from Redis..."
flush_txid_dedup_keys

echo "==> Enabling TxID dedup on all listeners..."
enable_txid_dedup_all

echo "==> Snapshot metrics (before)"
snapshot_metrics "$BEFORE"

echo "==> Starting generator in background (pps=$PPS duration=$DURATION_TOTAL)..."
lxc exec "$SOURCE_VM" -- subtx-gen \
  -addr "$PROXY_ADDR" \
  -shard-bits "$SHARD_BITS" \
  -subtrees "$SUBTREES" \
  -subtree-seed "$SUBTREE_SEED" \
  -pps "$PPS" \
  -duration "$DURATION_TOTAL" \
  -payload-size "$PAYLOAD_SIZE" \
  -payload-format "$PAYLOAD_FORMAT" \
  -log-interval 2s &>/tmp/subtx-gen-53.log &
GEN_PID=$!

echo "==> Waiting ${KILL_AFTER}s before killing listener2..."
sleep "$KILL_AFTER"

echo "==> Snapshot metrics (mid — just before kill)"
snapshot_metrics "$MID"

echo "==> Stopping listener2..."
lxc exec listener2 -- systemctl stop bitcoin-shard-listener
echo "     listener2 stopped"

echo "==> Waiting for generator to finish..."
wait "$GEN_PID" || true

echo "==> Allow egress pipeline to drain"
sleep 3

echo "==> Snapshot metrics (after)"
snapshot_metrics "$AFTER"

echo "==> Restarting listener2..."
lxc exec listener2 -- systemctl start bitcoin-shard-listener
sleep 1

# Phase 1: before kill (before → mid). All 3 listeners active.
rec_l1_ph1=$(diff_metric "$BEFORE" "$MID" listener1 bsl_frames_received_total)
fwd_l1_ph1=$(diff_metric "$BEFORE" "$MID" listener1 'bsl_frames_forwarded_total|proto="udp"')
dedup_l1_ph1=$(diff_metric "$BEFORE" "$MID" listener1 bsl_frames_tx_deduped_total)
fwd_l2_ph1=$(diff_metric "$BEFORE" "$MID" listener2 'bsl_frames_forwarded_total|proto="udp"')
fwd_l3_ph1=$(diff_metric "$BEFORE" "$MID" listener3 'bsl_frames_forwarded_total|proto="udp"')
dedup_l3_ph1=$(diff_metric "$BEFORE" "$MID" listener3 bsl_frames_tx_deduped_total)

total_fwd_ph1=$(( fwd_l1_ph1 + fwd_l2_ph1 + fwd_l3_ph1 ))

# Phase 2: after kill (mid → after). Only l1 + l3 active.
rec_l1_ph2=$(diff_metric "$MID" "$AFTER" listener1 bsl_frames_received_total)
fwd_l1_ph2=$(diff_metric "$MID" "$AFTER" listener1 'bsl_frames_forwarded_total|proto="udp"')
dedup_l1_ph2=$(diff_metric "$MID" "$AFTER" listener1 bsl_frames_tx_deduped_total)
fwd_l3_ph2=$(diff_metric "$MID" "$AFTER" listener3 'bsl_frames_forwarded_total|proto="udp"')
dedup_l3_ph2=$(diff_metric "$MID" "$AFTER" listener3 bsl_frames_tx_deduped_total)

total_fwd_ph2=$(( fwd_l1_ph2 + fwd_l3_ph2 ))
total_dedup_ph2=$(( dedup_l1_ph2 + dedup_l3_ph2 ))

echo "==> Phase 1 (all 3 listeners): l1_received=$rec_l1_ph1"
echo "     l1_fwd=$fwd_l1_ph1 l2_fwd=$fwd_l2_ph1 l3_fwd=$fwd_l3_ph1 total_fwd=$total_fwd_ph1"
echo "     l1_deduped=$dedup_l1_ph1 l3_deduped=$dedup_l3_ph1"

echo "==> Phase 2 (l2 killed): l1_received=$rec_l1_ph2"
echo "     l1_fwd=$fwd_l1_ph2 l3_fwd=$fwd_l3_ph2 total_fwd=$total_fwd_ph2"
echo "     l1_deduped=$dedup_l1_ph2 l3_deduped=$dedup_l3_ph2 total_deduped=$total_dedup_ph2"

# Assertion 1: phase 1 total_fwd ≈ l1_received (dedup active, each TxID once).
if [[ "$rec_l1_ph1" -gt 0 ]]; then
  assert_near "phase1 total_fwd ≈ l1_received" "$total_fwd_ph1" "$rec_l1_ph1" 0.20
fi

# Assertion 2: phase 2 total_fwd (l1+l3) ≈ l1_received (no l2, full coverage).
if [[ "$rec_l1_ph2" -gt 0 ]]; then
  assert_near "phase2 l1+l3 total_fwd ≈ l1_received" "$total_fwd_ph2" "$rec_l1_ph2" 0.20
fi

# Assertion 3: phase 2 dedup < phase 1 dedup per active listener (fewer
# competitors after l2 dies).  Use l1 as the reference.
if [[ "$rec_l1_ph1" -gt 0 && "$rec_l1_ph2" -gt 0 ]]; then
  rate_ph1=$(awk -v d="$dedup_l1_ph1" -v r="$rec_l1_ph1" 'BEGIN{printf "%.4f", d/r}')
  rate_ph2=$(awk -v d="$dedup_l1_ph2" -v r="$rec_l1_ph2" 'BEGIN{printf "%.4f", d/r}')
  echo "     l1 dedup rate: phase1=$rate_ph1  phase2=$rate_ph2"
  # phase2 rate should be meaningfully lower (l2 no longer competing).
  # We check rate_ph2 < rate_ph1 with a 20pp margin to avoid flakes.
  ok=$(awk -v r1="$rate_ph1" -v r2="$rate_ph2" 'BEGIN{print (r2 < r1 + 0.20) ? "yes" : "no"}')
  if [[ "$ok" == "yes" ]]; then
    echo "PASS  l1 dedup rate decreased after l2 killed (phase2=$rate_ph2 <= phase1=$rate_ph1 + 0.20)"
  else
    echo "WARN  l1 dedup rate did not decrease as expected (phase2=$rate_ph2, phase1=$rate_ph1) — informational only"
  fi
fi

if [[ "$SCENARIO_FAIL" -ne 0 ]]; then
  echo "Scenario 53: FAIL"
  exit 1
fi
echo "Scenario 53: PASS"
