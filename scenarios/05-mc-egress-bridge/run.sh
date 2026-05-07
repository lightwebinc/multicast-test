#!/usr/bin/env bash
# Scenario 05 — Multicast Egress Bridge (Group Re-mapping)
#
# Verifies listener1 re-emits frames from ff05::0-3 (site-local) onto
# ff02::0-3 (link-local) and that listener4 receives and forwards them.
#
# Preconditions:
#   listener1 mgmt=10.10.10.31  mc_egress_enabled=true  mc_egress_scope=link
#   listener4 mgmt=10.10.10.37  mc_scope=link  beacon_enabled=false
#   New binary deployed to all listener VMs via build+push.
set -euo pipefail
SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCENARIO_DIR/../lib/common.sh"

L1_IP=10.10.10.31
L4_IP=10.10.10.37
METRICS_PORT=9200

BEFORE="$SCENARIO_DIR/metrics.before.tsv"
AFTER="$SCENARIO_DIR/metrics.after.tsv"
L4_BEFORE="$SCENARIO_DIR/listener4.before.tsv"
L4_AFTER="$SCENARIO_DIR/listener4.after.tsv"

# snapshot_l4 <outfile>
# Snapshot the subset of metrics relevant to listener4.
snapshot_l4() {
  local outfile="$1"
  : > "$outfile"
  for m in bsl_frames_received_total bsl_frames_forwarded_total \
            bsl_egress_errors_total bsl_mc_egress_errors_total \
            'bsl_frames_dropped_total|bad_frame'; do
    local name="${m%%|*}"
    local filter=""
    if [[ "$m" == *'|'* ]]; then
      filter="reason=\"${m##*|}\""
    fi
    local v
    v=$(metric_value "$L4_IP:$METRICS_PORT" "$name" "$filter")
    printf 'listener4\t%s\t%s\n' "$m" "$v" >> "$outfile"
  done
}

diff_l4() {
  local before="$1" after="$2" metric="$3"
  local b a
  b=$(awk -v m="$metric" -F'\t' '$1=="listener4" && $2==m {print $3}' "$before")
  a=$(awk -v m="$metric" -F'\t' '$1=="listener4" && $2==m {print $3}' "$after")
  echo $(( ${a:-0} - ${b:-0} ))
}

# --- Ensure UDP sink is running on listener4:9100 --------------------------
# Without a listener on 9100, ICMP port-unreachable causes alternating
# ECONNREFUSED on the egress socket (every other send fails).
echo "==> Ensuring UDP sink on listener4:9100..."
lxc exec listener4 -- bash -c '
  ss -ulnp sport = :9100 | grep -q 9100 && echo "sink already running" && exit 0
  systemd-run --unit=udp-sink-9100 --description="UDP sink port 9100" python3 -c "
import socket
s=socket.socket(socket.AF_INET6,socket.SOCK_DGRAM)
s.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1)
s.bind((\"\",9100))
while True:
    s.recv(65536)
"
  sleep 1
  ss -ulnp sport = :9100 | grep -q 9100 && echo "sink started" || echo "WARN: sink did not start"
'

# --- Verify listener4 health -----------------------------------------------
echo "==> Checking listener4 health..."
for i in $(seq 1 20); do
  code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 3 \
    "http://$L4_IP:$METRICS_PORT/readyz" || echo 000)
  if [[ "$code" == "200" ]]; then
    echo "     listener4: ready"
    break
  fi
  if [[ "$i" -eq 20 ]]; then
    echo "FAIL  listener4 did not become ready within 60s (last HTTP code: $code)"
    exit 1
  fi
  sleep 3
done

# --- Wait for MLD snooping convergence ------------------------------------
# Allow the bridge MDB to populate listener4's ff02:: group membership
# before frames start flowing. Two MLD query cycles at the default interval
# of ~125s is overkill; 5s is sufficient on lxdbr1 where the querier fires
# every 10s by default and reports arrive within one cycle.
echo "==> Waiting 5s for MLD snooping convergence on lxdbr1..."
sleep 5

# --- Snapshot before -------------------------------------------------------
echo "==> Snapshot metrics (before)"
snapshot_metrics "$BEFORE"
snapshot_l4 "$L4_BEFORE"

# Capture proto-specific forwarded values separately (TSV stores unfiltered totals).
l1_mcast_fwd_b=$(metric_value   "$L1_IP:$METRICS_PORT" bsl_frames_forwarded_total 'proto="udp-mcast"')
l1_uni_fwd_b=$(metric_value     "$L1_IP:$METRICS_PORT" bsl_frames_forwarded_total 'proto="udp"')
l1_received_b=$(metric_value    "$L1_IP:$METRICS_PORT" bsl_frames_received_total)

# --- Run generator ---------------------------------------------------------
frames=$(run_generator)

# --- Drain -----------------------------------------------------------------
echo "==> Allow egress pipelines to drain (3s)..."
sleep 3

# --- Snapshot after --------------------------------------------------------
echo "==> Snapshot metrics (after)"
snapshot_metrics "$AFTER"
snapshot_l4 "$L4_AFTER"

# --- Compute diffs ---------------------------------------------------------
# listener1: proto-filtered forwarded (mc-egress path).
l1_mcast_fwd_a=$(metric_value "$L1_IP:$METRICS_PORT" bsl_frames_forwarded_total 'proto="udp-mcast"')
l1_mcast_fwd=$(( ${l1_mcast_fwd_a:-0} - ${l1_mcast_fwd_b:-0} ))

# listener1: unicast forwarded (must also be ~received — mc-egress is additive).
l1_uni_fwd_a=$(metric_value  "$L1_IP:$METRICS_PORT" bsl_frames_forwarded_total 'proto="udp"')
l1_uni_fwd=$(( ${l1_uni_fwd_a:-0} - ${l1_uni_fwd_b:-0} ))
l1_received_a=$(metric_value "$L1_IP:$METRICS_PORT" bsl_frames_received_total)
l1_received=$(( ${l1_received_a:-0} - ${l1_received_b:-0} ))

# listener1: mc-egress errors (must be zero).
l1_mc_errors=$(diff_metric "$BEFORE" "$AFTER" listener1 bsl_mc_egress_errors_total)

# listener4: received + forwarded.
l4_received=$(diff_l4 "$L4_BEFORE" "$L4_AFTER" bsl_frames_received_total)
l4_forwarded=$(diff_l4 "$L4_BEFORE" "$L4_AFTER" bsl_frames_forwarded_total)

cat <<EOF

-- listener1 (re-emitter) --
bsl_frames_received_total                     = $l1_received
bsl_frames_forwarded_total{proto="udp-mcast"} = $l1_mcast_fwd
bsl_frames_forwarded_total{proto="udp"}       = $l1_uni_fwd
bsl_mc_egress_errors_total                    = $l1_mc_errors

-- listener4 (egress domain consumer, ff02::) --
bsl_frames_received_total  = $l4_received
bsl_frames_forwarded_total = $l4_forwarded
EOF

# --- Assertions ------------------------------------------------------------
SCENARIO_FAIL=${SCENARIO_FAIL:-0}

# listener1 unicast forwarded (regression guard — mc-egress must not disrupt existing path).
assert_near "l1 unicast forwarded" "$l1_uni_fwd" "$l1_received" 0.05

# listener1 mc-egress forwarded.
assert_near "l1 mc-egress forwarded" "$l1_mcast_fwd" "$l1_received" 0.05

# listener1 mc-egress errors must be zero.
if [[ "$l1_mc_errors" -ne 0 ]]; then
  echo "FAIL  l1 mc_egress_errors=$l1_mc_errors (expected 0) — check nft OUTPUT rule and socket config"
  SCENARIO_FAIL=1
else
  echo "PASS  l1 mc_egress_errors=0"
fi

# listener4 received (consumer of listener1's mc-egress output).
assert_near "l4 received"  "$l4_received"  "$l1_received" 0.10

# listener4 forwarded.
assert_near "l4 forwarded" "$l4_forwarded" "$l1_received" 0.10

if [[ "$SCENARIO_FAIL" -ne 0 ]]; then
  echo "Scenario 05: FAIL"
  exit 1
fi
echo "Scenario 05: PASS"
