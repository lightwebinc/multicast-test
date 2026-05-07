#!/usr/bin/env bash
# Scenario 14 — Multi-Endpoint Rate Limit Defense.
#
# Drives three simultaneous attack vectors against all three retry endpoints:
#
#   1. Legitimate NACK escalation: subtx-gen gap injection → listener gap
#      detection → NACK chain retry1 (T0/P128) → retry2 (T0/P64) → retry3
#      (T1/P128), each silently rate-limiting and forcing backoff escalation.
#
#   2. Rogue node flood: Python3 UDP flood from source VM (fd20::10, not a
#      listener) targeting all three endpoints directly on port 9300 with a
#      fixed LookupSeq. Proves per-IP limiter fires for arbitrary IPs.
#
#   3. Compromised listener flood: Python3 UDP flood from listener1 VM
#      (fd20::21, a legitimate IP abused at high rate) with random LookupSeq
#      values per packet. Proves per-IP limiter catches a bad participant even
#      when seq values keep changing (no per-seq bypass).
#
# Tight RL is applied via a temporary systemd drop-in on all three endpoints
# and removed by the EXIT trap regardless of pass/fail.
#
# Pass criterion: bre_rate_limit_drops_total{level="ip"} > 0 on ALL THREE
# retry endpoints.
set -euo pipefail
SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCENARIO_DIR/../lib/common.sh"

: "${PPS:=500}"
: "${DURATION:=15s}"
: "${SEQ_GAP_EVERY:=20}"
: "${SEQ_GAP_SIZE:=2}"
: "${SEQ_GAP_DELAY:=2s}"
: "${RL_IP_RATE:=5}"
: "${RL_IP_BURST:=3}"
: "${RL_SEQUENCE_MAX:=2}"
: "${RL_SEQUENCE_WINDOW:=10s}"
: "${RL_CHAIN_RATE:=1000}"
: "${RL_CHAIN_WINDOW:=10s}"
: "${RL_GROUP_RATE:=1000}"
: "${RL_GROUP_BURST:=500}"
export PPS DURATION SEQ_GAP_EVERY SEQ_GAP_SIZE SEQ_GAP_DELAY

RETRY1_VM=retry1; RETRY1_IP=10.10.10.34; RETRY1_METRICS_PORT=9400
RETRY2_VM=retry2; RETRY2_IP=10.10.10.35; RETRY2_METRICS_PORT=9400
RETRY3_VM=retry3; RETRY3_IP=10.10.10.36; RETRY3_METRICS_PORT=9400

RETRY1_FABRIC=fd20::24; RETRY2_FABRIC=fd20::25; RETRY3_FABRIC=fd20::26
NACK_PORT=9300

BEFORE="$SCENARIO_DIR/metrics.before.tsv"
AFTER="$SCENARIO_DIR/metrics.after.tsv"
R1_BEFORE="$SCENARIO_DIR/retry1.before.tsv"; R1_AFTER="$SCENARIO_DIR/retry1.after.tsv"
R2_BEFORE="$SCENARIO_DIR/retry2.before.tsv"; R2_AFTER="$SCENARIO_DIR/retry2.after.tsv"
R3_BEFORE="$SCENARIO_DIR/retry3.before.tsv"; R3_AFTER="$SCENARIO_DIR/retry3.after.tsv"

RETRY_METRICS=(bre_nack_requests_total bre_retransmits_total
               bre_cache_hits_total bre_cache_misses_total
               bre_rate_limit_drops_total
               bre_responses_sent_total)

ROGUE_PID=""
COMPROMISED_PID=""

# --- Helpers -----------------------------------------------------------------

snapshot_retry() {
  local vm="$1" ip="$2" port="$3" out="$4"
  : > "$out"
  for m in "${RETRY_METRICS[@]}"; do
    printf '%s\t%s\t%s\n' "$vm" "$m" "$(metric_value "$ip:$port" "$m")" >> "$out"
  done
  printf '%s\t%s\t%s\n' "$vm" "bre_rate_limit_drops_total|level=ip" \
    "$(metric_value "$ip:$port" bre_rate_limit_drops_total 'level="ip"')" >> "$out"
  printf '%s\t%s\t%s\n' "$vm" "bre_rate_limit_drops_total|level=sequence" \
    "$(metric_value "$ip:$port" bre_rate_limit_drops_total 'level="sequence"')" >> "$out"
  printf '%s\t%s\t%s\n' "$vm" "bre_rate_limit_drops_total|level=chain" \
    "$(metric_value "$ip:$port" bre_rate_limit_drops_total 'level="chain"')" >> "$out"
  printf '%s\t%s\t%s\n' "$vm" "bre_rate_limit_drops_total|level=group" \
    "$(metric_value "$ip:$port" bre_rate_limit_drops_total 'level="group"')" >> "$out"
  printf '%s\t%s\t%s\n' "$vm" "bre_responses_sent_total|type=ack" \
    "$(metric_value "$ip:$port" bre_responses_sent_total 'type="ack"')" >> "$out"
}

retry_diff() {
  local before="$1" after="$2" metric="$3" b a
  b=$(awk -v m="$metric" -F'\t' '$2==m {print $3}' "$before")
  a=$(awk -v m="$metric" -F'\t' '$2==m {print $3}' "$after")
  echo $(( ${a:-0} - ${b:-0} ))
}

apply_tight_rl() {
  echo "==> Applying tight RL config (IP rate=${RL_IP_RATE} burst=${RL_IP_BURST} seq_max=${RL_SEQUENCE_MAX} window=${RL_SEQUENCE_WINDOW} chain_rate=${RL_CHAIN_RATE} group_rate=${RL_GROUP_RATE})..."
  # NOTE: systemd Environment= drop-ins are overridden by EnvironmentFile= when
  # the file is read at service start. Modify config.env in-place instead.
  local env_file="/etc/bitcoin-retry-endpoint/config.env"
  for vm in "$RETRY1_VM" "$RETRY2_VM" "$RETRY3_VM"; do
    lxc exec "$vm" -- bash -c "
      cp ${env_file} ${env_file}.bak
      sed -i 's|^RL_IP_RATE=.*|RL_IP_RATE=${RL_IP_RATE}|'     ${env_file}
      sed -i 's|^RL_IP_BURST=.*|RL_IP_BURST=${RL_IP_BURST}|'  ${env_file}
      sed -i 's|^RL_SEQUENCE_MAX=.*|RL_SEQUENCE_MAX=${RL_SEQUENCE_MAX}|'   ${env_file}
      sed -i 's|^RL_SEQUENCE_WINDOW=.*|RL_SEQUENCE_WINDOW=${RL_SEQUENCE_WINDOW}|' ${env_file}
      sed -i 's|^RL_CHAIN_RATE=.*|RL_CHAIN_RATE=${RL_CHAIN_RATE}|'         ${env_file}
      sed -i 's|^RL_CHAIN_WINDOW=.*|RL_CHAIN_WINDOW=${RL_CHAIN_WINDOW}|'   ${env_file}
      sed -i 's|^RL_GROUP_RATE=.*|RL_GROUP_RATE=${RL_GROUP_RATE}|'         ${env_file}
      sed -i 's|^RL_GROUP_BURST=.*|RL_GROUP_BURST=${RL_GROUP_BURST}|'      ${env_file}
      systemctl restart bitcoin-retry-endpoint
    "
    echo "     $vm: tight RL applied + restarted"
  done
}

restore_rl() {
  echo "==> Cleanup: stopping flood background jobs..."
  [[ -n "$ROGUE_PID" ]]      && kill "$ROGUE_PID"      2>/dev/null || true
  [[ -n "$COMPROMISED_PID" ]] && kill "$COMPROMISED_PID" 2>/dev/null || true
  echo "==> Cleanup: restoring original config.env and restarting endpoints..."
  local env_file="/etc/bitcoin-retry-endpoint/config.env"
  for vm in "$RETRY1_VM" "$RETRY2_VM" "$RETRY3_VM"; do
    lxc exec "$vm" -- bash -c "
      if [ -f ${env_file}.bak ]; then
        mv ${env_file}.bak ${env_file}
        systemctl restart bitcoin-retry-endpoint
      fi
    " || true
    echo "     $vm: RL config restored"
  done
}
trap restore_rl EXIT

# --- Health check ------------------------------------------------------------

echo "==> Checking endpoint health..."
for spec in "$RETRY1_IP:$RETRY1_METRICS_PORT" \
            "$RETRY2_IP:$RETRY2_METRICS_PORT" \
            "$RETRY3_IP:$RETRY3_METRICS_PORT"; do
  code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 "http://$spec/healthz" || echo 000)
  if [[ "$code" != "200" ]]; then
    echo "FAIL  http://$spec/healthz returned $code — endpoint not ready"
    exit 1
  fi
  echo "     $spec: healthy"
done

echo "==> Checking Python3 availability on source and listener1 VMs..."
for vm in "$SOURCE_VM" listener1; do
  if ! lxc exec "$vm" -- python3 --version >/dev/null 2>&1; then
    echo "FAIL  python3 not found on $vm — install with: lxc exec $vm -- apt-get install -y python3"
    exit 1
  fi
  echo "     $vm: $(lxc exec "$vm" -- python3 --version 2>&1)"
done

# --- Apply tight RL and wait for readiness -----------------------------------

apply_tight_rl

echo "==> Waiting for endpoints to become ready..."
for vm_ep in "$RETRY1_VM $RETRY1_IP:$RETRY1_METRICS_PORT" \
             "$RETRY2_VM $RETRY2_IP:$RETRY2_METRICS_PORT" \
             "$RETRY3_VM $RETRY3_IP:$RETRY3_METRICS_PORT"; do
  vm="${vm_ep%% *}"; ep="${vm_ep##* }"
  for i in $(seq 1 10); do
    code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 3 "http://$ep/readyz" || echo 000)
    if [[ "$code" == "200" ]]; then echo "     $vm: ready"; break; fi
    if [[ "$i" -eq 10 ]]; then echo "FAIL  $vm not ready after 30s"; exit 1; fi
    sleep 3
  done
done

echo "==> Waiting 12s for beacon registry to converge (2 × beacon_interval=5s)..."
sleep 12

# --- Snapshot before ---------------------------------------------------------

echo "==> Snapshot metrics (before)"
snapshot_metrics "$BEFORE"
snapshot_retry "$RETRY1_VM" "$RETRY1_IP" "$RETRY1_METRICS_PORT" "$R1_BEFORE"
snapshot_retry "$RETRY2_VM" "$RETRY2_IP" "$RETRY2_METRICS_PORT" "$R2_BEFORE"
snapshot_retry "$RETRY3_VM" "$RETRY3_IP" "$RETRY3_METRICS_PORT" "$R3_BEFORE"

# --- Launch background flood jobs --------------------------------------------

flood_secs=$(( $(dur_to_seconds "$DURATION") + 25 ))
echo "==> Launching background NACK floods (flood_secs=$flood_secs)..."
echo "    Attack 1 (rogue node):           source VM (fd20::10) → all 3 endpoints, fixed LookupSeq"
echo "    Attack 2 (compromised listener): listener1 VM (fd20::21) → all 3 endpoints, random LookupSeq"

# NACK wire format (24 bytes):
#   [0:4]   Magic      0xE3E1F3E8  (BSV mainnet magic)
#   [4:6]   ProtoVer   0x02BF
#   [6]     MsgType    0x10        (NACK)
#   [7]     LookupType 0x01        (by CurSeq)
#   [8:16]  LookupSeq  uint64 BE
#   [16:24] ChainID    uint64 BE   (0 = orphan/unattributed; non-zero = chain-attributed)
#
# Rogue flood: ChainID=0 (orphan). Tests IP limiter for arbitrary source IPs.
# Compromised flood: random LookupSeq + ChainID=0. Tests IP limiter even
#   when per-sequence window is never exhausted for any individual seq.

rogue_script="
import socket,struct,time
TARGETS=[('${RETRY1_FABRIC}',${NACK_PORT}),('${RETRY2_FABRIC}',${NACK_PORT}),('${RETRY3_FABRIC}',${NACK_PORT})]
sock=socket.socket(socket.AF_INET6,socket.SOCK_DGRAM)
# ChainID=0: orphan gap — bypasses chain limiter, hits IP limiter only
pkt=struct.pack('>IHBBQQ',0xE3E1F3E8,0x02BF,0x10,0x01,0xDEADBEEFCAFEBABE,0)
end=time.time()+${flood_secs}
while time.time()<end:
  [sock.sendto(pkt,a) for a in TARGETS]
"
lxc exec "$SOURCE_VM" -- python3 -c "$rogue_script" &
ROGUE_PID=$!
echo "     rogue flood started [controller pid=$ROGUE_PID]"

compromised_script="
import socket,struct,time,random
TARGETS=[('${RETRY1_FABRIC}',${NACK_PORT}),('${RETRY2_FABRIC}',${NACK_PORT}),('${RETRY3_FABRIC}',${NACK_PORT})]
sock=socket.socket(socket.AF_INET6,socket.SOCK_DGRAM)
# ChainID=0: orphan NACKs. Random LookupSeq proves per-IP limiter fires
# even when no single sequence window is exhausted.
end=time.time()+${flood_secs}
while time.time()<end:
  pkt=struct.pack('>IHBBQQ',0xE3E1F3E8,0x02BF,0x10,0x01,random.randint(0,2**64-1),0)
  [sock.sendto(pkt,a) for a in TARGETS]
"
lxc exec listener1 -- python3 -c "$compromised_script" &
COMPROMISED_PID=$!
echo "     compromised-listener flood started [controller pid=$COMPROMISED_PID]"

# Brief pause so floods have reached the endpoints before the snapshot window.
sleep 1

# --- Run gap-injection generator (legitimate NACK traffic) -------------------

echo "==> Running generator (pps=$PPS duration=$DURATION gap-every=$SEQ_GAP_EVERY gap-size=$SEQ_GAP_SIZE gap-delay=$SEQ_GAP_DELAY)"
gen_output=$(lxc exec "$SOURCE_VM" -- subtx-gen \
  -addr "$PROXY_ADDR" \
  -shard-bits "$SHARD_BITS" \
  -subtrees "$SUBTREES" \
  -subtree-seed "$SUBTREE_SEED" \
  -pps "$PPS" \
  -duration "$DURATION" \
  -payload-size "$PAYLOAD_SIZE" \
  -seq-gap-every "$SEQ_GAP_EVERY" \
  -seq-gap-size "$SEQ_GAP_SIZE" \
  -seq-gap-delay "$SEQ_GAP_DELAY" \
  -log-interval 5s 2>&1)
echo "$gen_output" | tail -5
frames=$(echo "$gen_output" | grep -oP 'sent=\K[0-9]+' | tail -1 || true)
echo "    sent=${frames:-0} frames"

# --- Drain -------------------------------------------------------------------

echo "==> Draining NACK/retransmit pipeline (20s — allows backoff escalation through all 3 endpoints)..."
sleep 20

# --- Snapshot after ----------------------------------------------------------

echo "==> Snapshot metrics (after)"
snapshot_metrics "$AFTER"
snapshot_retry "$RETRY1_VM" "$RETRY1_IP" "$RETRY1_METRICS_PORT" "$R1_AFTER"
snapshot_retry "$RETRY2_VM" "$RETRY2_IP" "$RETRY2_METRICS_PORT" "$R2_AFTER"
snapshot_retry "$RETRY3_VM" "$RETRY3_IP" "$RETRY3_METRICS_PORT" "$R3_AFTER"

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
gaps_unrecovered=$(sum_listener_metric bsl_gaps_unrecovered_total)

r1_nacks=$(retry_diff "$R1_BEFORE" "$R1_AFTER" bre_nack_requests_total)
r2_nacks=$(retry_diff "$R2_BEFORE" "$R2_AFTER" bre_nack_requests_total)
r3_nacks=$(retry_diff "$R3_BEFORE" "$R3_AFTER" bre_nack_requests_total)

r1_drops_ip=$(retry_diff "$R1_BEFORE" "$R1_AFTER" "bre_rate_limit_drops_total|level=ip")
r2_drops_ip=$(retry_diff "$R2_BEFORE" "$R2_AFTER" "bre_rate_limit_drops_total|level=ip")
r3_drops_ip=$(retry_diff "$R3_BEFORE" "$R3_AFTER" "bre_rate_limit_drops_total|level=ip")

r1_drops_seq=$(retry_diff "$R1_BEFORE" "$R1_AFTER" "bre_rate_limit_drops_total|level=sequence")
r2_drops_seq=$(retry_diff "$R2_BEFORE" "$R2_AFTER" "bre_rate_limit_drops_total|level=sequence")
r3_drops_seq=$(retry_diff "$R3_BEFORE" "$R3_AFTER" "bre_rate_limit_drops_total|level=sequence")

r1_drops_chain=$(retry_diff "$R1_BEFORE" "$R1_AFTER" "bre_rate_limit_drops_total|level=chain")
r2_drops_chain=$(retry_diff "$R2_BEFORE" "$R2_AFTER" "bre_rate_limit_drops_total|level=chain")
r3_drops_chain=$(retry_diff "$R3_BEFORE" "$R3_AFTER" "bre_rate_limit_drops_total|level=chain")

r1_drops_group=$(retry_diff "$R1_BEFORE" "$R1_AFTER" "bre_rate_limit_drops_total|level=group")
r2_drops_group=$(retry_diff "$R2_BEFORE" "$R2_AFTER" "bre_rate_limit_drops_total|level=group")
r3_drops_group=$(retry_diff "$R3_BEFORE" "$R3_AFTER" "bre_rate_limit_drops_total|level=group")

cat <<EOF

-- Listener aggregate (l1+l2+l3) --
bsl_gaps_detected_total    = $gaps_detected
bsl_nacks_dispatched_total = $nacks_dispatched
bsl_gaps_unrecovered_total = $gaps_unrecovered

-- retry1 (T0/P128) --
bre_nack_requests_total                       = $r1_nacks
bre_rate_limit_drops_total{level="ip"}        = $r1_drops_ip   [rogue node fd20::10 + compromised listener fd20::21]
bre_rate_limit_drops_total{level="sequence"}  = $r1_drops_seq
bre_rate_limit_drops_total{level="chain"}     = $r1_drops_chain
bre_rate_limit_drops_total{level="group"}     = $r1_drops_group

-- retry2 (T0/P64) --
bre_nack_requests_total                       = $r2_nacks
bre_rate_limit_drops_total{level="ip"}        = $r2_drops_ip
bre_rate_limit_drops_total{level="sequence"}  = $r2_drops_seq
bre_rate_limit_drops_total{level="chain"}     = $r2_drops_chain
bre_rate_limit_drops_total{level="group"}     = $r2_drops_group

-- retry3 (T1/P128) --
bre_nack_requests_total                       = $r3_nacks
bre_rate_limit_drops_total{level="ip"}        = $r3_drops_ip
bre_rate_limit_drops_total{level="sequence"}  = $r3_drops_seq
bre_rate_limit_drops_total{level="chain"}     = $r3_drops_chain
bre_rate_limit_drops_total{level="group"}     = $r3_drops_group
EOF

# --- Assertions --------------------------------------------------------------

SCENARIO_FAIL=${SCENARIO_FAIL:-0}

if [[ "$gaps_detected" -le 0 ]]; then
  echo "FAIL  no gaps detected — gap injection broken?"
  SCENARIO_FAIL=1
else
  echo "PASS  listeners detected $gaps_detected gaps"
fi

if [[ "$nacks_dispatched" -le 0 ]]; then
  echo "FAIL  no NACKs dispatched by listeners"
  SCENARIO_FAIL=1
else
  echo "PASS  listeners dispatched $nacks_dispatched NACKs"
fi

# Core: per-IP rate limiter must fire on all three endpoints.
# The rogue flood (source VM) and compromised-listener flood (listener1) both
# exceed RL_IP_RATE from non-standard / over-quota source IPs, triggering drops.
for pair in "retry1:$r1_drops_ip" "retry2:$r2_drops_ip" "retry3:$r3_drops_ip"; do
  vm="${pair%%:*}"; drops="${pair##*:}"
  if [[ "$drops" -le 0 ]]; then
    echo "FAIL  $vm: rate_limit_drops{level=ip}=0 — per-IP RL did not fire"
    echo "      Check: (a) flood reached endpoint (IPv6 routing on fabric),"
    echo "             (b) RL_IP_RATE=${RL_IP_RATE} is tight enough,"
    echo "             (c) rl-test.conf drop-in was applied correctly."
    SCENARIO_FAIL=1
  else
    echo "PASS  $vm: rate_limit_drops{level=ip}=$drops (per-IP RL fired)"
  fi
done

# Pervasive RL should leave most gaps unrecovered.
if [[ "$gaps_unrecovered" -le 0 ]]; then
  echo "WARN  gaps_unrecovered=0 — RL may not be tight enough or cache hit rate is too high"
else
  echo "PASS  $gaps_unrecovered gaps unrecovered (RL pervasive — expected)"
fi

if [[ "$SCENARIO_FAIL" -ne 0 ]]; then
  echo "Scenario 14: FAIL"
  exit 1
fi
echo "Scenario 14: PASS"
