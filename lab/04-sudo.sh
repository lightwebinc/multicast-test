#!/usr/bin/env bash
set -euo pipefail
exec </dev/null

echo "==> [04] Configuring passwordless sudo for ubuntu user on all VMs..."

for vm in source proxy listener1 listener2 listener3 listener4 retry1 retry2 retry3; do
  echo "     $vm..."
  lxc exec "$vm" -- bash -c \
    "echo 'ubuntu ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/ubuntu-nopasswd && chmod 440 /etc/sudoers.d/ubuntu-nopasswd"
done

echo "==> [04] Done."
