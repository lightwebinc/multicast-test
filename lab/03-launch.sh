#!/usr/bin/env bash
# Launch the lab VMs: source, proxy, listener1..3 (metrics VM managed separately).
# Old recv1..3 are retired — run lab/99-teardown-recv.sh to remove them.
set -euo pipefail
exec </dev/null

VMS=(source proxy listener1 listener2 listener3 listener4 retry1 retry2 retry3 redis)

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

echo "==> [03] Waiting for all VMs to be RUNNING..."
for vm in "${VMS[@]}"; do
  wait_for_vm "$vm"
done

echo "==> [03] All VMs running:"
lxc list

echo "==> [03] Tuning lxdbr1 tap interfaces (txqlen + mrouter)..."
for iface in $(ls /sys/devices/virtual/net/lxdbr1/brif/ 2>/dev/null); do
  sudo ip link set "$iface" txqueuelen 10000
done
for f in /sys/devices/virtual/net/lxdbr1/brif/*/multicast_router; do
  sudo tee "$f" <<< 2 > /dev/null
done
echo "     tap txqlen=10000, mrouter=2 (always-on) applied"

echo "==> [03] Done."
