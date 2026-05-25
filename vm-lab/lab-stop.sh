#!/usr/bin/env bash
# Stop all LXD VMs in the lab. VMs retain storage and can be restarted with
# lab-start.sh. LXD daemon itself is not disabled (snapshots still required).
set -euo pipefail

echo "==> stopping all LXD instances"
lxc stop --all || true

echo "==> current state"
lxc list -c ns
