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

received_l3=$(diff_metric "$BEFORE" "$AFTER" listener3 bsl_frames_received_total)
fwd_l3=$(diff_metric     "$BEFORE" "$AFTER" listener3 'bsl_frames_forwarded_total|proto="udp"')
dropped_l3=$(diff_metric "$BEFORE" "$AFTER" listener3 'bsl_frames_dropped_total|subtree_include_miss')

assert_near "listener3 forwarded (subtree-include)"  "$fwd_l3"     "$(( received_l3 * 1 / 8 ))" 0.15
assert_near "listener3 dropped subtree_include_miss" "$dropped_l3" "$(( received_l3 * 7 / 8 ))" 0.10

if [[ "$SCENARIO_FAIL" -ne 0 ]]; then
  echo "Scenario 03: FAIL"
  exit 1
fi
echo "Scenario 03: PASS"
