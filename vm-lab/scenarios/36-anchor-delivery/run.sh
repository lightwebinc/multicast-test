#!/usr/bin/env bash
# Scenario 36 — BRC-134 anchor transaction: basic delivery
#
# Sends anchor frames (FrameVerV6 = 0x06) via UDP to the proxy.
# The proxy stamps HashKey/SeqNum in-place and forwards them to
# FF0E::B:FFFE (GroupBlockBroadcast). All three listeners subscribe to
# this group and must receive every anchor frame regardless of their
# shard/subtree filter configuration.
#
# Expectations:
#   bsl_frames_received_total{version="brc134"}  == ANCHOR_COUNT on every listener
#   bsl_frames_forwarded_total{proto="udp"}      includes anchor frames
#   bsl_gaps_detected_total{flow="brc134"}       == 0 (no loss)
#
# Prerequisites:
#   - All services running with current binaries (built from this branch).
#   - send-anchor-frame binary installed on SOURCE_VM.
set -euo pipefail
SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCENARIO_DIR/../lib/common.sh"

: "${PROXY_UDP_ADDR:=[2001:db8:ffff::1]:9000}"
: "${ANCHOR_COUNT:=20}"

BEFORE="$SCENARIO_DIR/metrics.before.tsv"
AFTER="$SCENARIO_DIR/metrics.after.tsv"

echo "==> Snapshot metrics (before)"
snapshot_metrics "$BEFORE"

echo "==> Sending $ANCHOR_COUNT anchor frames via UDP → $PROXY_UDP_ADDR"
lxc exec "$SOURCE_VM" -- send-anchor-frame \
  -addr     "$PROXY_UDP_ADDR" \
  -count    "$ANCHOR_COUNT" \
  -interval 50ms

echo "==> Allow multicast pipeline to drain (3s)"
sleep 3

echo "==> Snapshot metrics (after)"
snapshot_metrics "$AFTER"

# --- Assertions ----------------------------------------------------------------

echo ""
echo "Expected anchor frames per listener: $ANCHOR_COUNT"
echo ""

SCENARIO_FAIL=0
for host in "${LISTENERS[@]}"; do
  recv=$(diff_metric "$BEFORE" "$AFTER" "$host" 'bsl_frames_received_total|version="brc134"')
  fwd=$(diff_metric  "$BEFORE" "$AFTER" "$host" 'bsl_frames_forwarded_total|proto="udp"')
  gaps=$(diff_metric "$BEFORE" "$AFTER" "$host" 'bsl_gaps_detected_total|flow="brc134"')
  echo "$host: brc134_received=$recv  forwarded_udp=$fwd  gaps=$gaps"

  # Every listener must receive all anchor frames (no shard/subtree filtering).
  assert_near "$host brc134_received == $ANCHOR_COUNT" \
    "$recv" "$ANCHOR_COUNT" 0.05

  # Forwarded must be at least the anchor frames.
  if (( fwd >= recv )); then
    echo "PASS  $host forwarded ($fwd) >= brc134_received ($recv)"
  else
    echo "FAIL  $host forwarded ($fwd) < brc134_received ($recv)"
    SCENARIO_FAIL=1
  fi

  # No gaps expected (no loss injected).
  assert_near "$host gaps_detected == 0" "$gaps" 0 0.00
done

if [[ "$SCENARIO_FAIL" -ne 0 ]]; then
  echo ""
  echo "Scenario 36: FAIL"
  exit 1
fi
echo ""
echo "Scenario 36: PASS"
