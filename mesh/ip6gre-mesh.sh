#!/usr/bin/env bash
# ip6gre-mesh.sh — privileged netns repro of the integrated-infra mc-router
# full-mesh fabric. Builds N node network namespaces in a full mesh of ip6gre
# tunnels, installs the same smcroute static (iif,G)->oifs rules the Ansible
# role generates, and tests IPv6 multicast replication across the mesh.
#
# This is the empirical proof of the Phase 0 thesis (multicast over a GRE mesh)
# and mirrors integrated-infra/ansible/roles/mc-router exactly:
#   * mc-local : node-local multicast segment (dummy), carries the ff0X route
#   * gre6-NN  : one ip6gre peer tunnel per neighbour (fabric link)
#   * smcrouted: fan-out local->tunnels, fan-in tunnel->local (full mesh)
#
# Requirements (root): ip6_gre kernel module, iproute2, smcroute >= 2.5,
# python3. Run:  sudo ./ip6gre-mesh.sh [N]   (default N=3)
#
# VERIFIED FINDINGS (this script proves the Phase 0 thesis):
#   * Linux submits LOCALLY-ORIGINATED multicast to the MFC using the TRANSMIT
#     interface as the input VIF — so emitting on mc-local matches a
#     `from mc-local to <tunnels>` fan-out rule directly. No veth needed; the
#     co-located receiver gets its copy via IPV6_MULTICAST_LOOP. This mirrors
#     the collapsed-node model (proxy + listener share mc_iface).
#   * ip6gre tunnels need `multicast on` (they come up POINTOPOINT,NOARP) or
#     smcrouted won't register them as VIFs; smcroute also needs `phyint enable`.
#   * The emit SOURCE must be a global/ULA address (link-local is never
#     forwarded off-link).
#   * The outer transport must not be a Docker-managed bridge (br_netfilter ->
#     ip6tables FORWARD DROP). This repro uses direct per-pair veth links.
# FRR pim6d remains the path for PARTIAL meshes (transit relay needs PIM RPF,
# which smcroute lacks).
set -euo pipefail

N="${1:-3}"
GROUP="ff05::b:1"          # a BRC-129 site-scoped shard group (idx 0x0001)
PORT=9001
WAN_PREFIX="fd00:a"        # outer transport subnet (scenario-specific; lab only)
LOC_PREFIX="fd00:b"        # local multicast segment inner addrs (lab only)
GRE_PREFIX="fd00:f"        # tunnel inner addrs (lab only)
NS=() ; for i in $(seq 1 "$N"); do NS+=("mcmesh$i"); done

log()  { printf '\033[1;34m[mesh]\033[0m %s\n' "$*"; }
fail() { printf '\033[1;31m[FAIL]\033[0m %s\n' "$*"; }
ok()   { printf '\033[1;32m[ ok ]\033[0m %s\n' "$*"; }

require_root() { [ "$(id -u)" -eq 0 ] || { fail "must run as root"; exit 1; }; }
preflight() {
  modprobe ip6_gre 2>/dev/null || true
  ip link add _gretest type ip6gre local ::1 remote ::2 2>/dev/null \
    && ip link del _gretest \
    || { fail "ip6_gre unavailable (modprobe ip6_gre / kernel support)"; exit 1; }
  command -v smcrouted >/dev/null || { fail "smcroute >= 2.5 required"; exit 1; }
  command -v python3   >/dev/null || { fail "python3 required"; exit 1; }
}

cleanup() {
  log "cleanup"
  for ns in "${NS[@]}"; do ip netns pids "$ns" 2>/dev/null | xargs -r kill 2>/dev/null || true; done
  # deleting a namespace removes its veth ends (and their peers), so the direct
  # outer links are torn down with the namespaces — no bridge to clean up.
  for ns in "${NS[@]}"; do ip netns del "$ns" 2>/dev/null || true; done
}
trap cleanup EXIT

# --- topology -------------------------------------------------------------
# Direct per-pair veth links for the outer transport (no bridge): a shared
# bridge would subject bridged frames to br_netfilter -> ip6tables FORWARD,
# which Docker sets to DROP. Point-to-point veths bypass that entirely.
build() {
  log "building $N-node full mesh (direct veth outer links, no bridge)"
  for i in $(seq 1 "$N"); do
    ns="mcmesh$i"
    ip netns add "$ns"
    ip -n "$ns" link set lo up
    # node-local multicast segment (dummy). Proxy emits AND listener/retry join
    # here — exactly the collapsed-node model. Linux submits locally-originated
    # multicast to MFC using the TRANSMIT interface as the input VIF, so the
    # smcroute fan-out rule is `from mc-local to <tunnels>`; the local listener
    # gets its own copy via IPV6_MULTICAST_LOOP. A global/ULA source address is
    # required (link-local sources are never forwarded off-link).
    ip -n "$ns" link add mc-local type dummy
    ip -n "$ns" addr add "$LOC_PREFIX::$i/64" dev mc-local nodad
    ip -n "$ns" link set mc-local up multicast on
    ip -n "$ns" -6 route add ff05::/16 dev mc-local
    ip -n "$ns" -6 route add ff0e::/16 dev mc-local
    # Only forwarding is user-writable; mc_forwarding is read-only and is
    # enabled by the kernel when smcrouted opens the MRT6 routing socket.
    ip netns exec "$ns" sysctl -wq net.ipv6.conf.all.forwarding=1
  done
  # direct outer veth link per unordered pair (the "internet" between nodes)
  for i in $(seq 1 "$N"); do
    for j in $(seq $((i + 1)) "$N"); do
      ip link add "o${i}_${j}" type veth peer name "o${j}_${i}"
      ip link set "o${i}_${j}" netns "mcmesh$i"
      ip link set "o${j}_${i}" netns "mcmesh$j"
      ip -n "mcmesh$i" addr add "$WAN_PREFIX:$i:$j::$i/64" dev "o${i}_${j}" nodad
      ip -n "mcmesh$j" addr add "$WAN_PREFIX:$i:$j::$j/64" dev "o${j}_${i}" nodad
      ip -n "mcmesh$i" link set "o${i}_${j}" up
      ip -n "mcmesh$j" link set "o${j}_${i}" up
    done
  done
  # full mesh of ip6gre tunnels over the direct links; named by peer index.
  for i in $(seq 1 "$N"); do
    ns="mcmesh$i"
    for j in $(seq 1 "$N"); do
      [ "$i" -eq "$j" ] && continue
      lo=$(( i < j ? i : j )) ; hi=$(( i < j ? j : i ))
      ip -n "$ns" link add "gre6-$j" type ip6gre \
        local "$WAN_PREFIX:$lo:$hi::$i" remote "$WAN_PREFIX:$lo:$hi::$j" ttl 64
      ip -n "$ns" addr add "$GRE_PREFIX:$lo:$hi::$i/64" dev "gre6-$j" nodad
      # ip6gre tunnels come up without the MULTICAST flag; smcrouted needs it
      # to register the tunnel as a forwarding VIF.
      ip -n "$ns" link set "gre6-$j" up mtu 1400 multicast on
    done
  done
}

# --- smcroute (mirrors roles/mc-router/templates/smcroute.conf.j2) --------
start_routers() {
  for i in $(seq 1 "$N"); do
    ns="mcmesh$i" ; conf="/tmp/${ns}-smcroute.conf"
    tuns=() ; for j in $(seq 1 "$N"); do [ "$i" -ne "$j" ] && tuns+=("gre6-$j"); done
    {
      echo "# generated by ip6gre-mesh.sh for $ns"
      # Register the VIFs explicitly so route parsing always finds them.
      echo "phyint mc-local enable"
      for t in "${tuns[@]}"; do echo "phyint $t enable"; done
      for g in ff05::/16 ff0e::/16; do
        echo "mroute from mc-local group $g to ${tuns[*]}"          # fan-out
        for t in "${tuns[@]}"; do echo "mroute from $t group $g to mc-local"; done  # fan-in
      done
    } > "$conf"
    ip netns exec "$ns" smcrouted -n -f "$conf" &
    sleep 0.3
  done
  log "smcrouted started in all namespaces"
}

# --- multicast test harness (python, inline) ------------------------------
RECV_PY='
import socket,struct,sys,time
grp,port,ifname,secs=sys.argv[1],int(sys.argv[2]),sys.argv[3],float(sys.argv[4])
s=socket.socket(socket.AF_INET6,socket.SOCK_DGRAM)
s.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1)
s.bind(("",port))
idx=socket.if_nametoindex(ifname)
mreq=socket.inet_pton(socket.AF_INET6,grp)+struct.pack("@I",idx)
s.setsockopt(socket.IPPROTO_IPV6,socket.IPV6_JOIN_GROUP,mreq)
s.settimeout(secs)
got=0
try:
  while True:
    d,_=s.recvfrom(2048); got+=1
except socket.timeout: pass
print(got)
'
SEND_PY='
import socket,struct,sys,time
grp,port,ifname,n=sys.argv[1],int(sys.argv[2]),sys.argv[3],int(sys.argv[4])
s=socket.socket(socket.AF_INET6,socket.SOCK_DGRAM)
idx=socket.if_nametoindex(ifname)
s.setsockopt(socket.IPPROTO_IPV6,socket.IPV6_MULTICAST_IF,struct.pack("@I",idx))
s.setsockopt(socket.IPPROTO_IPV6,socket.IPV6_MULTICAST_HOPS,8)
# spread sends: the first packet triggers the smcroute (S,G) upcall + MFC
# install; later ones traverse the freshly-installed route.
for _ in range(n):
    s.sendto(b"bsv-mesh-probe",(grp,port)); time.sleep(0.05)
'

# send from src node index, expect receipt at all other nodes
probe() {
  local src="$1" ; shift
  declare -A recv_pid recv_out
  for i in $(seq 1 "$N"); do
    [ "$i" -eq "$src" ] && continue
    recv_out[$i]="/tmp/mcmesh$i-recv.out"
    ip netns exec "mcmesh$i" python3 -c "$RECV_PY" "$GROUP" "$PORT" mc-local 2.0 \
      > "${recv_out[$i]}" & recv_pid[$i]=$!
  done
  sleep 0.4
  # emit on mc-local: locally-originated mcast hits MFC with iif=mc-local (the
  # tx iface), matching the `from mc-local to <tunnels>` fan-out rule.
  ip netns exec "mcmesh$src" python3 -c "$SEND_PY" "$GROUP" "$PORT" mc-local 20
  local rc=0
  for i in $(seq 1 "$N"); do
    [ "$i" -eq "$src" ] && continue
    wait "${recv_pid[$i]}" || true
    local got; got=$(cat "${recv_out[$i]}" 2>/dev/null || echo 0)
    if [ "${got:-0}" -gt 0 ]; then ok "node$src -> node$i: received $got"; else fail "node$src -> node$i: 0 received"; rc=1; fi
  done
  return $rc
}

# --- diagnostics: localize the fault layer by layer ----------------------
mroute_dump() {
  for i in $(seq 1 "$N"); do
    echo "-- mcmesh$i kernel MFC (ip -6 mroute) --"
    ip netns exec "mcmesh$i" ip -6 mroute show 2>/dev/null || true
  done
}

diagnose() {
  log "diagnostics"
  echo "-- node1 interface flags (need MULTICAST) --"
  ip -n mcmesh1 link show mc-local | sed -n '1,2p'
  ip -n mcmesh1 link show gre6-2   | sed -n '1,2p'
  echo "-- L1 outer transport (direct veth) --"
  ip netns exec mcmesh1 ping6 -c1 -W1 "$WAN_PREFIX:1:2::2" >/dev/null 2>&1 \
    && ok "WAN node1->2" || fail "WAN node1->2 (direct veth transport broken)"
  echo "-- L2 tunnel encap (inner unicast over gre6) --"
  ip netns exec mcmesh1 ping6 -c1 -W1 "$GRE_PREFIX:1:2::2" >/dev/null 2>&1 \
    && ok "tunnel node1->2 inner" || fail "tunnel node1->2 inner (ip6gre encap broken)"
  echo "-- L3 RELAY path only: node1 emits straight into gre6-2, node2 relays to mc-local --"
  local out="/tmp/mcmesh2-direct.out"
  ip netns exec mcmesh2 python3 -c "$RECV_PY" "$GROUP" "$PORT" mc-local 2.0 > "$out" &
  local pid=$!
  sleep 0.4
  ip netns exec mcmesh1 python3 -c "$SEND_PY" "$GROUP" "$PORT" gre6-2 20
  wait "$pid" || true
  local got; got=$(cat "$out" 2>/dev/null || echo 0)
  if [ "${got:-0}" -gt 0 ]; then ok "RELAY node1-gre->node2: $got (transport + receive-relay OK; only local-source fan-out remains)"
  else fail "RELAY node1-gre->node2: 0 (transport or receive-side smcroute relay broken)"; fi
  echo "-- L4 self-delivery: node1 emits + receives on mc-local (MULTICAST_LOOP) --"
  local s4="/tmp/mcmesh1-self.out"
  ip netns exec mcmesh1 python3 -c "$RECV_PY" "$GROUP" "$PORT" mc-local 2.0 > "$s4" &
  local sp=$!
  sleep 0.4
  ip netns exec mcmesh1 python3 -c "$SEND_PY" "$GROUP" "$PORT" mc-local 20
  wait "$sp" || true
  local g4; g4=$(cat "$s4" 2>/dev/null || echo 0)
  if [ "${g4:-0}" -gt 0 ]; then ok "local self-delivery: $g4 (MULTICAST_LOOP OK)"
  else fail "local self-delivery: 0"; fi
  echo "-- L5 local-source fan-out: node1 emits on mc-local -> node2 --"
  local s5="/tmp/mcmesh2-fan.out"
  ip netns exec mcmesh2 python3 -c "$RECV_PY" "$GROUP" "$PORT" mc-local 2.5 > "$s5" &
  local rp=$!
  sleep 0.4
  ip netns exec mcmesh1 python3 -c "$SEND_PY" "$GROUP" "$PORT" mc-local 30
  wait "$rp" || true
  local g5; g5=$(cat "$s5" 2>/dev/null || echo 0)
  if [ "${g5:-0}" -gt 0 ]; then ok "fan-out node1->node2: $g5"; else fail "fan-out node1->node2: 0"; fi
  echo "-- MFC entries after local-source probes (look for Iif: mc-local on node1) --"
  mroute_dump
}

require_root ; preflight ; build ; start_routers
diagnose
log "testing full-duplex multicast replication on $GROUP"
overall=0
for s in $(seq 1 "$N"); do probe "$s" || overall=1; done
echo
if [ "$overall" -eq 0 ]; then ok "MESH REPLICATION VERIFIED (all directions)"; else
  fail "some directions failed — inspect the L1-L5 diagnostics and MFC dump above (transport / MULTICAST flag / phyint / global source)"; fi
exit "$overall"
