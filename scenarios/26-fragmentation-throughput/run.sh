#!/usr/bin/env bash
# Scenario 26 — BRC-130 fragmentation: high-throughput delivery ratio
#
# Proxy configured with FRAG_MTU=1500, payload=4096 bytes (ceil(4096/1348)=4 fragments).
# Generator runs at PPS=500 for 10s → ~5000 frames → ~20000 fragment datagrams.
#
# Expectations:
#   bsl_reassembly_completed_total / bsl_reassembly_started_total  >= 0.95
#     (≥95% of all started reassemblies complete under no-loss conditions)
#   bsl_reassembly_abandoned_total == 0
#   listener1 forwarded ≈ listener1 reassembly_completed (100% pass rate)
set -euo pipefail
SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCENARIO_DIR/../lib/common.sh"

: "${FRAG_MTU:=1500}"
: "${PAYLOAD_SIZE:=4096}"
: "${PPS:=500}"
: "${DURATION:=10s}"
PROXY_ENV_FILE="/etc/bitcoin-shard-proxy/config.env"

BEFORE="$SCENARIO_DIR/metrics.before.tsv"
AFTER="$SCENARIO_DIR/metrics.after.tsv"

restore_fragmentation() {
  lxc exec proxy -- bash -c "
    if [ -f ${PROXY_ENV_FILE}.bak ]; then
      mv ${PROXY_ENV_FILE}.bak ${PROXY_ENV_FILE}
      systemctl restart bitcoin-shard-proxy
    fi
  " || true
}
trap 'restore_fragmentation' EXIT

echo "==> Enabling fragmentation (FRAG_MTU=$FRAG_MTU payload=$PAYLOAD_SIZE bytes)"
lxc exec proxy -- bash -c "
  cp ${PROXY_ENV_FILE} ${PROXY_ENV_FILE}.bak
  if grep -q '^FRAG_MTU=' ${PROXY_ENV_FILE}; then
    sed -i 's|^FRAG_MTU=.*|FRAG_MTU=${FRAG_MTU}|' ${PROXY_ENV_FILE}
  else
    echo 'FRAG_MTU=${FRAG_MTU}' >> ${PROXY_ENV_FILE}
  fi
  systemctl restart bitcoin-shard-proxy
"
sleep 3

echo "==> Snapshot metrics (before)"
snapshot_metrics "$BEFORE"

frames=$(PPS=$PPS DURATION=$DURATION PAYLOAD_SIZE=$PAYLOAD_SIZE run_generator)

echo "==> Allow reassembly pipeline to drain"
sleep 3

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
fwd_l1=$(diff_metric "$BEFORE" "$AFTER" listener1 'bsl_frames_forwarded_total|proto="udp"')
completed_l1=$(diff_metric "$BEFORE" "$AFTER" listener1 bsl_reassembly_completed_total)

echo "==> Fragmentation throughput metrics:"
echo "    frames_sent=$frames started=$started completed=$completed abandoned=$abandoned"
echo "    listener1: completed=$completed_l1 forwarded=$fwd_l1"

# Require ≥95% completion rate.
min_completed=$(( started * 95 / 100 ))
if [[ "$completed" -ge "$min_completed" ]]; then
  echo "PASS  completion_rate ≥95%: completed=$completed started=$started"
else
  echo "FAIL  completion_rate <95%: completed=$completed started=$started (need ≥$min_completed)"
  SCENARIO_FAIL=1
fi

assert_near "reassembly_abandoned == 0" "$abandoned" 0 0.00
assert_near "listener1 forwarded ≈ completed_l1" "$fwd_l1" "$completed_l1" 0.10

if [[ "$SCENARIO_FAIL" -ne 0 ]]; then
  echo "Scenario 26: FAIL"
  exit 1
fi
echo "Scenario 26: PASS"
