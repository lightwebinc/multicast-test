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
assert_near "listener2 received (shard 0+1 only)" "$received_l2" "$(( received_l1 / 2 ))"       0.10
assert_near "listener2 dropped subtree_exclude"    "$dropped_l2"  "$(( received_l2 / 8 ))"      0.20
assert_near "listener2 forwarded"                  "$fwd_l2"      "$(( received_l2 * 7 / 8 ))"  0.10

if [[ "$SCENARIO_FAIL" -ne 0 ]]; then
  echo "Scenario 02: FAIL"
  exit 1
fi
echo "Scenario 02: PASS"
