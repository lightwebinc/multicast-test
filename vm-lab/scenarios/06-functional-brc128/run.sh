#!/usr/bin/env bash
# Scenario 06 — Functional BRC-128 (EF payload, all shards).
#
# Same shape as scenario 01, but the generator emits BRC-30 Extended Format
# (BRC-128) payloads instead of BRC-12 raw transactions. The frame header is
# identical (BRC-124, 92-byte v2), so proxy/listener/retry process them
# verbatim. This scenario proves that infrastructure is payload-agnostic.
set -euo pipefail
SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

export PAYLOAD_FORMAT=brc128
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

bad_l1=$(diff_metric "$BEFORE" "$AFTER" listener1 'bsl_frames_dropped_total|bad_frame')
bad_l2=$(diff_metric "$BEFORE" "$AFTER" listener2 'bsl_frames_dropped_total|bad_frame')
bad_l3=$(diff_metric "$BEFORE" "$AFTER" listener3 'bsl_frames_dropped_total|bad_frame')

assert_near "listener1 forwarded"                        "$fwd_l1" "$received_l1"              0.05
assert_near "listener2 forwarded (shardxsubtree filter)" "$fwd_l2" "$(( received_l2 * 7 / 8 ))" 0.10
assert_near "listener3 forwarded (subtree-include)"      "$fwd_l3" "$(( received_l3 * 1 / 8 ))" 0.15

# EF payloads must NOT trip the bad_frame counter (header is identical to BRC-124).
for h in listener1:$bad_l1 listener2:$bad_l2 listener3:$bad_l3; do
  name="${h%:*}"; val="${h##*:}"
  if [[ "$val" -gt 0 ]]; then
    echo "FAIL  $name dropped $val BRC-128 frames as bad_frame (header parse failure?)"
    SCENARIO_FAIL=1
  else
    echo "PASS  $name bad_frame=0 (EF payload transparent to header parser)"
  fi
done

if [[ "$SCENARIO_FAIL" -ne 0 ]]; then
  echo "Scenario 06: FAIL"
  exit 1
fi
echo "Scenario 06: PASS"
