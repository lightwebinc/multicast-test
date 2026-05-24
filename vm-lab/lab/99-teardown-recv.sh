#!/usr/bin/env bash
# One-shot: retire recv1..3 VMs and related per-host state.
# Safe to re-run; each step is idempotent.
set -euo pipefail
exec </dev/null

echo "==> [99] Retiring recv1..3"

for vm in recv1 recv2 recv3; do
  if lxc info "$vm" &>/dev/null; then
    echo "     stopping + deleting $vm"
    lxc stop --force "$vm" 2>/dev/null || true
    lxc delete --force "$vm"
  else
    echo "     $vm already absent"
  fi
done

echo "==> [99] Done. Listeners (listener1..3) now replace these roles."
