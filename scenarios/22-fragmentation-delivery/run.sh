#!/usr/bin/env bash
# Scenario 22 — BRC-130 fragmentation: basic delivery
#
# Proxy is configured with FRAG_MTU=1500. The generator sends 2 KB payloads.
# fragDataSize = 1500 - 40 - 8 - 104 = 1348 bytes, so each 2 KB payload is
# split into ceil(2048/1348) = 2 BRC-130 fragment datagrams.
#
# Listeners reassemble the fragments and forward the complete frame.
#
# Expectations:
#   bsl_reassembly_started_total     ≈ frames received (one slot per TxID)
#   bsl_reassembly_completed_total   ≈ bsl_reassembly_started_total
#   bsl_reassembly_abandoned_total   == 0 (all fragments arrive; no loss injected)
#   bsl_frames_forwarded_total{proto=udp}  ≈ bsl_reassembly_completed_total
set -euo pipefail
SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Set payload large enough to exceed fragDataSize (1500-152=1348) BEFORE common.sh
# sets PAYLOAD_SIZE=256.
: "${FRAG_MTU:=1500}"
: "${PAYLOAD_SIZE:=2048}"
source "$SCENARIO_DIR/../lib/common.sh"

BEFORE="$SCENARIO_DIR/metrics.before.tsv"
AFTER="$SCENARIO_DIR/metrics.after.tsv"

trap 'echo "==> Cleanup: restoring proxy config.env..."; restore_frag_all' EXIT

echo "==> Enabling fragmentation on all proxy VMs (FRAG_MTU=$FRAG_MTU)..."
enable_frag_all "$FRAG_MTU"

echo "==> Snapshot metrics (before)"
snapshot_metrics "$BEFORE"

frames=$(PAYLOAD_SIZE=$PAYLOAD_SIZE run_generator)

echo "==> Allow reassembly pipeline to drain"
sleep 12

echo "==> Snapshot metrics (after)"
snapshot_metrics "$AFTER"

# Aggregate across all listeners
sum_metric() {
  local metric="$1" total=0 d
  for h in "${LISTENERS[@]}"; do
    d=$(diff_metric "$BEFORE" "$AFTER" "$h" "$metric")
    total=$(( total + d ))
  done
  echo "$total"
}

started_l1=$(diff_metric "$BEFORE" "$AFTER" listener1 bsl_reassembly_started_total)
completed_l1=$(diff_metric "$BEFORE" "$AFTER" listener1 bsl_reassembly_completed_total)
abandoned=$(sum_metric bsl_reassembly_abandoned_total)
fwd_l1=$(diff_metric "$BEFORE" "$AFTER" listener1 'bsl_frames_forwarded_total|proto="udp"')

echo "==> Fragmentation metrics:"
echo "    listener1: started=$started_l1 completed=$completed_l1 forwarded=$fwd_l1"
echo "    abandoned(all)=$abandoned"

if [[ "$started_l1" -gt 0 ]]; then
  echo "PASS  reassembly_started_l1 > 0 ($started_l1)"
else
  echo "FAIL  reassembly_started_l1 == 0 (fragmentation not working)"
  SCENARIO_FAIL=1
fi

assert_near "reassembly_abandoned == 0"          "$abandoned"   0             0.00
assert_near "listener1 completed ≈ started_l1"   "$completed_l1" "$started_l1" 0.10
assert_near "listener1 forwarded ≈ frames_sent"  "$fwd_l1"       "$frames"     0.10

if [[ "$SCENARIO_FAIL" -ne 0 ]]; then
  echo "Scenario 22: FAIL"
  exit 1
fi
echo "Scenario 22: PASS"
