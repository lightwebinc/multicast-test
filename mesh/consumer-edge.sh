#!/usr/bin/env bash
# consumer-edge.sh — privileged netns repro of the Phase 5 consumer-edge
# scale-out: a core node emits onto the fabric; a consumer-edge node receives it
# over an upstream tunnel and re-fans the BRC-129 groups to its own miner. Proves
# consumer-tunnel termination can be offloaded to a neighbour (core -> edge ->
# miner, a 2-hop mc-router fan) using the same primitives as the collapsed node.
#
# Requirements (root): ip6_gre, smcroute>=2.5, python3.  Run: sudo ./consumer-edge.sh
set -euo pipefail
GROUP="ff05::b:1" ; PORT=9001
log(){ printf '\033[1;34m[edge]\033[0m %s\n' "$*"; }
ok(){ printf '\033[1;32m[ ok ]\033[0m %s\n' "$*"; }
fail(){ printf '\033[1;31m[FAIL]\033[0m %s\n' "$*"; }
cleanup(){ for n in ce-core ce-edge ce-miner; do ip netns del "$n" 2>/dev/null||true; done; }
trap cleanup EXIT
[ "$(id -u)" -eq 0 ] || { fail "run as root"; exit 1; }
modprobe ip6_gre 2>/dev/null||true
command -v smcrouted >/dev/null || { fail "smcroute>=2.5 required"; exit 1; }

log "building core -> consumer-edge -> miner"
for ns in ce-core ce-edge ce-miner; do ip netns add "$ns"; ip -n "$ns" link set lo up; done
# core local segment (proxy emit point + listener)
ip -n ce-core link add mc-local type dummy
ip -n ce-core addr add fd00:b::1/64 dev mc-local nodad
ip -n ce-core link set mc-local up multicast on
ip -n ce-core -6 route add ff05::/16 dev mc-local hoplimit 64 2>/dev/null || ip -n ce-core -6 route add ff05::/16 dev mc-local
ip netns exec ce-core sysctl -wq net.ipv6.conf.all.forwarding=1
# core <-> edge outer veth
ip link add core_e type veth peer name edge_c
ip link set core_e netns ce-core ; ip link set edge_c netns ce-edge
ip -n ce-core addr add fd00:a:1::1/64 dev core_e nodad ; ip -n ce-core link set core_e up
ip -n ce-edge addr add fd00:a:1::2/64 dev edge_c nodad ; ip -n ce-edge link set edge_c up
# core's consumer-leaf tunnel to the edge (mc_egress on the core side)
ip -n ce-core link add gre6-edge type ip6gre local fd00:a:1::1 remote fd00:a:1::2 ttl 64
ip -n ce-core addr add fd00:c0:e::1/64 dev gre6-edge nodad
ip -n ce-core link set gre6-edge up mtu 1400 multicast on
# edge upstream tunnel from the core (fabric input on the edge side)
ip netns exec ce-edge sysctl -wq net.ipv6.conf.all.forwarding=1
ip -n ce-edge link add gre6-up type ip6gre local fd00:a:1::2 remote fd00:a:1::1 ttl 64
ip -n ce-edge addr add fd00:c0:e::2/64 dev gre6-up nodad
ip -n ce-edge link set gre6-up up mtu 1400 multicast on
ip -n ce-edge link add mc-local type dummy        # edge listener segment
ip -n ce-edge addr add fd00:b:e::1/64 dev mc-local nodad
ip -n ce-edge link set mc-local up multicast on
# edge <-> miner outer veth + the miner's consumer leaf tunnel
ip link add edge_m type veth peer name miner_e
ip link set edge_m netns ce-edge ; ip link set miner_e netns ce-miner
ip -n ce-edge addr add fd00:a:2::1/64 dev edge_m nodad ; ip -n ce-edge link set edge_m up
ip -n ce-miner addr add fd00:a:2::2/64 dev miner_e nodad ; ip -n ce-miner link set miner_e up
ip -n ce-edge link add gre6-c1 type ip6gre local fd00:a:2::1 remote fd00:a:2::2 ttl 64
ip -n ce-edge addr add fd00:c0:f::1/64 dev gre6-c1 nodad
ip -n ce-edge link set gre6-c1 up mtu 1400 multicast on
ip -n ce-miner link add gre6-up type ip6gre local fd00:a:2::2 remote fd00:a:2::1 ttl 64
ip -n ce-miner addr add fd00:c0:f::2/64 dev gre6-up nodad
ip -n ce-miner link set gre6-up up mtu 1400 multicast on

# smcroute: core fans local emit -> edge leaf; edge fans upstream -> local + miner leaf
printf 'phyint mc-local enable\nphyint gre6-edge enable\nmroute from mc-local group ff05::/16 to gre6-edge\n' > /tmp/ce-core.conf
printf 'phyint gre6-up enable\nphyint mc-local enable\nphyint gre6-c1 enable\nmroute from gre6-up group ff05::/16 to mc-local gre6-c1\n' > /tmp/ce-edge.conf
ip netns exec ce-core smcrouted -n -f /tmp/ce-core.conf >/tmp/ce-core.log 2>&1 &
ip netns exec ce-edge smcrouted -n -f /tmp/ce-edge.conf >/tmp/ce-edge.log 2>&1 &
sleep 1

RECV='import socket,struct,sys
s=socket.socket(socket.AF_INET6,socket.SOCK_DGRAM);s.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1);s.bind(("",9001))
idx=socket.if_nametoindex("gre6-up");s.setsockopt(41,20,socket.inet_pton(socket.AF_INET6,"ff05::b:1")+struct.pack("@I",idx))
s.settimeout(3.0);n=0
try:
 while True: s.recvfrom(2048);n+=1
except socket.timeout: pass
print(n)'
SEND='import socket,struct,sys,time
s=socket.socket(socket.AF_INET6,socket.SOCK_DGRAM)
s.setsockopt(41,17,struct.pack("@I",socket.if_nametoindex("mc-local")));s.setsockopt(41,18,8)
for _ in range(30): s.sendto(b"edge-probe",("ff05::b:1",9001));time.sleep(0.05)'

log "core emits; verifying miner receives via the consumer-edge"
ip netns exec ce-miner python3 -c "$RECV" >/tmp/ce-miner.out 2>/dev/null &
rp=$!
sleep 0.4
ip netns exec ce-core python3 -c "$SEND"
wait "$rp" 2>/dev/null || true
got=$(cat /tmp/ce-miner.out 2>/dev/null||echo 0); got=${got:-0}
echo
if [ "$got" -gt 0 ]; then ok "CONSUMER-EDGE VERIFIED: miner received $got frames (core -> edge -> miner)"; exit 0
else fail "miner received 0 — see /tmp/ce-core.log /tmp/ce-edge.log"; exit 1; fi
