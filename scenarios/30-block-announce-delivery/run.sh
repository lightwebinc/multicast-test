#!/usr/bin/env bash
# Scenario 30 — BRC-131 block announcement: basic delivery
#
# Sends BlockAnnounce + CoinbaseTx frame pairs via TCP to the proxy.
# The proxy stamps HashKey/SeqNum in-place and forwards them to
# FF0E::B:FFFE (CtrlGroupControl). All three listeners subscribe to
# this group and must receive and forward every frame regardless of
# their shard/subtree filter configuration.
#
# Expectations:
#   bsl_frames_received_total{version="brc131"}  == blocks*2 on every listener
#   bsl_frames_forwarded_total{proto="udp"}      includes block frames
#   bsl_gaps_detected_total                      == 0 (no loss)
#
# Prerequisites:
#   - Proxy has TCP_LISTEN_PORT set (default 9002 in lab config).
#   - All services running with current binaries (built from this branch).
set -euo pipefail
SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCENARIO_DIR/../lib/common.sh"

: "${PROXY_TCP_ADDR:=[2001:db8:ffff::1]:9002}"
: "${BLOCK_COUNT:=20}"
: "${SUBTREES_PER_BLOCK:=4}"

BEFORE="$SCENARIO_DIR/metrics.before.tsv"
AFTER="$SCENARIO_DIR/metrics.after.tsv"

# Ensure proxy TCP is enabled on ALL proxies; restore on exit.
_TCP_RESTORED_VMS=()
restore_proxy() {
  for vm in "${_TCP_RESTORED_VMS[@]+"${_TCP_RESTORED_VMS[@]}"}"; do
    echo "==> [cleanup] Disabling TCP ingress on $vm"
    lxc exec "$vm" -- bash -c "
      sed -i 's/^TCP_LISTEN_PORT=.*/TCP_LISTEN_PORT=0/' /etc/bitcoin-shard-proxy/config.env
      systemctl restart bitcoin-shard-proxy
    " || true
  done
}
trap restore_proxy EXIT

for _pvm in "${PROXY_VMS[@]}"; do
  _was_zero=$(lxc exec "$_pvm" -- bash -c "
    grep -q '^TCP_LISTEN_PORT=0' /etc/bitcoin-shard-proxy/config.env && echo 1 || echo 0
  " 2>/dev/null || echo 0)
  if [[ "$_was_zero" -eq 1 ]]; then
    echo "==> Enabling TCP ingress on $_pvm (port 9002)"
    lxc exec "$_pvm" -- bash -c "
      sed -i 's/^TCP_LISTEN_PORT=.*/TCP_LISTEN_PORT=9002/' /etc/bitcoin-shard-proxy/config.env
      systemctl restart bitcoin-shard-proxy
    "
    _TCP_RESTORED_VMS+=("$_pvm")
  fi
done
if [[ ${#_TCP_RESTORED_VMS[@]} -gt 0 ]]; then sleep 3; fi

echo "==> Drain residual frames from prior scenario (3s)"
sleep 3

echo "==> Snapshot metrics (before)"
snapshot_metrics "$BEFORE"

echo "==> Sending $BLOCK_COUNT block announcement pairs via TCP → $PROXY_TCP_ADDR"
lxc exec "$SOURCE_VM" -- send-block-announce \
  -addr     "$PROXY_TCP_ADDR" \
  -blocks   "$BLOCK_COUNT" \
  -subtrees "$SUBTREES_PER_BLOCK" \
  -coinbase=true \
  -interval 50ms

echo "==> Allow multicast pipeline to drain (3s)"
sleep 3

echo "==> Snapshot metrics (after)"
snapshot_metrics "$AFTER"

# --- Assertions ----------------------------------------------------------------

# Expected frames: 2 per block (BlockAnnounce + CoinbaseTx)
expected_frames=$(( BLOCK_COUNT * 2 ))

echo ""
echo "Expected frames per listener: $expected_frames (${BLOCK_COUNT} blocks x 2 msg types)"
echo ""

all_pass=1
for host in "${LISTENERS[@]}"; do
  recv=$(diff_metric "$BEFORE" "$AFTER" "$host" 'bsl_frames_received_total|version="brc131"')
  fwd=$(diff_metric  "$BEFORE" "$AFTER" "$host" 'bsl_frames_forwarded_total|proto="udp"')
  gaps=$(diff_metric "$BEFORE" "$AFTER" "$host" bsl_gaps_detected_total)
  echo "$host: brc131_received=$recv  forwarded_udp=$fwd  gaps=$gaps"

  # Every listener must receive all block frames (no shard/subtree filtering).
  assert_near "$host brc131_received == $expected_frames" \
    "$recv" "$expected_frames" 0.05

  # Forwarded must be at least the block frames.
  if (( fwd >= recv )); then
    echo "PASS  $host forwarded ($fwd) >= brc131_received ($recv)"
  else
    echo "FAIL  $host forwarded ($fwd) < brc131_received ($recv)"
    SCENARIO_FAIL=1
  fi

  # No gaps expected (no loss injected).
  assert_near "$host gaps_detected == 0" "$gaps" 0 0.00
done

if [[ "$SCENARIO_FAIL" -ne 0 ]]; then
  echo ""
  echo "Scenario 30: FAIL"
  exit 1
fi
echo ""
echo "Scenario 30: PASS"
