#!/usr/bin/env bash
# Shared helpers for scenarios.
# Source this from a scenario's run.sh:
#
#   SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
#   source "$SCENARIO_DIR/../lib/common.sh"

set -euo pipefail

# Defaults — override via env.
: "${PROXY_VM:=proxy}"
: "${PROXY_ADDR:=[fd20::2]:9000}"
: "${SOURCE_VM:=source}"
: "${LISTENER_PORT:=9001}"
: "${METRICS_PORT:=9200}"
: "${SHARD_BITS:=2}"
: "${SUBTREES:=8}"
: "${SUBTREE_SEED:=lax-lab-2026}"
: "${PPS:=1000}"
: "${DURATION:=10s}"
: "${PAYLOAD_SIZE:=256}"
: "${PAYLOAD_FORMAT:=brc124}"  # brc124 | brc128 | mixed (subtx-gen -payload-format)

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
             bsl_gaps_detected_total bsl_gaps_suppressed_total \
             bsl_nacks_dispatched_total bsl_gaps_unrecovered_total \
             bsl_subtree_announces_received_total bsl_subtree_group_evictions_total; do
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
  local b a
  b=$(awk -v h="$host" -v m="$metric" -F'\t' '$1==h && $2==m {print $3}' "$before")
  a=$(awk -v h="$host" -v m="$metric" -F'\t' '$1==h && $2==m {print $3}' "$after")
  echo $(( ${a:-0} - ${b:-0} ))
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
  echo "-- generator: pps=$PPS duration=$DURATION payload=$PAYLOAD_FORMAT -> ~$theoretical frames --" >&2
  gen_output=$(lxc exec "$SOURCE_VM" -- subtx-gen \
    -addr "$PROXY_ADDR" \
    -shard-bits "$SHARD_BITS" \
    -subtrees "$SUBTREES" \
    -subtree-seed "$SUBTREE_SEED" \
    -pps "$PPS" \
    -duration "$DURATION" \
    -payload-size "$PAYLOAD_SIZE" \
    -payload-format "$PAYLOAD_FORMAT" \
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
# The nftables table is isolated (inet bitcoin-listener-test) and is fully
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
      nft add table inet bitcoin-listener-test 2>/dev/null || true
      nft add chain inet bitcoin-listener-test input \
        '{ type filter hook input priority -100; }' 2>/dev/null || true
      nft flush chain inet bitcoin-listener-test input 2>/dev/null || true
      nft add rule inet bitcoin-listener-test input \
        iif \"enp6s0\" udp dport 9001 \
        numgen random mod 1000 lt ${threshold} drop
    " 2>/dev/null && _LOSS_VMS+=("$vm") || \
      echo "WARN  could not apply loss rule on $vm (nft unavailable?)"
    echo "     loss=${pct} injected on $vm (enp6s0 port 9001)"
  done
}

remove_listener_loss() {
  for vm in "${_LOSS_VMS[@]+"${_LOSS_VMS[@]}"}"; do
    lxc exec "$vm" -- nft delete table inet bitcoin-listener-test 2>/dev/null || true
  done
  _LOSS_VMS=()
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
