#!/usr/bin/env bash
# Scenario 23 — BRC-130 fragmentation: shard filter
#
# Proxy is configured with FRAG_MTU=1500 (2-fragment datagrams).
# listener2 is configured with shard-include=0 (accepts only group-0 frames).
# listener3 is configured with subtree_include (accepts 1/8 subtrees).
#
# Expectations:
#   listener1: reassembly_completed ≈ received frames (no filter)
#   listener2: reassembly_completed ≈ reassembly_started / num_groups (shard filter)
#              forwarded ≈ reassembly_completed x 7/8 (subtree filter)
#   listener3: forwarded ≈ reassembly_completed x 1/8
set -euo pipefail
SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

: "${FRAG_MTU:=1500}"
: "${PAYLOAD_SIZE:=2048}"
source "$SCENARIO_DIR/../lib/common.sh"

BEFORE="$SCENARIO_DIR/metrics.before.tsv"
AFTER="$SCENARIO_DIR/metrics.after.tsv"

trap 'restore_frag_all' EXIT

echo "==> Enabling fragmentation on all proxy VMs (FRAG_MTU=$FRAG_MTU)"
enable_frag_all "$FRAG_MTU"

echo "==> Snapshot metrics (before)"
snapshot_metrics "$BEFORE"

frames=$(PAYLOAD_SIZE=$PAYLOAD_SIZE run_generator)

echo "==> Allow pipeline to drain"
sleep 12

echo "==> Snapshot metrics (after)"
snapshot_metrics "$AFTER"

num_groups=$(( 1 << SHARD_BITS ))

started_l1=$(diff_metric "$BEFORE" "$AFTER" listener1 bsl_reassembly_started_total)
completed_l1=$(diff_metric "$BEFORE" "$AFTER" listener1 bsl_reassembly_completed_total)
fwd_l1=$(diff_metric "$BEFORE" "$AFTER" listener1 'bsl_frames_forwarded_total|proto="udp"')

started_l2=$(diff_metric "$BEFORE" "$AFTER" listener2 bsl_reassembly_started_total)
completed_l2=$(diff_metric "$BEFORE" "$AFTER" listener2 bsl_reassembly_completed_total)
fwd_l2=$(diff_metric "$BEFORE" "$AFTER" listener2 'bsl_frames_forwarded_total|proto="udp"')

completed_l3=$(diff_metric "$BEFORE" "$AFTER" listener3 bsl_reassembly_completed_total)
fwd_l3=$(diff_metric "$BEFORE" "$AFTER" listener3 'bsl_frames_forwarded_total|proto="udp"')

echo "==> Metrics:"
echo "    listener1: started=$started_l1 completed=$completed_l1 fwd=$fwd_l1"
echo "    listener2: started=$started_l2 completed=$completed_l2 fwd=$fwd_l2"
echo "    listener3: completed=$completed_l3 fwd=$fwd_l3"

# listener2: SHARD_INCLUDE=0,1 → receives 2 of 4 groups (50% of transactions)
assert_near "listener1 completed ≈ started_l1"         "$completed_l1" "$started_l1"                  0.10
assert_near "listener1 fwd ≈ completed_l1"             "$fwd_l1"       "$completed_l1"                0.10
assert_near "listener2 started ≈ started_l1/2"         "$started_l2"   "$(( started_l1 / 2 ))"        0.20
assert_near "listener2 fwd ≈ completed_l2 x 7/8"      "$fwd_l2"       "$(( completed_l2 * 7 / 8 ))" 0.15
assert_near "listener3 fwd ≈ completed_l3 x 1/8"      "$fwd_l3"       "$(( completed_l3 * 1 / 8 ))" 0.20

if [[ "$SCENARIO_FAIL" -ne 0 ]]; then
  echo "Scenario 23: FAIL"
  exit 1
fi
echo "Scenario 23: PASS"
