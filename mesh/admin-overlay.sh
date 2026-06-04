#!/usr/bin/env bash
# admin-overlay.sh — privileged netns repro of the integrated-infra
# admin-overlay role: an N-node full-mesh WireGuard overlay on a separate
# address space, reachable over an "internet" of direct veth links. Verifies
# the wg handshake and end-to-end connectivity over the overlay in every
# direction (the encrypted admin plane the role provisions for SSH).
#
# Mirrors roles/admin-overlay (keys per node, peers = every other node, overlay
# /128 AllowedIPs, endpoint = peer WAN:port). The role uses wg-quick; here we
# set up the same wg config manually inside each netns.
#
# Requirements (root): wireguard kernel module, wireguard-tools, iproute2.
# Run:  sudo ./admin-overlay.sh [N]   (default N=3)
set -euo pipefail

N="${1:-3}"
WAN_PREFIX="fd00:a"        # outer transport (the "internet"); per-pair /64
AD_PREFIX="fd00:ad"        # overlay address space (operator-supplied; lab here)
WG_PORT=51820
NS=() ; for i in $(seq 1 "$N"); do NS+=("nodewg$i"); done
declare -A PUB

log()  { printf '\033[1;34m[wg]\033[0m %s\n' "$*"; }
ok()   { printf '\033[1;32m[ ok ]\033[0m %s\n' "$*"; }
fail() { printf '\033[1;31m[FAIL]\033[0m %s\n' "$*"; }

require_root() { [ "$(id -u)" -eq 0 ] || { fail "run as root"; exit 1; }; }
preflight() {
  modprobe wireguard 2>/dev/null || true
  command -v wg >/dev/null || { fail "wireguard-tools (wg) required: apt-get install -y wireguard-tools"; exit 1; }
}
cleanup() { for ns in "${NS[@]}"; do ip netns del "$ns" 2>/dev/null || true; done; }
trap cleanup EXIT

build() {
  log "building $N-node WireGuard admin overlay over a direct-veth internet"
  for i in $(seq 1 "$N"); do
    ip netns add "nodewg$i" ; ip -n "nodewg$i" link set lo up
    # per-node wg keypair
    local key; key=$(wg genkey); PUB[$i]=$(echo "$key" | wg pubkey)
    printf '%s' "$key" > "/tmp/nodewg$i.key"
  done
  # direct outer veth link per unordered pair (the transport "internet")
  for i in $(seq 1 "$N"); do for j in $(seq $((i + 1)) "$N"); do
    ip link add "ow${i}_${j}" type veth peer name "ow${j}_${i}"
    ip link set "ow${i}_${j}" netns "nodewg$i" ; ip link set "ow${j}_${i}" netns "nodewg$j"
    ip -n "nodewg$i" addr add "$WAN_PREFIX:$i:$j::$i/64" dev "ow${i}_${j}" nodad
    ip -n "nodewg$j" addr add "$WAN_PREFIX:$i:$j::$j/64" dev "ow${j}_${i}" nodad
    ip -n "nodewg$i" link set "ow${i}_${j}" up ; ip -n "nodewg$j" link set "ow${j}_${i}" up
  done ; done
  # wg interface per node + a full mesh of peers
  for i in $(seq 1 "$N"); do
    ns="nodewg$i"
    ip -n "$ns" link add wg-admin type wireguard
    ip -n "$ns" addr add "$AD_PREFIX::$i/64" dev wg-admin
    ip netns exec "$ns" wg set wg-admin listen-port "$WG_PORT" private-key "/tmp/nodewg$i.key"
    for j in $(seq 1 "$N"); do
      [ "$i" -eq "$j" ] && continue
      lo=$(( i < j ? i : j )) ; hi=$(( i < j ? j : i ))
      # peer j is reachable at its outer address on the shared per-pair link
      ip netns exec "$ns" wg set wg-admin peer "${PUB[$j]}" \
        allowed-ips "$AD_PREFIX::$j/128" \
        endpoint "[$WAN_PREFIX:$lo:$hi::$j]:$WG_PORT" \
        persistent-keepalive 25
    done
    ip -n "$ns" link set wg-admin up
  done
}

probe() {   # ping every other node's overlay address from node $1
  local src="$1" rc=0
  for j in $(seq 1 "$N"); do
    [ "$j" -eq "$src" ] && continue
    if ip netns exec "nodewg$src" ping6 -c2 -W2 "$AD_PREFIX::$j" >/dev/null 2>&1; then
      ok "node$src -> node$j over overlay ($AD_PREFIX::$j)"
    else fail "node$src -> node$j overlay unreachable"; rc=1; fi
  done
  return $rc
}

require_root ; preflight ; build
sleep 1   # allow initial handshakes
log "verifying full-mesh overlay reachability + handshakes"
overall=0
for s in $(seq 1 "$N"); do probe "$s" || overall=1; done
echo
echo "-- node1 wg handshakes --"
ip netns exec nodewg1 wg show wg-admin latest-handshakes | sed 's/^/  /'
echo
if [ "$overall" -eq 0 ]; then ok "ADMIN OVERLAY VERIFIED ($N-node WireGuard full mesh, all directions)"; else
  fail "overlay failed — check 'wg show' handshakes + outer veth transport"; fi
exit "$overall"
