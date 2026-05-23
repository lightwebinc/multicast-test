#!/usr/bin/env bash
# Scenario 50: TxID dedup basic — cross-listener deduplication.
#
# All 3 listeners share a single Redis. Each TxID is forwarded exactly once
# in total (first-writer wins); the other listeners suppress their egress.
#
# Key assertion:
#   sum(forwarded_l1 + forwarded_l2 + forwarded_l3) ≈ received_l1
#   (l1 has no shard/subtree filter; it receives every frame.)
set -euo pipefail
SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCENARIO_DIR/../lib/common.sh"

BEFORE="$SCENARIO_DIR/metrics.before.tsv"
AFTER="$SCENARIO_DIR/metrics.after.tsv"

trap 'restore_txid_dedup_all' EXIT

echo "==> Flushing stale TxID dedup keys from Redis..."
flush_txid_dedup_keys

echo "==> Enabling TxID dedup on all listeners..."
enable_txid_dedup_all

echo "==> Snapshot metrics (before)"
snapshot_metrics "$BEFORE"

echo "==> Running generator (pps=$PPS duration=$DURATION)..."
frames=$(run_generator)

echo "==> Allow egress pipeline to drain"
sleep 2

echo "==> Snapshot metrics (after)"
snapshot_metrics "$AFTER"

# Per-listener forwarded (UDP unicast) and tx_deduped.
fwd_l1=$(diff_metric "$BEFORE" "$AFTER" listener1 'bsl_frames_forwarded_total|proto="udp"')
fwd_l2=$(diff_metric "$BEFORE" "$AFTER" listener2 'bsl_frames_forwarded_total|proto="udp"')
fwd_l3=$(diff_metric "$BEFORE" "$AFTER" listener3 'bsl_frames_forwarded_total|proto="udp"')
rec_l1=$(diff_metric "$BEFORE" "$AFTER" listener1 bsl_frames_received_total)
rec_l2=$(diff_metric "$BEFORE" "$AFTER" listener2 bsl_frames_received_total)
rec_l3=$(diff_metric "$BEFORE" "$AFTER" listener3 bsl_frames_received_total)
dedup_l1=$(diff_metric "$BEFORE" "$AFTER" listener1 bsl_frames_tx_deduped_total)
dedup_l2=$(diff_metric "$BEFORE" "$AFTER" listener2 bsl_frames_tx_deduped_total)
dedup_l3=$(diff_metric "$BEFORE" "$AFTER" listener3 bsl_frames_tx_deduped_total)

total_fwd=$(( fwd_l1 + fwd_l2 + fwd_l3 ))
total_dedup=$(( dedup_l1 + dedup_l2 + dedup_l3 ))

echo "==> Metrics:"
echo "     listener1: received=$rec_l1  forwarded=$fwd_l1  tx_deduped=$dedup_l1"
echo "     listener2: received=$rec_l2  forwarded=$fwd_l2  tx_deduped=$dedup_l2"
echo "     listener3: received=$rec_l3  forwarded=$fwd_l3  tx_deduped=$dedup_l3"
echo "     total_forwarded=$total_fwd  total_tx_deduped=$total_dedup"
echo "     frames_sent=$frames  l1_received=$rec_l1"

# Assertion 1: total forwarded across all listeners ≈ l1_received.
# With dedup, each TxID is forwarded exactly once.
# Tolerance 15%: allows for natural packet loss + NACK retransmit inflation.
assert_near "total forwarded across l1+l2+l3 ≈ l1_received" \
  "$total_fwd" "$rec_l1" 0.15

# Assertion 2: per-listener (forwarded + tx_deduped) ≈ received.
# Every frame received by a listener is either forwarded or deduped — no gaps.
if [[ "$rec_l1" -gt 0 ]]; then
  fwd_plus_dedup_l1=$(( fwd_l1 + dedup_l1 ))
  assert_near "l1: forwarded+tx_deduped ≈ received" \
    "$fwd_plus_dedup_l1" "$rec_l1" 0.10
fi
if [[ "$rec_l2" -gt 0 ]]; then
  fwd_plus_dedup_l2=$(( fwd_l2 + dedup_l2 ))
  assert_near "l2: forwarded+tx_deduped ≈ received" \
    "$fwd_plus_dedup_l2" "$rec_l2" 0.10
fi
if [[ "$rec_l3" -gt 0 ]]; then
  fwd_plus_dedup_l3=$(( fwd_l3 + dedup_l3 ))
  assert_near "l3: forwarded+tx_deduped ≈ received" \
    "$fwd_plus_dedup_l3" "$rec_l3" 0.10
fi

# Assertion 3: tx_deduped > 0 (dedup is actually firing; at least 2 listeners
# receive overlapping TxIDs, so some must be suppressed).
if [[ "$total_dedup" -eq 0 ]]; then
  echo "FAIL  tx_dedup: total_tx_deduped=0 — dedup does not appear to be active"
  SCENARIO_FAIL=1
else
  echo "PASS  tx_dedup fired: total_tx_deduped=$total_dedup"
fi

if [[ "$SCENARIO_FAIL" -ne 0 ]]; then
  echo "Scenario 50: FAIL"
  exit 1
fi
echo "Scenario 50: PASS"
