#!/usr/bin/env bash
# Restart all lab VMs and wait for SSH to be ready.
# Run after 06-netplan so VMs come up with a clean network state.
set -euo pipefail
exec </dev/null

VMS=(source proxy listener1 listener2 listener3 listener4 retry1 retry2 retry3)

echo "==> [06b] Restarting all lab VMs..."
for vm in "${VMS[@]}"; do
  echo "     restarting $vm..."
  lxc restart "$vm" --force
done

echo "==> [06b] Waiting for all VMs to reach RUNNING state..."
for vm in "${VMS[@]}"; do
  for _ in $(seq 1 60); do
    state=$(lxc list "$vm" --format csv -c s 2>/dev/null | head -1)
    if [[ "$state" == "RUNNING" ]]; then
      echo "     $vm: RUNNING"
      break
    fi
    sleep 2
  done
done

echo "==> [06b] Waiting for SSH to be ready on all VMs..."
for vm in "${VMS[@]}"; do
  ip=$(lxc list "$vm" --format csv -c 4 2>/dev/null | grep -o '[0-9]\+\.[0-9]\+\.[0-9]\+\.[0-9]\+' | head -1)
  if [[ -z "$ip" ]]; then
    echo "WARNING: could not determine IP for $vm, skipping SSH wait" >&2
    continue
  fi
  echo -n "     $vm ($ip): waiting for SSH..."
  for _ in $(seq 1 60); do
    if ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
           -o GSSAPIAuthentication=no -o ConnectTimeout=3 \
           -o BatchMode=yes ubuntu@"$ip" true 2>/dev/null; then
      echo " ready"
      break
    fi
    echo -n "."
    sleep 2
  done
done

echo "==> [06b] Done."
