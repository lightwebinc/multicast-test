#!/usr/bin/env bash
# Launch the lab VMs: source, proxy, listener1..3 (metrics VM managed separately).
# Old recv1..3 are retired — run lab/99-teardown-recv.sh to remove them.
set -euo pipefail
exec </dev/null

VMS=(source proxy proxy2 listener1 listener2 listener3 listener4 retry1 retry2 retry3 redis)

wait_for_vm() {
  local vm="$1"
  echo "     Waiting for $vm to reach RUNNING state..."
  for _ in $(seq 1 60); do
    state=$(lxc list "$vm" --format csv -c s 2>/dev/null | head -1)
    if [[ "$state" == "RUNNING" ]]; then
      echo "     $vm is RUNNING"
      return 0
    fi
    sleep 3
  done
  echo "ERROR: $vm did not reach RUNNING state in time" >&2
  return 1
}

for vm in "${VMS[@]}"; do
  if [[ "$vm" == "redis" ]]; then
    profile="ubuntu-small-single"
  elif [[ "$vm" == "source" ]]; then
    profile="ubuntu-source"
  else
    profile="ubuntu-small-mcast"
  fi
  echo "==> [03] Launching VM: $vm ($profile)..."
  if lxc info "$vm" &>/dev/null; then
    echo "     $vm already exists, skipping"
  else
    lxc launch ubuntu:24.04 "$vm" --vm --profile "$profile"
  fi
done

wait_for_agent() {
  local vm="$1"
  echo "     Waiting for $vm agent..."
  for _ in $(seq 1 60); do
    if lxc exec "$vm" -- true 2>/dev/null; then
      echo "     $vm agent ready"
      return 0
    fi
    sleep 3
  done
  echo "ERROR: $vm agent did not become ready in time" >&2
  return 1
}

echo "==> [03] Waiting for all VMs to be RUNNING..."
for vm in "${VMS[@]}"; do
  wait_for_vm "$vm"
done

echo "==> [03] Waiting for VM agents to be ready..."
for vm in "${VMS[@]}"; do
  wait_for_agent "$vm"
done

echo "==> [03] All VMs running:"
lxc list

# --- BGP router VMs ---
declare -A BGP_PROFILES=( [router1]=ubuntu-bgp-r1 [router2]=ubuntu-bgp-r2 )

for vm in router1 router2; do
  profile="${BGP_PROFILES[$vm]}"
  echo "==> [03] Launching BGP VM: $vm ($profile)..."
  if lxc info "$vm" &>/dev/null; then
    echo "     $vm already exists, skipping"
  else
    lxc launch ubuntu:24.04 "$vm" --vm --profile "$profile"
  fi
done

echo "==> [03] Adding lxdbr3 NIC to proxy (for iBGP peering)..."
if lxc config device show proxy | grep -q eth2; then
  echo "     proxy eth2 already exists, skipping"
else
  lxc config device add proxy eth2 nic network=lxdbr3 name=eth2
fi

echo "==> [03] Adding lxdbr3 NIC to proxy2 (for iBGP peering)..."
if lxc config device show proxy2 | grep -q eth2; then
  echo "     proxy2 eth2 already exists, skipping"
else
  lxc config device add proxy2 eth2 nic network=lxdbr3 name=eth2
fi

echo "==> [03] Waiting for BGP VMs..."
for vm in router1 router2; do
  wait_for_vm "$vm"
done
for vm in router1 router2; do
  wait_for_agent "$vm"
done

echo "==> [03] Tuning lxdbr1 tap interfaces (txqlen + mrouter)..."
for iface in $(ls /sys/devices/virtual/net/lxdbr1/brif/ 2>/dev/null); do
  sudo ip link set "$iface" txqueuelen 10000
done
for f in /sys/devices/virtual/net/lxdbr1/brif/*/multicast_router; do
  sudo tee "$f" <<< 2 > /dev/null
done
echo "     tap txqlen=10000, mrouter=2 (always-on) applied"

echo "==> [03] Done."
