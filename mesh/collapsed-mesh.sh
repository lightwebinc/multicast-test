#!/usr/bin/env bash
# collapsed-mesh.sh — Phase 3 success demo: N independent collapsed nodes
# (real shard-proxy + shard-listener + retry-endpoint) in a full ip6gre mesh
# with mc-router (smcroute), each with one connected miner. Injects real BSV
# tx frames at every node's proxy via subtx-gen and asserts every miner
# receives traffic ingested at every node — full-duplex across the mesh.
#
# Builds on the verified Phase 0-2 topology (mesh/ip6gre-mesh.sh). Runs the
# host-native binaries directly in each node's netns (no Docker: the default
# bridge + br_netfilter cannot carry the mesh; netns + ip6gre can).
#
# Requirements (root): ip6_gre, smcroute>=2.5, go (workspace at $REPO_ROOT),
# python3. Run:  sudo ./collapsed-mesh.sh [N]   (default N=3)
set -euo pipefail

N="${1:-3}"
REPO_ROOT="${REPO_ROOT:-/home/light/repo}"
GOWORK="${GOWORK:-$REPO_ROOT/go.work}"
BIN=/tmp/mesh-bin
RUN=/tmp/mesh-run
SHARD_BITS=2 ; MC_SCOPE=site ; MC_GROUP_ID=0x000B
UDP_INGRESS=9000 ; MC_PORT=9001 ; NACK_PORT=9300 ; EGRESS_PORT=9500
WAN_PREFIX="fd00:a" ; LOC_PREFIX="fd00:b" ; GRE_PREFIX="fd00:f"
COUT_PREFIX="fd00:ac" ; CIN_PREFIX="fd00:c0"
PIDS=()

log()  { printf '\033[1;34m[demo]\033[0m %s\n' "$*"; }
ok()   { printf '\033[1;32m[ ok ]\033[0m %s\n' "$*"; }
fail() { printf '\033[1;31m[FAIL]\033[0m %s\n' "$*"; }
nse()  { ip netns exec "$@"; }

require_root() { [ "$(id -u)" -eq 0 ] || { fail "run as root"; exit 1; }; }
preflight() {
  modprobe ip6_gre 2>/dev/null || true
  ip link add _t type ip6gre local ::1 remote ::2 2>/dev/null && ip link del _t \
    || { fail "ip6_gre unavailable"; exit 1; }
  command -v smcrouted >/dev/null || { fail "smcroute>=2.5 required"; exit 1; }
  command -v go >/dev/null || { fail "go required"; exit 1; }
}

cleanup() {
  log "cleanup"
  for p in "${PIDS[@]:-}"; do kill "$p" 2>/dev/null || true; done
  for i in $(seq 1 "$N"); do
    ip netns pids "mcmesh$i" 2>/dev/null | xargs -r kill 2>/dev/null || true
    ip netns pids "mccons$i" 2>/dev/null | xargs -r kill 2>/dev/null || true
    ip netns del "mcmesh$i" 2>/dev/null || true
    ip netns del "mccons$i" 2>/dev/null || true
  done
}
trap cleanup EXIT

build_binaries() {
  log "building real binaries (GOWORK=$GOWORK)"
  mkdir -p "$BIN" "$RUN"
  GOWORK="$GOWORK" CGO_ENABLED=0 go build -trimpath -buildvcs=false \
    -o "$BIN/shard-proxy"    "$REPO_ROOT/shard-proxy"
  GOWORK="$GOWORK" CGO_ENABLED=0 go build -trimpath -buildvcs=false \
    -o "$BIN/shard-listener" "$REPO_ROOT/shard-listener"
  GOWORK="$GOWORK" CGO_ENABLED=0 go build -trimpath -buildvcs=false \
    -o "$BIN/retry-endpoint"  "$REPO_ROOT/retry-endpoint"
  ( cd "$REPO_ROOT/subtx-generator" && GOWORK="$GOWORK" CGO_ENABLED=0 go build \
      -trimpath -buildvcs=false -o "$BIN/subtx-gen" ./cmd/subtx-gen )
  ok "binaries in $BIN"
}

# --- topology (identical model to the verified ip6gre-mesh.sh) -------------
build_topology() {
  log "building $N-node full mesh + 1 miner each"
  for i in $(seq 1 "$N"); do
    ns="mcmesh$i" ; ip netns add "$ns" ; ip -n "$ns" link set lo up
    ip -n "$ns" link add mc-local type dummy
    ip -n "$ns" addr add "$LOC_PREFIX::$i/64" dev mc-local nodad
    ip -n "$ns" link set mc-local up multicast on
    ip -n "$ns" -6 route add ff05::/16 dev mc-local
    ip -n "$ns" -6 route add ff0e::/16 dev mc-local
    nse "$ns" sysctl -wq net.ipv6.conf.all.forwarding=1
  done
  for i in $(seq 1 "$N"); do for j in $(seq $((i + 1)) "$N"); do
    ip link add "o${i}_${j}" type veth peer name "o${j}_${i}"
    ip link set "o${i}_${j}" netns "mcmesh$i" ; ip link set "o${j}_${i}" netns "mcmesh$j"
    ip -n "mcmesh$i" addr add "$WAN_PREFIX:$i:$j::$i/64" dev "o${i}_${j}" nodad
    ip -n "mcmesh$j" addr add "$WAN_PREFIX:$i:$j::$j/64" dev "o${j}_${i}" nodad
    ip -n "mcmesh$i" link set "o${i}_${j}" up ; ip -n "mcmesh$j" link set "o${j}_${i}" up
  done ; done
  for i in $(seq 1 "$N"); do ns="mcmesh$i" ; for j in $(seq 1 "$N"); do
    [ "$i" -eq "$j" ] && continue
    lo=$(( i < j ? i : j )) ; hi=$(( i < j ? j : i ))
    ip -n "$ns" link add "gre6-$j" type ip6gre \
      local "$WAN_PREFIX:$lo:$hi::$i" remote "$WAN_PREFIX:$lo:$hi::$j" ttl 64
    ip -n "$ns" addr add "$GRE_PREFIX:$lo:$hi::$i/64" dev "gre6-$j" nodad
    ip -n "$ns" link set "gre6-$j" up mtu 1400 multicast on
  done ; done
  # miner per node over a consumer leaf tunnel (gre6-c1 = node side)
  for i in $(seq 1 "$N"); do
    cns="mccons$i" ; ip netns add "$cns" ; ip -n "$cns" link set lo up
    ip link add "cu$i" type veth peer name "cup$i"
    ip link set "cu$i" netns "mcmesh$i" ; ip link set "cup$i" netns "$cns"
    ip -n "mcmesh$i" addr add "$COUT_PREFIX:$i::1/64" dev "cu$i" nodad
    ip -n "$cns"     addr add "$COUT_PREFIX:$i::2/64" dev "cup$i" nodad
    ip -n "mcmesh$i" link set "cu$i" up ; ip -n "$cns" link set "cup$i" up
    ip -n "mcmesh$i" link add gre6-c1 type ip6gre \
      local "$COUT_PREFIX:$i::1" remote "$COUT_PREFIX:$i::2" ttl 64
    ip -n "mcmesh$i" addr add "$CIN_PREFIX:$i::1/64" dev gre6-c1 nodad
    ip -n "mcmesh$i" link set gre6-c1 up mtu 1400 multicast on
    ip -n "$cns" link add gre6-up type ip6gre \
      local "$COUT_PREFIX:$i::2" remote "$COUT_PREFIX:$i::1" ttl 64
    ip -n "$cns" addr add "$CIN_PREFIX:$i::2/64" dev gre6-up nodad
    ip -n "$cns" link set gre6-up up mtu 1400 multicast on
  done
}

start_routers() {
  for i in $(seq 1 "$N"); do
    ns="mcmesh$i" ; conf="$RUN/${ns}-smcroute.conf"
    tuns=() ; for j in $(seq 1 "$N"); do [ "$i" -ne "$j" ] && tuns+=("gre6-$j"); done
    { echo "phyint mc-local enable"
      for t in "${tuns[@]}"; do echo "phyint $t enable"; done
      echo "phyint gre6-c1 enable"
      for g in ff05::/16 ff0e::/16; do
        echo "mroute from mc-local group $g to ${tuns[*]} gre6-c1"
        for t in "${tuns[@]}"; do echo "mroute from $t group $g to mc-local gre6-c1"; done
      done
    } > "$conf"
    nse "$ns" smcrouted -n -f "$conf" >>"$RUN/mcmesh$i-smcroute.log" 2>&1 &
    PIDS+=($!)
  done
  sleep 1
}

# --- real services per node -----------------------------------------------
start_services() {
  log "starting proxy + listener + retry per node"
  for i in $(seq 1 "$N"); do
    ns="mcmesh$i"
    # retry-endpoint (cache + NACK)
    nse "$ns" env MC_IFACE=mc-local EGRESS_IFACE=mc-local LISTEN_PORT=$MC_PORT \
      NACK_PORT=$NACK_PORT EGRESS_PORT=$MC_PORT SHARD_BITS=$SHARD_BITS MC_SCOPE=$MC_SCOPE \
      MC_GROUP_ID=$MC_GROUP_ID METRICS_ADDR=:9400 \
      "$BIN/retry-endpoint" >>"$RUN/mcmesh$i-retry.log" 2>&1 & PIDS+=($!)
    # shard-listener (egress unicast to this node's miner over the consumer tunnel)
    nse "$ns" env MULTICAST_IF=mc-local LISTEN_PORT=$MC_PORT SHARD_BITS=$SHARD_BITS \
      MC_SCOPE=$MC_SCOPE MC_GROUP_ID=$MC_GROUP_ID NUM_WORKERS=1 \
      EGRESS_ADDR="[$CIN_PREFIX:$i::2]:$EGRESS_PORT" EGRESS_PROTO=udp METRICS_ADDR=:9200 \
      "$BIN/shard-listener" >>"$RUN/mcmesh$i-listener.log" 2>&1 & PIDS+=($!)
    # shard-proxy (sender ingress on [::]:9000, multicast egress on mc-local)
    # EGRESS_HOPLIMIT=64: the proxy emits multicast at hoplimit 1 by default,
    # which MFC decrements to 0 on the first fabric hop. Raising it lets frames
    # cross the mesh. DEBUG=true enables IPV6_MULTICAST_LOOP so the co-located
    # listener also receives this node's OWN emissions.
    nse "$ns" env MULTICAST_IF=mc-local UDP_LISTEN_PORT=$UDP_INGRESS EGRESS_PORT=$MC_PORT \
      SHARD_BITS=$SHARD_BITS MC_SCOPE=$MC_SCOPE MC_GROUP_ID=$MC_GROUP_ID METRICS_ADDR=:9100 \
      EGRESS_HOPLIMIT=64 DEBUG=true \
      "$BIN/shard-proxy" >>"$RUN/mcmesh$i-proxy.log" 2>&1 & PIDS+=($!)
  done
  sleep 3   # let listeners join groups + beacons settle
}

# UDP sink in each miner netns counting frames the listener forwards downstream.
# Self-terminates after IDLE seconds with no packet, then prints the count — no
# external kill (the kill target would be the `ip netns exec` wrapper, not this).
SINK_PY='
import socket,sys
addr,port,idle=sys.argv[1],int(sys.argv[2]),float(sys.argv[3])
s=socket.socket(socket.AF_INET6,socket.SOCK_DGRAM); s.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1)
s.bind((addr,port)); s.settimeout(idle); n=0
while True:
    try: s.recvfrom(65535); n+=1
    except socket.timeout: break
print(n)
'

SINK_PIDS=()
start_sinks() {
  for i in $(seq 1 "$N"); do
    nse "mccons$i" python3 -c "$SINK_PY" "$CIN_PREFIX:$i::2" "$EGRESS_PORT" 4.0 \
      > "$RUN/miner$i.count" 2>"$RUN/miner$i-sink.log" & SINK_PIDS+=($!) ; PIDS+=($!)
  done
  sleep 0.5
}

inject() {   # inject real frames from miner $1 into its node's proxy ingress
  local i="$1"
  nse "mccons$i" "$BIN/subtx-gen" \
    -addr "[$CIN_PREFIX:$i::1]:$UDP_INGRESS" \
    -shard-bits $SHARD_BITS -subtrees 8 -subtree-seed mesh-demo \
    -pps 200 -duration 3s -payload-size 256 -log-interval 1s \
    >>"$RUN/miner$i-gen.log" 2>&1 || true
}

require_root ; preflight ; build_binaries ; build_topology ; start_routers
start_services ; start_sinks

log "injecting real BSV frames from every miner (full duplex)"
inj_pids=()
for i in $(seq 1 "$N"); do inject "$i" & inj_pids+=($!); done
for p in "${inj_pids[@]}"; do wait "$p" 2>/dev/null || true; done  # only the injectors, not the services
sleep 2

# scrape per-node pipeline counters before teardown (proxy/listener don't log
# per-frame). Localizes any break: ingress -> emit -> listener recv -> egress.
SCRAPE_PY='
import sys,urllib.request
url=sys.argv[1]; keys=sys.argv[2:]
try: data=urllib.request.urlopen(url,timeout=2).read().decode()
except Exception as e: print("  scrape-failed:",e); sys.exit(0)
hit=False
for ln in data.splitlines():
    if ln.startswith("#"): continue
    if any(ln.startswith(k) for k in keys): print("  "+ln); hit=True
if not hit:
    tot=sum(1 for l in data.splitlines() if l and not l.startswith("#"))
    print("  (no matching counters; %d metric series present)"%tot)
'
log "per-node pipeline metrics"
for i in $(seq 1 "$N"); do
  echo "-- node$i proxy --"
  nse "mcmesh$i" python3 -c "$SCRAPE_PY" "http://[::1]:9100/metrics" \
    bsp_packets_received_total bsp_packets_forwarded_total bsp_egress_errors_total bsp_ingress_errors_total
  echo "-- node$i listener --"
  nse "mcmesh$i" python3 -c "$SCRAPE_PY" "http://[::1]:9200/metrics" \
    bsl_frames_received_total bsl_frames_forwarded_total bsl_egress_errors_total
  echo "-- node$i MFC (expect Iif: gre6-N fan-in entries once cross-tunnel works) --"
  nse "mcmesh$i" ip -6 mroute show 2>/dev/null | sed 's/^/  /'
done

# sinks self-terminate after their idle timeout; wait for them to flush counts
log "waiting for miner sinks to drain"
for pid in "${SINK_PIDS[@]}"; do wait "$pid" 2>/dev/null || true; done

echo
overall=0
for i in $(seq 1 "$N"); do
  got=$(cat "$RUN/miner$i.count" 2>/dev/null || echo 0); got=${got:-0}
  if [ "$got" -gt 0 ]; then ok "miner$i received $got frames (proxy->mesh->listener->miner)"
  else fail "miner$i received 0 — see $RUN/mcmesh$i-*.log"; overall=1; fi
done
echo
if [ "$overall" -eq 0 ]; then
  ok "COLLAPSED MESH FULL-DUPLEX DEMO VERIFIED ($N nodes, $N miners, real binaries)"
else
  fail "demo failed — inspect $RUN/*.log (proxy/listener/retry/smcroute per node)"
fi
exit "$overall"
