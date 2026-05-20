#!/usr/bin/env bash
# Scenario 35 — Block header egress: stripped BRC-131 retransmission
#
# Sends BlockAnnounce frames via TCP to the proxy. Listener1 is
# temporarily configured with HEADER_EGRESS_ENABLED=true, forwarding
# stripped 172-byte BRC-131 datagrams (92B header + 80B block header)
# to a local UDP sink. The test verifies:
#
#   1. bsl_header_forwarded_total increments for each BlockAnnounce
#   2. bsl_header_egress_errors_total remains 0
#   3. The stripped datagrams actually arrive at the sink (correct count)
#
# Coinbase frames (BlockMsgCoinbase) must NOT produce header egress.
#
# Prerequisites:
#   - Proxy has TCP_LISTEN_PORT set (default 9002 in lab config).
#   - Listener binary includes header egress support.
set -euo pipefail
SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCENARIO_DIR/../lib/common.sh"

: "${PROXY_TCP_ADDR:=[fd20::2]:9002}"
: "${BLOCK_COUNT:=20}"
: "${SUBTREES_PER_BLOCK:=4}"
: "${HEADER_SINK_PORT:=9107}"

BEFORE="$SCENARIO_DIR/metrics.before.tsv"
AFTER="$SCENARIO_DIR/metrics.after.tsv"

# --- Setup: enable proxy TCP if needed ----------------------------------------

PROXY_TCP_WAS_ZERO=0
L1_HEADER_WAS_OFF=1
restore_all() {
  # Restore listener1 config: remove header egress vars and restart.
  if [[ "$L1_HEADER_WAS_OFF" -eq 1 ]]; then
    echo "==> [cleanup] Disabling header egress on listener1"
    lxc exec listener1 -- bash -c "
      sed -i '/^HEADER_EGRESS_ENABLED=/d' /etc/bitcoin-shard-listener/config.env
      sed -i '/^HEADER_EGRESS_ADDR=/d' /etc/bitcoin-shard-listener/config.env
      sed -i '/^HEADER_EGRESS_PROTO=/d' /etc/bitcoin-shard-listener/config.env
      systemctl restart bitcoin-shard-listener
    " || true
  fi
  # Restore proxy TCP if we enabled it.
  if [[ "$PROXY_TCP_WAS_ZERO" -eq 1 ]]; then
    echo "==> [cleanup] Disabling proxy TCP ingress"
    lxc exec proxy -- bash -c "
      sed -i 's/^TCP_LISTEN_PORT=.*/TCP_LISTEN_PORT=0/' /etc/bitcoin-shard-proxy/config.env
      systemctl restart bitcoin-shard-proxy
    " || true
  fi
  # Stop socat inside the container if still running.
  lxc exec listener1 -- pkill -f "socat.*${HEADER_SINK_PORT}" 2>/dev/null || true
}
trap restore_all EXIT

# Ensure proxy TCP is enabled.
PROXY_TCP_WAS_ZERO=$(lxc exec proxy -- bash -c "
  grep -q '^TCP_LISTEN_PORT=0' /etc/bitcoin-shard-proxy/config.env && echo 1 || echo 0
" 2>/dev/null || echo 0)
if [[ "$PROXY_TCP_WAS_ZERO" -eq 1 ]]; then
  echo "==> Enabling proxy TCP ingress (port 9002)"
  lxc exec proxy -- bash -c "
    sed -i 's/^TCP_LISTEN_PORT=.*/TCP_LISTEN_PORT=9002/' /etc/bitcoin-shard-proxy/config.env
    systemctl restart bitcoin-shard-proxy
  "
  sleep 3
fi

# --- Setup: enable header egress on listener1 ---------------------------------

# Check if header egress is already enabled.
L1_HEADER_WAS_OFF=$(lxc exec listener1 -- bash -c "
  grep -q '^HEADER_EGRESS_ENABLED=true' /etc/bitcoin-shard-listener/config.env && echo 0 || echo 1
" 2>/dev/null || echo 1)

if [[ "$L1_HEADER_WAS_OFF" -eq 1 ]]; then
  echo "==> Enabling header egress on listener1 -> 127.0.0.1:$HEADER_SINK_PORT/udp"
  lxc exec listener1 -- bash -c "
    sed -i '/^HEADER_EGRESS_ENABLED=/d' /etc/bitcoin-shard-listener/config.env
    sed -i '/^HEADER_EGRESS_ADDR=/d' /etc/bitcoin-shard-listener/config.env
    sed -i '/^HEADER_EGRESS_PROTO=/d' /etc/bitcoin-shard-listener/config.env
    echo HEADER_EGRESS_ENABLED=true >> /etc/bitcoin-shard-listener/config.env
    echo HEADER_EGRESS_ADDR=127.0.0.1:$HEADER_SINK_PORT >> /etc/bitcoin-shard-listener/config.env
    echo HEADER_EGRESS_PROTO=udp >> /etc/bitcoin-shard-listener/config.env
    systemctl restart bitcoin-shard-listener
  "
  sleep 3
fi

# --- Setup: start a UDP sink on listener1 to count datagrams ------------------

# Run socat as a daemon inside the container. nohup + background (&) lets the
# lxc exec process exit immediately; socat keeps running as an orphan adopted
# by container init. Each 172-byte stripped BRC-131 datagram is appended to
# SINK_OUT; we count datagrams by dividing total bytes by 172.
SINK_OUT="/tmp/header-egress-sink-$$"
lxc exec listener1 -- rm -f "$SINK_OUT" 2>/dev/null || true
lxc exec listener1 -- bash -c \
  "nohup socat -u 'UDP4-LISTEN:${HEADER_SINK_PORT},reuseaddr,fork' 'OPEN:${SINK_OUT},creat,append' >/dev/null 2>&1 &"
sleep 1

# --- Test run -----------------------------------------------------------------

echo "==> Snapshot metrics (before)"
snapshot_metrics "$BEFORE"

echo "==> Sending $BLOCK_COUNT block announcement pairs via TCP -> $PROXY_TCP_ADDR"
lxc exec "$SOURCE_VM" -- send-block-announce \
  -addr     "$PROXY_TCP_ADDR" \
  -blocks   "$BLOCK_COUNT" \
  -subtrees "$SUBTREES_PER_BLOCK" \
  -coinbase=true \
  -interval 50ms

echo "==> Allow pipeline to drain (3s)"
sleep 3

echo "==> Snapshot metrics (after)"
snapshot_metrics "$AFTER"

# --- Assertions ---------------------------------------------------------------

echo ""
echo "Expected: $BLOCK_COUNT header egress datagrams (one per BlockAnnounce, none for CoinbaseTx)"
echo ""

# Metric: bsl_header_forwarded_total on listener1
hdr_fwd=$(diff_metric "$BEFORE" "$AFTER" listener1 bsl_header_forwarded_total)
hdr_err=$(diff_metric "$BEFORE" "$AFTER" listener1 bsl_header_egress_errors_total)
echo "listener1: header_forwarded=$hdr_fwd  header_egress_errors=$hdr_err"

assert_near "listener1 header_forwarded == $BLOCK_COUNT" \
  "$hdr_fwd" "$BLOCK_COUNT" 0.05

assert_near "listener1 header_egress_errors == 0" \
  "$hdr_err" 0 0.00

# Stop the sink inside the container so the file is fully flushed.
lxc exec listener1 -- pkill -f "socat.*${HEADER_SINK_PORT}" 2>/dev/null || true
sleep 0.3

# Count datagrams: each stripped BRC-131 frame is exactly 172 bytes
# (92-byte header + 80-byte block header payload).
sink_bytes=$(lxc exec listener1 -- bash -c "wc -c < '$SINK_OUT' 2>/dev/null || echo 0" 2>/dev/null || echo 0)
sink_bytes=$(echo "$sink_bytes" | tr -d '[:space:]')
sink_count=$(( ${sink_bytes:-0} / 172 ))
echo "listener1: sink_datagrams_received=$sink_count"

assert_near "listener1 sink received == $BLOCK_COUNT" \
  "$sink_count" "$BLOCK_COUNT" 0.05

# Other listeners should NOT have header egress metrics (not configured).
for host in listener2 listener3; do
  other_fwd=$(diff_metric "$BEFORE" "$AFTER" "$host" bsl_header_forwarded_total)
  assert_near "$host header_forwarded == 0" "$other_fwd" 0 0.00
done

# Cleanup sink output.
lxc exec listener1 -- rm -f "$SINK_OUT" 2>/dev/null || true

if [[ "$SCENARIO_FAIL" -ne 0 ]]; then
  echo ""
  echo "Scenario 35: FAIL"
  exit 1
fi
echo ""
echo "Scenario 35: PASS"
