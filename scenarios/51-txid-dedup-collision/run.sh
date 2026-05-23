#!/usr/bin/env bash
# Scenario 51: TxID dedup collision resistance.
#
# A single listener with TxID dedup enabled receives frames whose TxIDs are
# each unique (SHA256d of distinct payloads — subtx-gen default behaviour).
# No TxID should be falsely suppressed.
#
# Key assertions:
#   tx_deduped == 0  (no false positives)
#   forwarded ≈ received
set -euo pipefail
SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCENARIO_DIR/../lib/common.sh"

LISTENER_VM="listener1"
LISTENER_IP="10.10.10.31"

BEFORE="$SCENARIO_DIR/metrics.before.tsv"
AFTER="$SCENARIO_DIR/metrics.after.tsv"

trap 'restore_txid_dedup "$LISTENER_VM"' EXIT

echo "==> Flushing stale TxID dedup keys from Redis..."
flush_txid_dedup_keys

echo "==> Enabling TxID dedup on $LISTENER_VM only..."
enable_txid_dedup "$LISTENER_VM"
sleep 3

echo "==> Snapshot metrics (before)"
snapshot_metrics "$BEFORE"

echo "==> Running generator (all unique TxIDs, pps=$PPS duration=$DURATION)..."
frames=$(run_generator)

echo "==> Allow egress pipeline to drain"
sleep 2

echo "==> Snapshot metrics (after)"
snapshot_metrics "$AFTER"

received=$(diff_metric "$BEFORE" "$AFTER" listener1 bsl_frames_received_total)
forwarded=$(diff_metric "$BEFORE" "$AFTER" listener1 'bsl_frames_forwarded_total|proto="udp"')
tx_deduped=$(diff_metric "$BEFORE" "$AFTER" listener1 bsl_frames_tx_deduped_total)
dedup_errors=$(diff_metric "$BEFORE" "$AFTER" listener1 bsl_txid_dedup_errors_total)

echo "==> Metrics: received=$received  forwarded=$forwarded  tx_deduped=$tx_deduped  dedup_errors=$dedup_errors"

# Assertion 1: zero false positives — all distinct TxIDs must pass through.
if [[ "$tx_deduped" -ne 0 ]]; then
  echo "FAIL  collision: tx_deduped=$tx_deduped (expected 0 — false positives detected)"
  SCENARIO_FAIL=1
else
  echo "PASS  collision: tx_deduped=0 (no false positives)"
fi

# Assertion 2: forwarded ≈ received (all unique TxIDs forwarded).
assert_near "l1 forwarded ≈ received" "$forwarded" "$received" 0.10

# Assertion 3: no Redis errors during normal operation.
if [[ "$dedup_errors" -ne 0 ]]; then
  echo "WARN  txid_dedup_errors=$dedup_errors (unexpected Redis errors)"
fi

if [[ "$SCENARIO_FAIL" -ne 0 ]]; then
  echo "Scenario 51: FAIL"
  exit 1
fi
echo "Scenario 51: PASS"
