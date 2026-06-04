#!/usr/bin/env bash
# Shared helpers for scenarios.
# Source this from a scenario's run.sh:
#
#   SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
#   source "$SCENARIO_DIR/../lib/common.sh"

set -euo pipefail

# Defaults — override via env.
: "${PROXY_VM:=proxy}"
: "${PROXY_ADDR:=[2001:db8:ffff::1]:9000}"
: "${PROXY_TCP_ADDR:=[2001:db8:ffff::1]:9002}"
: "${SOURCE_VM:=source}"
: "${LISTENER_PORT:=9001}"
: "${METRICS_PORT:=9200}"
: "${SHARD_BITS:=2}"
: "${SUBTREES:=8}"
: "${SUBTREE_SEED:=multicast-lab-bsv}"
: "${PPS:=1000}"
: "${DURATION:=10s}"
: "${PAYLOAD_SIZE:=256}"
: "${PAYLOAD_FORMAT:=brc124}"  # brc124 | brc128 | mixed (subtx-gen -payload-format)
: "${CORRUPT_TXID_RATE:=0}"    # percentage of frames to corrupt TxID (0-100)

LISTENERS=(listener1 listener2 listener3)
LISTENER_IPS=(10.10.10.31 10.10.10.32 10.10.10.33)

# --- Metric helpers -------------------------------------------------------

# metric_value <ip:port> <metric_name> [label_filter_substring]
# Prints the sum of all matching samples (0 if none found). Label filter
# is a simple grep on the raw metric line, e.g. 'reason="shard_filter"'.
metric_value() {
  local endpoint="$1" name="$2" filter="${3:-}"
  local raw
  raw=$(curl -s --max-time 3 "http://$endpoint/metrics" || true)
  if [[ -z "$raw" ]]; then
    echo 0
    return
  fi
  if [[ -n "$filter" ]]; then
    awk -v n="$name" -v f="$filter" '
      $0 !~ /^#/ && index($0, n) == 1 && index($0, f) { v += $NF }
      END { printf "%.0f\n", v }
    ' <<<"$raw"
  else
    awk -v n="$name" '
      $0 !~ /^#/ && index($0, n) == 1 { v += $NF }
      END { printf "%.0f\n", v }
    ' <<<"$raw"
  fi
}

# Take a snapshot of all counter metrics we care about into
# a tab-separated file: <host>\t<metric>\t<value>.
snapshot_metrics() {
  local outfile="$1"
  : > "$outfile"
  for i in "${!LISTENERS[@]}"; do
    local host="${LISTENERS[$i]}"
    local ip="${LISTENER_IPS[$i]}"
    for m in bsl_frames_received_total 'bsl_frames_forwarded_total|proto="udp"' bsl_egress_errors_total \
             bsl_mc_egress_errors_total \
             'bsl_frames_dropped_total|shard_filter' \
             'bsl_frames_dropped_total|subtree_exclude' \
             'bsl_frames_dropped_total|subtree_include_miss' \
             'bsl_frames_dropped_total|bad_frame' \
             bsl_frames_invalid_payload_total \
             bsl_reassembly_started_total bsl_reassembly_completed_total \
             bsl_reassembly_abandoned_total bsl_reassembly_hash_mismatch_total \
             bsl_gaps_detected_total bsl_gaps_suppressed_total \
             bsl_nacks_dispatched_total bsl_gaps_unrecovered_total \
             'bsl_gaps_detected_total|flow="brc131"' \
             'bsl_gaps_unrecovered_total|flow="brc131"' \
             'bsl_nacks_dispatched_total|flow="brc131"' \
             bsl_subtree_group_announces_received_total bsl_subtree_group_evictions_total \
             'bsl_frames_received_total|version="brc131"' \
             'bsl_frames_received_total|version="brc132"' \
             'bsl_frames_received_total|version="brc132_reassembled"' \
             'bsl_gaps_detected_total|flow="brc132"' \
             'bsl_gaps_unrecovered_total|flow="brc132"' \
             'bsl_nacks_dispatched_total|flow="brc132"' \
             'bsl_frames_received_total|version="brc134"' \
             'bsl_gaps_detected_total|flow="brc134"' \
             'bsl_gaps_unrecovered_total|flow="brc134"' \
             'bsl_nacks_dispatched_total|flow="brc134"' \
             bsl_header_forwarded_total \
             bsl_header_egress_errors_total \
             bsl_frames_tx_deduped_total \
             bsl_txid_dedup_errors_total; do
      local name="${m%%|*}"
      local filter=""
      if [[ "$m" == *'|'* ]]; then
        local _raw_filter="${m##*|}"
        if [[ "$_raw_filter" == *=* ]]; then
          filter="$_raw_filter"
        else
          filter="reason=\"$_raw_filter\""
        fi
      fi
      local v
      v=$(metric_value "$ip:$METRICS_PORT" "$name" "$filter")
      printf '%s\t%s\t%s\n' "$host" "$m" "$v" >> "$outfile"
    done
  done
}

# diff_metric <before.tsv> <after.tsv> <host> <metric-with-optional-filter>
diff_metric() {
  local before="$1" after="$2" host="$3" metric="$4"
  local b a delta
  b=$(awk -v h="$host" -v m="$metric" -F'\t' '$1==h && $2==m {print $3}' "$before")
  a=$(awk -v h="$host" -v m="$metric" -F'\t' '$1==h && $2==m {print $3}' "$after")
  delta=$(( ${a:-0} - ${b:-0} ))
  # Handle 64-bit unsigned counter wrap-around (Prometheus counters)
  if (( delta < 0 )); then
    delta=$(( delta + 18446744073709551616 ))  # 2^64
  fi
  echo "$delta"
}

# assert_near <label> <got> <expected> <tolerance_fraction>
# Fails the scenario if |got-expected|/max(expected,1) > tolerance.
assert_near() {
  local label="$1" got="$2" expected="$3" tol="$4"
  if [[ "$expected" -le 0 ]]; then
    if [[ "$got" -eq 0 ]]; then
      echo "PASS  $label: got 0 (expected 0)"
      return 0
    fi
    echo "FAIL  $label: got $got (expected 0)"
    SCENARIO_FAIL=1
    return 1
  fi
  local diff=$(( got - expected ))
  diff=${diff#-}
  local limit
  limit=$(awk -v e="$expected" -v t="$tol" 'BEGIN{printf "%.0f", e*t}')
  if (( diff <= limit )); then
    echo "PASS  $label: got $got expected~$expected (tol=$tol, diff=$diff <= $limit)"
  else
    echo "FAIL  $label: got $got expected~$expected (tol=$tol, diff=$diff > $limit)"
    SCENARIO_FAIL=1
  fi
}

# Fire the subtx-gen inside the source VM.
# Prints ONLY the expected frame-count to stdout so callers can do
# `frames=$(run_generator)`; all log output goes to stderr.
run_generator() {
  local theoretical=$(( PPS * $(dur_to_seconds "$DURATION") ))
  local gen_output
  echo "-- generator: pps=$PPS duration=$DURATION payload=$PAYLOAD_FORMAT corrupt-txid-rate=$CORRUPT_TXID_RATE -> ~$theoretical frames --" >&2
  gen_output=$(lxc exec "$SOURCE_VM" -- subtx-gen \
    -addr "$PROXY_ADDR" \
    -shard-bits "$SHARD_BITS" \
    -subtrees "$SUBTREES" \
    -subtree-seed "$SUBTREE_SEED" \
    -pps "$PPS" \
    -duration "$DURATION" \
    -payload-size "$PAYLOAD_SIZE" \
    -payload-format "$PAYLOAD_FORMAT" \
    -corrupt-txid-rate "$CORRUPT_TXID_RATE" \
    -log-interval 2s 2>&1)
  echo "$gen_output" >&2
  # Return the actual sent count so tolerance checks measure delivery ratio
  # against frames that were actually transmitted, not a theoretical target.
  local actual
  actual=$(echo "$gen_output" | grep -oP 'sent=\K[0-9]+' | tail -1)
  echo "${actual:-$theoretical}"
}

dur_to_seconds() {
  local d="$1"
  # Accept 10s, 1m, 500ms → seconds (rounded).
  if [[ "$d" =~ ^([0-9]+)s$ ]]; then
    echo "${BASH_REMATCH[1]}"
  elif [[ "$d" =~ ^([0-9]+)m$ ]]; then
    echo $(( ${BASH_REMATCH[1]} * 60 ))
  elif [[ "$d" =~ ^([0-9]+)ms$ ]]; then
    echo $(( ${BASH_REMATCH[1]} / 1000 ))
  else
    echo 10
  fi
}

SCENARIO_FAIL=0

# --- Selective packet-loss helpers (for NACK/retransmit scenarios) -----------
#
# These inject a random per-packet drop rule inside each LISTENER VM on the
# fabric interface (enp6s0), port 9001 (multicast frames). Retry endpoints are
# NOT affected, so they accumulate frames that listeners miss — creating the
# selective delivery needed for cache-hit / retransmit testing.
#
# The nftables table is isolated (inet listener-infra-test) and is fully
# removed on cleanup regardless of scenario outcome.
#
# Usage:
#   apply_listener_loss 1%    # call before generator; 1% = 1 drop per 100
#   trap remove_listener_loss EXIT

_LOSS_VMS=()

apply_listener_loss() {
  local pct="${1:-1%}"
  # Convert "N%" → threshold for "numgen random mod 1000 < T" (0.1% resolution).
  local num="${pct%%%}"           # strip trailing %
  local threshold=$(( num * 10 )) # 1% → 10, 2% → 20, etc.
  _LOSS_VMS=()
  for vm in "${LISTENERS[@]}"; do
    lxc exec "$vm" -- bash -c "
      nft add table inet listener-infra-test 2>/dev/null || true
      nft add chain inet listener-infra-test input \
        '{ type filter hook input priority -100; }' 2>/dev/null || true
      nft flush chain inet listener-infra-test input 2>/dev/null || true
      nft add rule inet listener-infra-test input \
        iif \"enp6s0\" udp dport 9001 \
        numgen random mod 1000 lt ${threshold} counter drop
    " 2>/dev/null && _LOSS_VMS+=("$vm") || \
      echo "WARN  could not apply loss rule on $vm (nft unavailable?)"
    # Verify the rule is actually in place.
    local rule_count
    rule_count=$(lxc exec "$vm" -- nft list chain inet listener-infra-test input 2>/dev/null \
      | grep -c "counter" || true)
    if [[ "${rule_count:-0}" -lt 1 ]]; then
      echo "WARN  loss rule NOT confirmed on $vm"
    fi
    echo "     loss=${pct} injected on $vm (enp6s0 port 9001)"
  done
}

check_listener_loss_counters() {
  for vm in "${_LOSS_VMS[@]+"${_LOSS_VMS[@]}"}"; do
    local cnt
    cnt=$(lxc exec "$vm" -- nft list chain inet listener-infra-test input 2>/dev/null \
      | grep -oP 'packets \K[0-9]+' || echo "0")
    echo "     [nft] $vm dropped $cnt packets"
  done
}

remove_listener_loss() {
  for vm in "${_LOSS_VMS[@]+"${_LOSS_VMS[@]}"}"; do
    lxc exec "$vm" -- nft delete table inet listener-infra-test 2>/dev/null || true
  done
  _LOSS_VMS=()
}

# --- TxID dedup (Redis) helpers -------------------------------------------

: "${TXID_DEDUP_ADDR:=10.10.10.40:6379}"
: "${TXID_DEDUP_PREFIX:=bsl:txid:}"
: "${TXID_DEDUP_TTL:=60s}"

LISTENER_ENV_FILE="/etc/shard-listener/config.env"

# enable_txid_dedup <vm>: inject TXID_DEDUP_ADDR/PREFIX/TTL into a single
# listener VM's config.env, saving a .bak, and restart the service.
enable_txid_dedup() {
  local vm="$1"
  # The deployed listener firewall denies outbound TCP except DNS/HTTP/HTTPS.
  # Without opening the Redis port, txdedup.New times out at startup and the
  # listener service exits, so we must open the firewall before restart.
  allow_redis_egress "$vm"
  lxc exec "$vm" -- bash -c "
    # Preserve original config: only create .bak if not already present
    # (a stale .bak from a failed prior run already holds the original).
    if [ ! -f ${LISTENER_ENV_FILE}.txdedup.bak ]; then
      cp ${LISTENER_ENV_FILE} ${LISTENER_ENV_FILE}.txdedup.bak
    fi
    sed -i '/^TXID_DEDUP_ADDR=/d; /^TXID_DEDUP_PREFIX=/d; /^TXID_DEDUP_TTL=/d' ${LISTENER_ENV_FILE}
    printf 'TXID_DEDUP_ADDR=${TXID_DEDUP_ADDR}\nTXID_DEDUP_PREFIX=${TXID_DEDUP_PREFIX}\nTXID_DEDUP_TTL=${TXID_DEDUP_TTL}\n' >> ${LISTENER_ENV_FILE}
    systemctl restart shard-listener
  "
  echo "     $vm txid-dedup enabled (redis=$TXID_DEDUP_ADDR)"
}

# allow_redis_egress <vm>: insert an nft rule at the top of the
# `inet listener-infra output` chain permitting outbound TCP to
# TXID_DEDUP_ADDR. The deployed listener firewall has policy=drop on output
# with only DNS/HTTP/HTTPS allowed; without this, the Redis TCP SYN is dropped
# and txdedup.New times out causing the listener to fail to start.
#
# Idempotent: removes any prior rule with the same comment before inserting.
allow_redis_egress() {
  local vm="$1"
  local redis_ip="${TXID_DEDUP_ADDR%%:*}"
  local redis_port="${TXID_DEDUP_ADDR##*:}"
  lxc exec "$vm" -- bash -c "
    # Remove any prior rule with our marker handle (idempotent).
    handle=\$(nft -a list chain inet listener-infra output 2>/dev/null \
      | awk '/txdedup-redis-allow/ { for (i=1;i<=NF;i++) if (\$i==\"handle\") print \$(i+1) }')
    if [ -n \"\$handle\" ]; then
      nft delete rule inet listener-infra output handle \$handle 2>/dev/null || true
    fi
    # Insert at top so it runs before the trailing 'counter drop'.
    nft insert rule inet listener-infra output \
      ip daddr ${redis_ip} tcp dport ${redis_port} accept comment '\"txdedup-redis-allow\"'
  "
  echo "     $vm redis-egress allowed (${TXID_DEDUP_ADDR})"
}

revoke_redis_egress() {
  local vm="$1"
  lxc exec "$vm" -- bash -c "
    handle=\$(nft -a list chain inet listener-infra output 2>/dev/null \
      | awk '/txdedup-redis-allow/ { for (i=1;i<=NF;i++) if (\$i==\"handle\") print \$(i+1) }')
    if [ -n \"\$handle\" ]; then
      nft delete rule inet listener-infra output handle \$handle 2>/dev/null || true
    fi
  " || true
}

allow_redis_egress_all() {
  for vm in "${LISTENERS[@]}"; do
    allow_redis_egress "$vm"
  done
}

revoke_redis_egress_all() {
  for vm in "${LISTENERS[@]}"; do
    revoke_redis_egress "$vm"
  done
}

# enable_txid_dedup_all: enable on all 3 listener VMs (firewall opened
# per-VM inside enable_txid_dedup).
enable_txid_dedup_all() {
  for vm in "${LISTENERS[@]}"; do
    enable_txid_dedup "$vm"
  done
  sleep 3  # allow workers to reconnect to Redis and start serving
}

# restore_txid_dedup <vm>: restore config.env from .bak, restart, and
# revoke the Redis firewall allow rule.
restore_txid_dedup() {
  local vm="$1"
  lxc exec "$vm" -- bash -c "
    if [ -f ${LISTENER_ENV_FILE}.txdedup.bak ]; then
      mv ${LISTENER_ENV_FILE}.txdedup.bak ${LISTENER_ENV_FILE}
      systemctl restart shard-listener
    fi
  " || true
  revoke_redis_egress "$vm"
}

# restore_txid_dedup_all: restore all 3 listener VMs (config + firewall).
restore_txid_dedup_all() {
  for vm in "${LISTENERS[@]}"; do
    restore_txid_dedup "$vm"
  done
}

# flush_txid_dedup_keys: delete all bsl:txid:* keys from Redis so each
# scenario starts with a clean dedup state.
flush_txid_dedup_keys() {
  lxc exec redis -- bash -c "
    redis-cli --no-auth-warning --scan --pattern '${TXID_DEDUP_PREFIX}*' \
      | xargs -r redis-cli --no-auth-warning del
  " 2>/dev/null || true
  echo "     Redis: flushed ${TXID_DEDUP_PREFIX}* keys"
}

# --- Proxy configuration helpers ------------------------------------------

# All proxy VMs that must be configured identically.
PROXY_VMS=(proxy proxy2)
PROXY_ENV_FILE="/etc/shard-proxy/config.env"

# enable_frag_all <mtu>: set FRAG_MTU on all proxy VMs and restart.
enable_frag_all() {
  local mtu="${1:?usage: enable_frag_all <mtu>}"
  for vm in "${PROXY_VMS[@]}"; do
    lxc exec "$vm" -- bash -c "
      cp ${PROXY_ENV_FILE} ${PROXY_ENV_FILE}.bak
      if grep -q '^FRAG_MTU=' ${PROXY_ENV_FILE}; then
        sed -i 's|^FRAG_MTU=.*|FRAG_MTU=${mtu}|' ${PROXY_ENV_FILE}
      else
        echo 'FRAG_MTU=${mtu}' >> ${PROXY_ENV_FILE}
      fi
      systemctl restart shard-proxy
    "
    echo "     $vm restarted with FRAG_MTU=$mtu"
  done
  sleep 3
}

# restore_frag_all: restore all proxy VMs config from .bak and restart.
restore_frag_all() {
  for vm in "${PROXY_VMS[@]}"; do
    lxc exec "$vm" -- bash -c "
      if [ -f ${PROXY_ENV_FILE}.bak ]; then
        mv ${PROXY_ENV_FILE}.bak ${PROXY_ENV_FILE}
        systemctl restart shard-proxy
      fi
    " || true
  done
}

# --- Retry endpoint multi-instance helpers --------------------------------

RETRY_VMS=(retry1 retry2 retry3)
RETRY_IPS=(10.10.10.34 10.10.10.35 10.10.10.36)
: "${RETRY_METRICS_PORT:=9400}"

# Snapshot metrics from all retry endpoints into a TSV:
# <vm>\t<metric>\t<value>
snapshot_all_retry() {
  local out="$1"
  : > "$out"
  for i in "${!RETRY_VMS[@]}"; do
    local vm="${RETRY_VMS[$i]}" ip="${RETRY_IPS[$i]}"
    for m in bre_frames_received_total bre_frames_cached_total bre_frames_dropped_total \
             bre_nack_requests_total bre_retransmits_total bre_retransmit_dedup_total \
             bre_cache_hits_total bre_cache_misses_total \
             bre_rate_limit_drops_total; do
      printf '%s\t%s\t%s\n' "$vm" "$m" \
        "$(metric_value "$ip:$RETRY_METRICS_PORT" "$m")" >> "$out"
    done
  done
}

# Sum a metric diff across all retry endpoints.
retry_diff_all() {
  local before="$1" after="$2" metric="$3" total=0
  local b a
  for vm in "${RETRY_VMS[@]}"; do
    b=$(awk -v v="$vm" -v m="$metric" -F'\t' '$1==v && $2==m {print $3}' "$before")
    a=$(awk -v v="$vm" -v m="$metric" -F'\t' '$1==v && $2==m {print $3}' "$after")
    total=$(( total + ${a:-0} - ${b:-0} ))
  done
  echo "$total"
}
