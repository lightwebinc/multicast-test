#!/usr/bin/env bash
set -euo pipefail
exec </dev/null

PUBKEY="$(cat ~/.ssh/id_ed25519.pub)"

echo "==> [04] Configuring passwordless sudo and SSH key for ubuntu user on all VMs..."

for vm in source proxy listener1 listener2 listener3 listener4 retry1 retry2 retry3 redis; do
  echo "     $vm..."
  lxc exec "$vm" -- bash -c \
    "echo 'ubuntu ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/ubuntu-nopasswd && chmod 440 /etc/sudoers.d/ubuntu-nopasswd"
  lxc exec "$vm" -- bash -c \
    "install -d -m 700 -o ubuntu -g ubuntu /home/ubuntu/.ssh
     grep -qxF '${PUBKEY}' /home/ubuntu/.ssh/authorized_keys 2>/dev/null \
       || echo '${PUBKEY}' >> /home/ubuntu/.ssh/authorized_keys
     chmod 600 /home/ubuntu/.ssh/authorized_keys
     chown ubuntu:ubuntu /home/ubuntu/.ssh/authorized_keys"
done

echo "==> [04] Done."
