#!/usr/bin/env bash
# Scenario 07 — Functional BRC-128 + BRC-124 coexistence.
#
# Generator alternates BRC-12 raw and BRC-30 EF payloads on the same
# multicast group. Infrastructure is payload-opaque, so the ratio
# assertions from scenario 01 must hold regardless of mix.
set -euo pipefail
SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

export PAYLOAD_FORMAT=mixed
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

assert_near "listener1 forwarded (mixed)"                        "$fwd_l1" "$received_l1"              0.05
assert_near "listener2 forwarded (mixed, shard×subtree filter)"  "$fwd_l2" "$(( received_l2 * 7 / 8 ))" 0.10
assert_near "listener3 forwarded (mixed, subtree-include)"       "$fwd_l3" "$(( received_l3 * 1 / 8 ))" 0.15

# Mixed traffic must not produce bad_frame drops.
for h in listener1 listener2 listener3; do
  bad=$(diff_metric "$BEFORE" "$AFTER" "$h" 'bsl_frames_dropped_total|bad_frame')
  if [[ "$bad" -gt 0 ]]; then
    echo "FAIL  $h dropped $bad mixed frames as bad_frame"
    SCENARIO_FAIL=1
  else
    echo "PASS  $h bad_frame=0 (mixed BRC-124/BRC-128 traffic transparent)"
  fi
done

if [[ "$SCENARIO_FAIL" -ne 0 ]]; then
  echo "Scenario 07: FAIL"
  exit 1
fi
echo "Scenario 07: PASS"
