#!/usr/bin/env bash
# Scenario 52: TxID dedup Redis failure — fail-open behaviour.
#
# With TxID dedup configured but Redis stopped, all listeners must continue
# forwarding frames (fail-open). The dedup_errors counter must be non-zero
# (Redis errors are counted), tx_deduped must be zero (no suppression without
# Redis), and forwarded must remain ≈ received on each listener.
set -euo pipefail
SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCENARIO_DIR/../lib/common.sh"

REDIS_VM="redis"

BEFORE="$SCENARIO_DIR/metrics.before.tsv"
AFTER="$SCENARIO_DIR/metrics.after.tsv"

restore_all() {
  echo "==> Cleanup: restoring Redis and listener config..."
  lxc exec "$REDIS_VM" -- systemctl start redis-server 2>/dev/null || true
  sleep 2
  restore_txid_dedup_all
}
trap 'restore_all' EXIT

echo "==> Flushing stale TxID dedup keys from Redis..."
flush_txid_dedup_keys

echo "==> Enabling TxID dedup on all listeners..."
enable_txid_dedup_all

echo "==> Stopping Redis ($REDIS_VM)..."
lxc exec "$REDIS_VM" -- systemctl stop redis-server
echo "     Redis stopped"
sleep 1

echo "==> Snapshot metrics (before)"
snapshot_metrics "$BEFORE"

echo "==> Running generator with Redis down (pps=$PPS duration=${DURATION:-5s})..."
DURATION="${DURATION:-5s}" frames=$(run_generator)

echo "==> Allow egress pipeline to drain"
sleep 2

echo "==> Snapshot metrics (after)"
snapshot_metrics "$AFTER"

echo "==> Restarting Redis..."
lxc exec "$REDIS_VM" -- systemctl start redis-server
sleep 1

for i in 1 2 3; do
  host="listener${i}"
  received=$(diff_metric "$BEFORE" "$AFTER" "$host" bsl_frames_received_total)
  forwarded=$(diff_metric "$BEFORE" "$AFTER" "$host" 'bsl_frames_forwarded_total|proto="udp"')
  tx_deduped=$(diff_metric "$BEFORE" "$AFTER" "$host" bsl_frames_tx_deduped_total)
  dedup_errors=$(diff_metric "$BEFORE" "$AFTER" "$host" bsl_txid_dedup_errors_total)

  echo "==> $host: received=$received  forwarded=$forwarded  tx_deduped=$tx_deduped  dedup_errors=$dedup_errors"

  # Fail-open: forwarded ≈ received (nothing dropped due to Redis being down).
  if [[ "$received" -gt 0 ]]; then
    assert_near "$host fail-open: forwarded ≈ received" "$forwarded" "$received" 0.15
  fi

  # No suppression: Redis is down, so no TxID can be claimed.
  if [[ "$tx_deduped" -ne 0 ]]; then
    echo "FAIL  $host: tx_deduped=$tx_deduped (expected 0 when Redis is down)"
    SCENARIO_FAIL=1
  else
    echo "PASS  $host: tx_deduped=0 (no suppression without Redis)"
  fi

  # Redis errors must be non-zero: the listener attempted to claim but failed.
  if [[ "$received" -gt 0 && "$dedup_errors" -eq 0 ]]; then
    echo "FAIL  $host: dedup_errors=0 but received=$received (Redis errors not counted)"
    SCENARIO_FAIL=1
  else
    echo "PASS  $host: dedup_errors=$dedup_errors (Redis errors counted)"
  fi
done

if [[ "$SCENARIO_FAIL" -ne 0 ]]; then
  echo "Scenario 52: FAIL"
  exit 1
fi
echo "Scenario 52: PASS"
