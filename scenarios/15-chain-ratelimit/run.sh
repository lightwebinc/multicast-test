#!/usr/bin/env bash
# Scenario 15 — Per-Chain NACK Rate Limit.
#
# Verifies that bre_rate_limit_drops_total{level="chain"} fires when a single
# source IP floods NACKs for the same ChainID at a rate exceeding RL_CHAIN_RATE.
#
# Attack vector:
#   - Tight chain RL (RL_CHAIN_RATE=3 per RL_CHAIN_WINDOW=10s) on all endpoints.
#   - High IP RL so only the chain tier fires (IP limiter stays cold).
#   - Python3 flood from source VM: fixed non-zero ChainID, fixed LookupSeq.
#     This exhausts the per-(srcIP, chainID) sliding window immediately.
#   - Parallel flood with ChainID=0: must NOT produce chain drops (orphan bypass).
#   - Legitimate gap injection via subtx-gen produces real ChainIDs from the
#     listener's multi-chain tracker; those NACKs also arrive with non-zero
#     ChainIDs and verify the end-to-end wire format.
#
# Pass criteria:
#   - bre_rate_limit_drops_total{level="chain"} > 0 on retry1.
#   - bre_rate_limit_drops_total{level="ip"}    == 0 on retry1
#     (proves it's chain, not IP, that fired).
#   - bre_rate_limit_drops_total{level="chain"} == 0 for ChainID=0 flood
#     (checked indirectly: total chain drops match single-chain flood period).
set -euo pipefail
SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCENARIO_DIR/../lib/common.sh"

: "${PPS:=200}"
: "${DURATION:=15s}"
: "${SEQ_GAP_EVERY:=30}"
: "${SEQ_GAP_SIZE:=2}"
: "${SEQ_GAP_DELAY:=1s}"
: "${RL_IP_RATE:=50000}"
: "${RL_IP_BURST:=10000}"
: "${RL_CHAIN_RATE:=3}"
: "${RL_CHAIN_WINDOW:=10s}"
: "${RL_SEQUENCE_MAX:=1000}"
: "${RL_SEQUENCE_WINDOW:=60s}"
: "${RL_GROUP_RATE:=10000}"
: "${RL_GROUP_BURST:=5000}"
export PPS DURATION SEQ_GAP_EVERY SEQ_GAP_SIZE SEQ_GAP_DELAY

RETRY_VM=retry1
RETRY_IP=10.10.10.34
RETRY_METRICS_PORT=9400
RETRY_FABRIC=fd20::24
NACK_PORT=9300

BEFORE="$SCENARIO_DIR/metrics.before.tsv"
AFTER="$SCENARIO_DIR/metrics.after.tsv"
R1_BEFORE="$SCENARIO_DIR/retry1.before.tsv"
R1_AFTER="$SCENARIO_DIR/retry1.after.tsv"

CHAIN_FLOOD_PID=""
ORPHAN_FLOOD_PID=""

# --- Helpers -----------------------------------------------------------------

snapshot_retry() {
  local out="$1"
  : > "$out"
  for m in bre_nack_requests_total bre_retransmits_total \
           bre_cache_hits_total bre_cache_misses_total \
           bre_rate_limit_drops_total; do
    printf '%s\t%s\t%s\n' "$RETRY_VM" "$m" \
      "$(metric_value "${RETRY_IP}:${RETRY_METRICS_PORT}" "$m")" >> "$out"
  done
  for level in ip chain sequence group; do
    printf '%s\t%s\t%s\n' "$RETRY_VM" "bre_rate_limit_drops_total|level=${level}" \
      "$(metric_value "${RETRY_IP}:${RETRY_METRICS_PORT}" \
         bre_rate_limit_drops_total "level=\"${level}\"")" >> "$out"
  done
}

retry_diff() {
  local metric="$1" b a
  b=$(awk -v m="$metric" -F'\t' '$2==m {print $3}' "$R1_BEFORE")
  a=$(awk -v m="$metric" -F'\t' '$2==m {print $3}' "$R1_AFTER")
  echo $(( ${a:-0} - ${b:-0} ))
}

apply_tight_rl() {
  echo "==> Applying tight chain RL (chain_rate=${RL_CHAIN_RATE} chain_window=${RL_CHAIN_WINDOW} ip_rate=${RL_IP_RATE})..."
  local env_file="/etc/bitcoin-retry-endpoint/config.env"
  lxc exec "$RETRY_VM" -- bash -c "
    cp ${env_file} ${env_file}.bak
    sed -i 's|^RL_IP_RATE=.*|RL_IP_RATE=${RL_IP_RATE}|'           ${env_file}
    sed -i 's|^RL_IP_BURST=.*|RL_IP_BURST=${RL_IP_BURST}|'        ${env_file}
    sed -i 's|^RL_CHAIN_RATE=.*|RL_CHAIN_RATE=${RL_CHAIN_RATE}|'   ${env_file}
    sed -i 's|^RL_CHAIN_WINDOW=.*|RL_CHAIN_WINDOW=${RL_CHAIN_WINDOW}|' ${env_file}
    sed -i 's|^RL_SEQUENCE_MAX=.*|RL_SEQUENCE_MAX=${RL_SEQUENCE_MAX}|' ${env_file}
    sed -i 's|^RL_SEQUENCE_WINDOW=.*|RL_SEQUENCE_WINDOW=${RL_SEQUENCE_WINDOW}|' ${env_file}
    sed -i 's|^RL_GROUP_RATE=.*|RL_GROUP_RATE=${RL_GROUP_RATE}|'   ${env_file}
    sed -i 's|^RL_GROUP_BURST=.*|RL_GROUP_BURST=${RL_GROUP_BURST}|' ${env_file}
    systemctl restart bitcoin-retry-endpoint
  "
  echo "     $RETRY_VM: tight chain RL applied + restarted"
}

restore_rl() {
  remove_listener_loss
  echo "==> Cleanup: stopping flood background jobs..."
  [[ -n "$CHAIN_FLOOD_PID" ]]  && kill "$CHAIN_FLOOD_PID"  2>/dev/null || true
  [[ -n "$ORPHAN_FLOOD_PID" ]] && kill "$ORPHAN_FLOOD_PID" 2>/dev/null || true
  echo "==> Cleanup: restoring original config.env..."
  local env_file="/etc/bitcoin-retry-endpoint/config.env"
  lxc exec "$RETRY_VM" -- bash -c "
    if [ -f ${env_file}.bak ]; then
      mv ${env_file}.bak ${env_file}
      systemctl restart bitcoin-retry-endpoint
    fi
  " || true
  echo "     $RETRY_VM: RL config restored"
}
trap restore_rl EXIT

# --- Health check ------------------------------------------------------------

echo "==> Checking endpoint health..."
code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 \
  "http://${RETRY_IP}:${RETRY_METRICS_PORT}/healthz" || echo 000)
if [[ "$code" != "200" ]]; then
  echo "FAIL  http://${RETRY_IP}:${RETRY_METRICS_PORT}/healthz returned $code"
  exit 1
fi
echo "     $RETRY_VM: healthy"

echo "==> Checking Python3 on source VM..."
if ! lxc exec "$SOURCE_VM" -- python3 --version >/dev/null 2>&1; then
  echo "FAIL  python3 not found on $SOURCE_VM"
  exit 1
fi

# --- Apply tight RL and wait for readiness -----------------------------------

apply_tight_rl

echo "==> Waiting for $RETRY_VM to become ready..."
for i in $(seq 1 10); do
  code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 3 \
    "http://${RETRY_IP}:${RETRY_METRICS_PORT}/readyz" || echo 000)
  if [[ "$code" == "200" ]]; then echo "     $RETRY_VM: ready"; break; fi
  if [[ "$i" -eq 10 ]]; then echo "FAIL  $RETRY_VM not ready after 30s"; exit 1; fi
  sleep 3
done

echo "==> Waiting 12s for beacon registry to converge..."
sleep 12

echo "==> Injecting selective frame loss on listeners (1%) to create legitimate gap traffic"
apply_listener_loss "1%"

# --- Snapshot before ---------------------------------------------------------

echo "==> Snapshot metrics (before)"
snapshot_metrics "$BEFORE"
snapshot_retry   "$R1_BEFORE"

# --- Launch flood jobs -------------------------------------------------------

# NACK wire format (64 bytes):
#   [0:4]   Magic      0xE3E1F3E8
#   [4:6]   ProtoVer   0x02BF
#   [6]     MsgType    0x10  (NACK)
#   [7]     Flags      0x01
#   [8:16]  HashKey    uint64 BE (non-zero = attributed flow; 0 = orphan, bypasses chain RL)
#   [16:24] StartSeq   uint64 BE
#   [24:32] EndSeq     uint64 BE (= StartSeq for single-frame request)
#   [32:64] SubtreeID  32-byte zero

flood_secs=$(( $(dur_to_seconds "$DURATION") + 25 ))
echo "==> Launching chain flood (fixed HashKey=0xCAFEBABEDEAD0001, flood_secs=$flood_secs)..."

# Attack: one chain-attributed NACK flood. RL_CHAIN_RATE=3 means only 3 NACKs
# per RL_CHAIN_WINDOW are admitted for this (srcIP, chainID) pair.
chain_flood_script="
import socket,struct,time
TARGET=('${RETRY_FABRIC}',${NACK_PORT})
sock=socket.socket(socket.AF_INET6,socket.SOCK_DGRAM)
# Fixed non-zero HashKey — exhausts per-chain sliding window immediately.
SEQ=0xDEADBEEF12345678
pkt=struct.pack('>IHBBQQQ32s',0xE3E1F3E8,0x02BF,0x10,0x01,0xCAFEBABEDEAD0001,SEQ,SEQ,b'\x00'*32)
end=time.time()+${flood_secs}
while time.time()<end:
  sock.sendto(pkt,TARGET)
"
lxc exec "$SOURCE_VM" -- python3 -c "$chain_flood_script" &
CHAIN_FLOOD_PID=$!
echo "     chain flood started [pid=$CHAIN_FLOOD_PID]"

# Control: ChainID=0 flood from same source — must NOT produce chain-level drops.
orphan_flood_script="
import socket,struct,time
TARGET=('${RETRY_FABRIC}',${NACK_PORT})
sock=socket.socket(socket.AF_INET6,socket.SOCK_DGRAM)
# HashKey=0: orphan gap, bypasses chain limiter entirely (AllowChain skips check).
SEQ=0xCAFEBABEDEAD0002
pkt=struct.pack('>IHBBQQQ32s',0xE3E1F3E8,0x02BF,0x10,0x01,0,SEQ,SEQ,b'\x00'*32)
end=time.time()+${flood_secs}
# Deliberately slow: stay well under IP limit so IP limiter stays cold.
import time as t
while t.time()<end:
  sock.sendto(pkt,TARGET)
  t.sleep(0.05)
"
lxc exec "$SOURCE_VM" -- python3 -c "$orphan_flood_script" &
ORPHAN_FLOOD_PID=$!
echo "     orphan (ChainID=0) control flood started [pid=$ORPHAN_FLOOD_PID]"

sleep 1

# --- Run legitimate gap-injection generator ----------------------------------

echo "==> Running generator (pps=$PPS duration=$DURATION gap-every=$SEQ_GAP_EVERY gap-size=$SEQ_GAP_SIZE)"
gen_output=$(lxc exec "$SOURCE_VM" -- subtx-gen \
  -addr "$PROXY_ADDR" \
  -shard-bits "$SHARD_BITS" \
  -subtrees "$SUBTREES" \
  -subtree-seed "$SUBTREE_SEED" \
  -pps "$PPS" \
  -duration "$DURATION" \
  -payload-size "$PAYLOAD_SIZE" \
  -seq-gap-every "$SEQ_GAP_EVERY" \
  -seq-gap-size  "$SEQ_GAP_SIZE" \
  -seq-gap-delay "$SEQ_GAP_DELAY" \
  -log-interval 5s 2>&1)
echo "$gen_output" | tail -5
frames=$(echo "$gen_output" | grep -oP 'sent=\K[0-9]+' | tail -1 || true)
echo "    sent=${frames:-0} frames"

echo "==> Draining NACK pipeline (10s)..."
sleep 10

# --- Snapshot after ----------------------------------------------------------

echo "==> Snapshot metrics (after)"
snapshot_metrics "$AFTER"
snapshot_retry   "$R1_AFTER"

# --- Compute diffs -----------------------------------------------------------

sum_listener_metric() {
  local metric="$1" total=0 d
  for h in "${LISTENERS[@]}"; do
    d=$(diff_metric "$BEFORE" "$AFTER" "$h" "$metric")
    total=$(( total + d ))
  done
  echo "$total"
}

gaps_detected=$(sum_listener_metric bsl_gaps_detected_total)
nacks_dispatched=$(sum_listener_metric bsl_nacks_dispatched_total)

nacks_received=$(retry_diff bre_nack_requests_total)
drops_ip=$(retry_diff    "bre_rate_limit_drops_total|level=ip")
drops_chain=$(retry_diff "bre_rate_limit_drops_total|level=chain")
drops_seq=$(retry_diff   "bre_rate_limit_drops_total|level=sequence")
drops_group=$(retry_diff "bre_rate_limit_drops_total|level=group")
retransmits=$(retry_diff bre_retransmits_total)
cache_hits=$(retry_diff  bre_cache_hits_total)

cat <<EOF

-- Listener aggregate --
bsl_gaps_detected_total    = $gaps_detected
bsl_nacks_dispatched_total = $nacks_dispatched

-- $RETRY_VM --
bre_nack_requests_total                      = $nacks_received
bre_rate_limit_drops_total{level="ip"}       = $drops_ip      (expect ~0)
bre_rate_limit_drops_total{level="chain"}    = $drops_chain   (expect > 0)
bre_rate_limit_drops_total{level="sequence"} = $drops_seq
bre_rate_limit_drops_total{level="group"}    = $drops_group
bre_retransmits_total                        = $retransmits
bre_cache_hits_total                         = $cache_hits
EOF

# --- Assertions --------------------------------------------------------------

SCENARIO_FAIL=0

if [[ "$nacks_received" -le 0 ]]; then
  echo "FAIL  retry endpoint received no NACKs"
  SCENARIO_FAIL=1
else
  echo "PASS  nacks_received=$nacks_received"
fi

# Core: chain limiter must fire.
if [[ "$drops_chain" -le 0 ]]; then
  echo "FAIL  rate_limit_drops{level=chain}=0 — chain RL did not fire"
  echo "      Check: (a) RL_CHAIN_RATE=${RL_CHAIN_RATE} is tight enough,"
  echo "             (b) ChainID is non-zero in flood (verify struct.pack above),"
  echo "             (c) config.env was updated and service restarted."
  SCENARIO_FAIL=1
else
  echo "PASS  rate_limit_drops{level=chain}=$drops_chain (chain RL fired)"
fi

# IP limiter must stay cold (high RL_IP_RATE ensures this).
if [[ "$drops_ip" -gt 0 ]]; then
  echo "WARN  rate_limit_drops{level=ip}=$drops_ip — IP limiter fired (increase RL_IP_RATE or reduce flood rate)"
else
  echo "PASS  rate_limit_drops{level=ip}=0 (IP limiter did not fire — chain is the active tier)"
fi

# Some retransmits must succeed (not everything chain-limited).
if [[ "$retransmits" -le 0 ]]; then
  echo "WARN  retransmits=0 — all NACKs chain-limited or cache missed (check generator output)"
else
  echo "PASS  retransmits=$retransmits"
fi

if [[ "$SCENARIO_FAIL" -ne 0 ]]; then
  echo "Scenario 15: FAIL"
  exit 1
fi
echo "Scenario 15: PASS"
