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
dropped_l2=$(diff_metric  "$BEFORE" "$AFTER" listener2 'bsl_frames_dropped_total|subtree_exclude')
fwd_l2=$(diff_metric      "$BEFORE" "$AFTER" listener2 'bsl_frames_forwarded_total|proto="udp"')

# MLD snooping delivers only groups 0+1 to listener2; verify via received count.
# Directional check: l2 must receive significantly fewer frames than l1.
# Exact 50% ratio is fragile under retransmit inflation (3 listeners NACK groups 0+1,
# only 2 NACK groups 2+3 → l2 inflated more than l1 when retransmit is active).
# Threshold 90% of l1: shard filter must be doing SOMETHING (l2 < l1 * 0.90).
l1_90=$(( received_l1 * 90 / 100 ))
if [[ "$received_l2" -ge "$l1_90" ]]; then
  echo "FAIL  shard filter inactive: l2 received $received_l2 >= 90% of l1 ($l1_90)"
  SCENARIO_FAIL=1
else
  echo "PASS  shard filter active: l2 received $received_l2 < $l1_90 (90% of l1=$received_l1)"
fi
assert_near "listener2 dropped subtree_exclude"    "$dropped_l2"  "$(( received_l2 / 8 ))"      0.20
assert_near "listener2 forwarded"                  "$fwd_l2"      "$(( received_l2 * 7 / 8 ))"  0.10

if [[ "$SCENARIO_FAIL" -ne 0 ]]; then
  echo "Scenario 02: FAIL"
  exit 1
fi
echo "Scenario 02: PASS"
