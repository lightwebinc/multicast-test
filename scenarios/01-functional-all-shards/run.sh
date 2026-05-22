#!/usr/bin/env bash
set -euo pipefail
SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCENARIO_DIR/../lib/common.sh"

BEFORE="$SCENARIO_DIR/metrics.before.tsv"
AFTER="$SCENARIO_DIR/metrics.after.tsv"

echo "==> Snapshot metrics (before)"
snapshot_metrics "$BEFORE"

frames=$(run_generator)

echo "==> Allow egress pipeline to drain"
sleep 2

echo "==> Snapshot metrics (after)"
snapshot_metrics "$AFTER"

received_l1=$(diff_metric "$BEFORE" "$AFTER" listener1 bsl_frames_received_total)
received_l2=$(diff_metric "$BEFORE" "$AFTER" listener2 bsl_frames_received_total)
received_l3=$(diff_metric "$BEFORE" "$AFTER" listener3 bsl_frames_received_total)
fwd_l1=$(diff_metric "$BEFORE" "$AFTER" listener1 'bsl_frames_forwarded_total|proto="udp"')
fwd_l2=$(diff_metric "$BEFORE" "$AFTER" listener2 'bsl_frames_forwarded_total|proto="udp"')
fwd_l3=$(diff_metric "$BEFORE" "$AFTER" listener3 'bsl_frames_forwarded_total|proto="udp"')

assert_near "listener1 forwarded"                        "$fwd_l1" "$received_l1"              0.05
assert_near "listener2 forwarded (shardxsubtree filter)" "$fwd_l2" "$(( received_l2 * 7 / 8 ))" 0.10
assert_near "listener3 forwarded (subtree-include)"      "$fwd_l3" "$(( received_l3 * 1 / 8 ))" 0.15

if [[ "$SCENARIO_FAIL" -ne 0 ]]; then
  echo "Scenario 01: FAIL"
  exit 1
fi
echo "Scenario 01: PASS"
