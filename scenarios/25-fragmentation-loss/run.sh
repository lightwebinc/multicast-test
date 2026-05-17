#!/usr/bin/env bash
# Scenario 25 — BRC-130 fragmentation: fragment loss / reassembly abandonment
#
# Proxy is configured with FRAG_MTU=1500 (2-fragment datagrams for 2 KB payloads).
# A 60% packet-loss rule is applied on all listeners' fabric ingress ports.
# With 2 fragments per transaction and 60% drop rate, the probability of both
# fragments arriving is (0.4)^2 = 16%, so ~84% of transactions fail to reassemble.
#
# Expectations:
#   bsl_reassembly_started_total    > 0    (slots opened on first fragment)
#   bsl_reassembly_abandoned_total  > 0    (TTL-evicted due to missing fragments)
#   bsl_reassembly_completed_total  < bsl_reassembly_started_total
set -euo pipefail
SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

: "${FRAG_MTU:=1500}"
: "${PAYLOAD_SIZE:=2048}"
source "$SCENARIO_DIR/../lib/common.sh"
: "${FRAGMENT_LOSS:=60%}"
# Shorter TTL makes the test faster (abandonment fires sooner).
# listener1's NACK_GAP_TTL would normally cover this; here we rely on
# reassembly.DefaultTTL (10s) which is already short enough.
PROXY_ENV_FILE="/etc/bitcoin-shard-proxy/config.env"

BEFORE="$SCENARIO_DIR/metrics.before.tsv"
AFTER="$SCENARIO_DIR/metrics.after.tsv"

restore_all() {
  lxc exec proxy -- bash -c "
    if [ -f ${PROXY_ENV_FILE}.bak ]; then
      mv ${PROXY_ENV_FILE}.bak ${PROXY_ENV_FILE}
      systemctl restart bitcoin-shard-proxy
    fi
  " || true
  remove_listener_loss
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
sleep 2

echo "==> Applying $FRAGMENT_LOSS fragment loss on listeners"
apply_listener_loss "$FRAGMENT_LOSS"

echo "==> Snapshot metrics (before)"
snapshot_metrics "$BEFORE"

frames=$(PAYLOAD_SIZE=$PAYLOAD_SIZE run_generator)

# Wait for reassembly TTL to expire on incomplete slots (default 10s).
echo "==> Waiting for reassembly TTL eviction (15s)..."
sleep 15

echo "==> Trigger TTL eviction on all listeners via a probe"
# Send a small probe frame to each listener to trigger lazy evictExpired().
# This is optional — a follow-up slot opening will also trigger it.

echo "==> Snapshot metrics (after)"
snapshot_metrics "$AFTER"

sum_metric() {
  local metric="$1" total=0 d
  for h in "${LISTENERS[@]}"; do
    d=$(diff_metric "$BEFORE" "$AFTER" "$h" "$metric")
    total=$(( total + d ))
  done
  echo "$total"
}

started=$(sum_metric bsl_reassembly_started_total)
completed=$(sum_metric bsl_reassembly_completed_total)
abandoned=$(sum_metric bsl_reassembly_abandoned_total)

echo "==> Reassembly metrics:"
echo "    started=$started completed=$completed abandoned=$abandoned"

if [[ "$started" -gt 0 ]]; then
  echo "PASS  reassembly_started > 0 ($started)"
else
  echo "FAIL  reassembly_started == 0 (fragments not reaching listeners)"
  SCENARIO_FAIL=1
fi

if [[ "$abandoned" -gt 0 ]]; then
  echo "PASS  reassembly_abandoned > 0 ($abandoned)"
else
  echo "FAIL  reassembly_abandoned == 0 (expected TTL evictions due to $FRAGMENT_LOSS loss)"
  SCENARIO_FAIL=1
fi

if [[ "$completed" -lt "$started" ]]; then
  echo "PASS  reassembly_completed ($completed) < started ($started)"
else
  echo "FAIL  expected completed < started with $FRAGMENT_LOSS fragment loss"
  SCENARIO_FAIL=1
fi

if [[ "$SCENARIO_FAIL" -ne 0 ]]; then
  echo "Scenario 25: FAIL"
  exit 1
fi
echo "Scenario 25: PASS"
