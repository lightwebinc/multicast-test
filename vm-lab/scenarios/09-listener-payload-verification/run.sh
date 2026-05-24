#!/usr/bin/env bash
set -euo pipefail
SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCENARIO_DIR/../lib/common.sh"

LISTENER_VM="listener1"
LISTENER_IP="10.10.10.31"
ENV_FILE="/etc/bitcoin-shard-listener/config.env"

BEFORE="$SCENARIO_DIR/metrics.before.tsv"
AFTER="$SCENARIO_DIR/metrics.after.tsv"

# --- Helper: temporarily enable verify-payload-hash on listener1 -------------
enable_verify_payload_hash() {
  echo "==> Enabling verify-payload-hash on $LISTENER_VM..."
  lxc exec "$LISTENER_VM" -- bash -c "
    cp ${ENV_FILE} ${ENV_FILE}.bak
    sed -i 's|^VERIFY_PAYLOAD_HASH=.*|VERIFY_PAYLOAD_HASH=true|' ${ENV_FILE}
    systemctl restart bitcoin-shard-listener
  "
  echo "     $LISTENER_VM: verify-payload-hash enabled + restarted"
  sleep 2  # Allow service to start
}

restore_verify_payload_hash() {
  echo "==> Cleanup: restoring original config.env on $LISTENER_VM..."
  lxc exec "$LISTENER_VM" -- bash -c "
    if [ -f ${ENV_FILE}.bak ]; then
      mv ${ENV_FILE}.bak ${ENV_FILE}
      systemctl restart bitcoin-shard-listener
    fi
  " || true
  echo "     $LISTENER_VM: config restored"
}
trap 'restore_verify_payload_hash' EXIT

# --- Test --------------------------------------------------------------------
enable_verify_payload_hash

echo "==> Snapshot metrics (before)"
snapshot_metrics "$BEFORE"

# Run generator with 50% TxID corruption rate
echo "==> Running subtx-gen with 50% TxID corruption..."
CORRUPT_TXID_RATE=50 frames=$(run_generator)

echo "==> Allow egress pipeline to drain"
sleep 2

echo "==> Snapshot metrics (after)"
snapshot_metrics "$AFTER"

# Extract metrics for listener1
received=$(diff_metric "$BEFORE" "$AFTER" listener1 bsl_frames_received_total)
invalid=$(diff_metric "$BEFORE" "$AFTER" listener1 bsl_frames_invalid_payload_total)
forwarded=$(diff_metric "$BEFORE" "$AFTER" listener1 'bsl_frames_forwarded_total|proto="udp"')

echo "==> listener1 metrics: received=$received invalid=$invalid forwarded=$forwarded"

# Assertions:
# - invalid_payload should be ~50% of received (corrupted frames dropped)
# - forwarded should be ~50% of received (only valid frames forwarded)
# - Tolerance: ±20% (allowing for random distribution variance)
assert_near "listener1 invalid_payload (≈50%)" "$invalid" "$(( received / 2 ))" 0.20
assert_near "listener1 forwarded (≈50%)" "$forwarded" "$(( received / 2 ))" 0.20

if [[ "$SCENARIO_FAIL" -ne 0 ]]; then
  echo "Scenario 09: FAIL"
  exit 1
fi
echo "Scenario 09: PASS"
