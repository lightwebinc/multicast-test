#!/usr/bin/env bash
# Scenario 24 — BRC-130 fragmentation: payload hash verification
#
# Proxy is configured with FRAG_MTU=1500. listener1 has VERIFY_PAYLOAD_HASH=true.
# Generator sends honest payloads (no TxID corruption).
#
# Expectations:
#   bsl_reassembly_completed_total  > 0 (all honest frames pass)
#   bsl_reassembly_hash_mismatch_total == 0 (no hash failures)
#   bsl_frames_forwarded_total       ≈ bsl_reassembly_completed_total
#
# This verifies the SHA256d(reassembled_payload) == TxID check works correctly
# for the BRC-130 reassembly path.
set -euo pipefail
SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

: "${FRAG_MTU:=1500}"
: "${PAYLOAD_SIZE:=2048}"
source "$SCENARIO_DIR/../lib/common.sh"
PROXY_ENV_FILE="/etc/bitcoin-shard-proxy/config.env"
LISTENER_ENV_FILE="/etc/bitcoin-shard-listener/config.env"
LISTENER_VM="listener1"

BEFORE="$SCENARIO_DIR/metrics.before.tsv"
AFTER="$SCENARIO_DIR/metrics.after.tsv"

restore_all() {
  lxc exec proxy -- bash -c "
    if [ -f ${PROXY_ENV_FILE}.bak ]; then
      mv ${PROXY_ENV_FILE}.bak ${PROXY_ENV_FILE}
      systemctl restart bitcoin-shard-proxy
    fi
  " || true
  lxc exec "$LISTENER_VM" -- bash -c "
    if [ -f ${LISTENER_ENV_FILE}.bak ]; then
      mv ${LISTENER_ENV_FILE}.bak ${LISTENER_ENV_FILE}
      systemctl restart bitcoin-shard-listener
    fi
  " || true
}
trap 'restore_all' EXIT

echo "==> Enabling fragmentation on proxy (FRAG_MTU=$FRAG_MTU)"
lxc exec proxy -- bash -c "
  cp ${PROXY_ENV_FILE} ${PROXY_ENV_FILE}.bak
  if grep -q '^FRAG_MTU=' ${PROXY_ENV_FILE}; then
    sed -i 's|^FRAG_MTU=.*|FRAG_MTU=${FRAG_MTU}|' ${PROXY_ENV_FILE}
  else
    echo 'FRAG_MTU=${FRAG_MTU}' >> ${PROXY_ENV_FILE}
  fi
  systemctl restart bitcoin-shard-proxy
"

echo "==> Enabling verify-payload-hash on $LISTENER_VM"
lxc exec "$LISTENER_VM" -- bash -c "
  cp ${LISTENER_ENV_FILE} ${LISTENER_ENV_FILE}.bak
  sed -i 's|^VERIFY_PAYLOAD_HASH=.*|VERIFY_PAYLOAD_HASH=true|' ${LISTENER_ENV_FILE}
  systemctl restart bitcoin-shard-listener
"
sleep 3

echo "==> Snapshot metrics (before)"
snapshot_metrics "$BEFORE"

frames=$(PAYLOAD_SIZE=$PAYLOAD_SIZE run_generator)

echo "==> Allow pipeline to drain"
sleep 12

echo "==> Snapshot metrics (after)"
snapshot_metrics "$AFTER"

completed=$(diff_metric "$BEFORE" "$AFTER" listener1 bsl_reassembly_completed_total)
mismatch=$(diff_metric "$BEFORE" "$AFTER" listener1 bsl_reassembly_hash_mismatch_total)
fwd=$(diff_metric "$BEFORE" "$AFTER" listener1 'bsl_frames_forwarded_total|proto="udp"')

echo "==> listener1 metrics: completed=$completed hash_mismatch=$mismatch forwarded=$fwd"

if [[ "$completed" -gt 0 ]]; then
  echo "PASS  reassembly_completed > 0 ($completed)"
else
  echo "FAIL  reassembly_completed == 0"
  SCENARIO_FAIL=1
fi

assert_near "hash_mismatch == 0"          "$mismatch"  0           0.00
assert_near "forwarded ≈ completed"       "$fwd"       "$completed" 0.10

if [[ "$SCENARIO_FAIL" -ne 0 ]]; then
  echo "Scenario 24: FAIL"
  exit 1
fi
echo "Scenario 24: PASS"
