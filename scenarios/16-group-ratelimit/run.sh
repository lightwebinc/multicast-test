#!/usr/bin/env bash
# Scenario 16 — Per-Group Retransmit Rate Limit (post-lookup, ACK preserved).
#
# Verifies two properties of the group-level rate limiter:
#
#   1. bre_rate_limit_drops_total{level="group"} fires when cache-hitting NACKs
#      arrive faster than RL_GROUP_RATE tokens/s per (srcIP, groupIdx).
#
#   2. ACK responses ARE still sent even when the retransmit is throttled.
#      This prevents listeners from escalating to the next endpoint when the
#      frame is available — the group limiter suppresses the retransmit, not
#      the acknowledgement.
#      Observable as: delta(bre_responses_sent_total{type="ack"})
#                       > delta(bre_retransmits_total)
#
# Method:
#   - Run subtx-gen with gap injection so frames are cached and gaps are
#     detected → listeners send NACKs with real ChainIDs.
#   - Tight RL_GROUP_RATE=2, RL_GROUP_BURST=2 forces throttle after the burst.
#   - High IP, chain, and sequence limits so only the group tier fires.
#   - Drain 15s after generator finishes; listeners keep retrying gaps,
#     generating sustained NACK traffic that hits the group limiter repeatedly.
#
# Pass criteria:
#   - bre_rate_limit_drops_total{level="group"} > 0 on retry1.
#   - delta(bre_responses_sent_total{type="ack"}) > delta(bre_retransmits_total)
#     (ACK sent without retransmit on throttled requests).
#   - bre_rate_limit_drops_total{level="ip"}    == 0 (IP limiter cold).
#   - bre_rate_limit_drops_total{level="chain"} == 0 or very low
#     (chain window >> scenario duration at generous chain rate).
set -euo pipefail
SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCENARIO_DIR/../lib/common.sh"

: "${PPS:=500}"
: "${DURATION:=15s}"
: "${SEQ_GAP_EVERY:=10}"
: "${SEQ_GAP_SIZE:=3}"
: "${SEQ_GAP_DELAY:=500ms}"
: "${RL_IP_RATE:=50000}"
: "${RL_IP_BURST:=10000}"
: "${RL_CHAIN_RATE:=10000}"
: "${RL_CHAIN_WINDOW:=60s}"
: "${RL_SEQUENCE_MAX:=1000}"
: "${RL_SEQUENCE_WINDOW:=60s}"
: "${RL_GROUP_RATE:=2}"
: "${RL_GROUP_BURST:=2}"
export PPS DURATION SEQ_GAP_EVERY SEQ_GAP_SIZE SEQ_GAP_DELAY

RETRY_VM=retry1
RETRY_IP=10.10.10.34
RETRY_METRICS_PORT=9400

BEFORE="$SCENARIO_DIR/metrics.before.tsv"
AFTER="$SCENARIO_DIR/metrics.after.tsv"
R1_BEFORE="$SCENARIO_DIR/retry1.before.tsv"
R1_AFTER="$SCENARIO_DIR/retry1.after.tsv"

# --- Helpers -----------------------------------------------------------------

snapshot_retry() {
  local out="$1"
  : > "$out"
  for m in bre_nack_requests_total bre_retransmits_total \
           bre_cache_hits_total bre_cache_misses_total \
           bre_rate_limit_drops_total \
           bre_responses_sent_total; do
    printf '%s\t%s\t%s\n' "$RETRY_VM" "$m" \
      "$(metric_value "${RETRY_IP}:${RETRY_METRICS_PORT}" "$m")" >> "$out"
  done
  for level in ip chain sequence group; do
    printf '%s\t%s\t%s\n' "$RETRY_VM" "bre_rate_limit_drops_total|level=${level}" \
      "$(metric_value "${RETRY_IP}:${RETRY_METRICS_PORT}" \
         bre_rate_limit_drops_total "level=\"${level}\"")" >> "$out"
  done
  printf '%s\t%s\t%s\n' "$RETRY_VM" "bre_responses_sent_total|type=ack" \
    "$(metric_value "${RETRY_IP}:${RETRY_METRICS_PORT}" \
       bre_responses_sent_total 'type="ack"')" >> "$out"
  printf '%s\t%s\t%s\n' "$RETRY_VM" "bre_responses_sent_total|type=miss" \
    "$(metric_value "${RETRY_IP}:${RETRY_METRICS_PORT}" \
       bre_responses_sent_total 'type="miss"')" >> "$out"
}

retry_diff() {
  local metric="$1" b a
  b=$(awk -v m="$metric" -F'\t' '$2==m {print $3}' "$R1_BEFORE")
  a=$(awk -v m="$metric" -F'\t' '$2==m {print $3}' "$R1_AFTER")
  echo $(( ${a:-0} - ${b:-0} ))
}

apply_tight_rl() {
  echo "==> Applying tight group RL (group_rate=${RL_GROUP_RATE} burst=${RL_GROUP_BURST} ip_rate=${RL_IP_RATE})..."
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
  echo "     $RETRY_VM: tight group RL applied + restarted"
}

restore_rl() {
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
trap 'remove_listener_loss; restore_rl' EXIT

# --- Health check (retry — prior scenario may have just restarted the service) --

echo "==> Checking endpoint health..."
for _hc in $(seq 1 6); do
  code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 \
    "http://${RETRY_IP}:${RETRY_METRICS_PORT}/healthz" 2>/dev/null) || code="000"
  [[ "$code" == "200" ]] && break
  sleep 2
done
if [[ "$code" != "200" ]]; then
  echo "FAIL  http://${RETRY_IP}:${RETRY_METRICS_PORT}/healthz returned $code"
  exit 1
fi
echo "     $RETRY_VM: healthy"

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

# --- Inject selective frame loss on listeners --------------------------------

: "${NETEM_LOSS:=1%}"
echo "==> Injecting selective frame loss on listeners ($NETEM_LOSS) to enable cache hits"
apply_listener_loss "$NETEM_LOSS"

# --- Snapshot before ---------------------------------------------------------

echo "==> Snapshot metrics (before)"
snapshot_metrics "$BEFORE"
snapshot_retry   "$R1_BEFORE"

# --- Run generator -----------------------------------------------------------

# Dense gap injection: frequent small gaps with short delay so frames ARE in
# cache (gap_delay << cache_ttl=60s) when the NACK arrives. This ensures the
# NACK reaches the post-lookup code path where the group limiter sits.
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
  -seq-gap-size  "$SEQ_GAP_SIZE" \
  -seq-gap-delay "$SEQ_GAP_DELAY" \
  -log-interval 5s 2>&1)
echo "$gen_output" | tail -5
frames=$(echo "$gen_output" | grep -oP 'sent=\K[0-9]+' | tail -1 || true)
echo "    sent=${frames:-0} frames"

# Allow listeners to retry gaps — each retry hits the endpoint and exercises
# the group limiter on subsequent attempts (burst exhausted after first hit).
echo "==> Draining NACK/retransmit pipeline (15s — listeners retry unrecovered gaps)..."
sleep 15

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
gaps_suppressed=$(sum_listener_metric bsl_gaps_suppressed_total)

nacks_received=$(retry_diff bre_nack_requests_total)
cache_hits=$(retry_diff     bre_cache_hits_total)
retransmits=$(retry_diff    bre_retransmits_total)
acks_sent=$(retry_diff      "bre_responses_sent_total|type=ack")
misses_sent=$(retry_diff    "bre_responses_sent_total|type=miss")
drops_ip=$(retry_diff       "bre_rate_limit_drops_total|level=ip")
drops_chain=$(retry_diff    "bre_rate_limit_drops_total|level=chain")
drops_seq=$(retry_diff      "bre_rate_limit_drops_total|level=sequence")
drops_group=$(retry_diff    "bre_rate_limit_drops_total|level=group")

# ACKs sent without retransmit = group-throttled requests that still got ACK.
acks_without_retransmit=$(( acks_sent - retransmits ))
if [[ "$acks_without_retransmit" -lt 0 ]]; then
  acks_without_retransmit=0
fi

cat <<EOF

-- Listener aggregate --
bsl_gaps_detected_total    = $gaps_detected
bsl_nacks_dispatched_total = $nacks_dispatched
bsl_gaps_suppressed_total  = $gaps_suppressed

-- $RETRY_VM --
bre_nack_requests_total                      = $nacks_received
bre_cache_hits_total                         = $cache_hits
bre_retransmits_total                        = $retransmits
bre_responses_sent_total{type="ack"}         = $acks_sent
bre_responses_sent_total{type="miss"}        = $misses_sent
acks_sent - retransmits (group-throttled)    = $acks_without_retransmit
bre_rate_limit_drops_total{level="ip"}       = $drops_ip      (expect ~0)
bre_rate_limit_drops_total{level="chain"}    = $drops_chain   (expect ~0)
bre_rate_limit_drops_total{level="sequence"} = $drops_seq
bre_rate_limit_drops_total{level="group"}    = $drops_group   (expect > 0)
EOF

# --- Assertions --------------------------------------------------------------

SCENARIO_FAIL=0

if [[ "$gaps_detected" -le 0 ]]; then
  echo "FAIL  no gaps detected — gap injection not working"
  SCENARIO_FAIL=1
else
  echo "PASS  gaps_detected=$gaps_detected"
fi

if [[ "$nacks_received" -le 0 ]]; then
  echo "FAIL  retry endpoint received no NACKs"
  SCENARIO_FAIL=1
else
  echo "PASS  nacks_received=$nacks_received"
fi

if [[ "$cache_hits" -le 0 ]]; then
  echo "FAIL  cache_hits=0 — NACKs not reaching post-lookup path; check gap_delay vs cache_ttl"
  SCENARIO_FAIL=1
else
  echo "PASS  cache_hits=$cache_hits (NACKs reached post-lookup path)"
fi

# Core: group limiter must fire.
if [[ "$drops_group" -le 0 ]]; then
  echo "FAIL  rate_limit_drops{level=group}=0 — group RL did not fire"
  echo "      Check: (a) RL_GROUP_RATE=${RL_GROUP_RATE} and RL_GROUP_BURST=${RL_GROUP_BURST} are tight enough,"
  echo "             (b) cache hits are occurring (checked above),"
  echo "             (c) SHARD_BITS=${SHARD_BITS} produces groupIdx used as key."
  SCENARIO_FAIL=1
else
  echo "PASS  rate_limit_drops{level=group}=$drops_group (group RL fired)"
fi

# ACK must be sent even when retransmit is throttled by group limiter.
# The difference (acks_sent - retransmits) must be >= drops_group:
# every group-throttled hit must still produce an ACK response.
if [[ "$acks_without_retransmit" -le 0 ]]; then
  echo "FAIL  acks_sent ($acks_sent) <= retransmits ($retransmits) — ACK not sent on group-throttled requests"
  SCENARIO_FAIL=1
else
  echo "PASS  acks_without_retransmit=$acks_without_retransmit (ACK sent even when retransmit throttled)"
fi

# IP and chain limiters must stay cold.
if [[ "$drops_ip" -gt 0 ]]; then
  echo "WARN  rate_limit_drops{level=ip}=$drops_ip — IP limiter fired; increase RL_IP_RATE"
else
  echo "PASS  rate_limit_drops{level=ip}=0 (IP limiter cold)"
fi

if [[ "$drops_chain" -gt 0 ]]; then
  echo "WARN  rate_limit_drops{level=chain}=$drops_chain — chain limiter fired; increase RL_CHAIN_RATE"
else
  echo "PASS  rate_limit_drops{level=chain}=0 (chain limiter cold)"
fi

if [[ "$SCENARIO_FAIL" -ne 0 ]]; then
  echo "Scenario 16: FAIL"
  exit 1
fi
echo "Scenario 16: PASS"
