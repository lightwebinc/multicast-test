#!/usr/bin/env bash
# Start the LXD lab VMs in a sensible order.
# Idempotent: skips VMs that are already RUNNING; starts only those that exist.
set -euo pipefail

# Boot order (dependencies first): network → infra → producers → consumers.
ORDER=(
  router1
  router2
  redis
  metrics
  proxy
  proxy2
  retry1
  retry2
  retry3
  listener1
  listener2
  listener3
  listener4
  source
)

state_of() {
  lxc list -c ns --format csv "^${1}\$" | awk -F, '{print $2}'
}

start_one() {
  local vm="$1"
  local state
  state="$(state_of "${vm}")"
  if [[ -z "${state}" ]]; then
    echo "  skip: ${vm} (not defined)"
    return 0
  fi
  if [[ "${state}" == "RUNNING" ]]; then
    echo "  skip: ${vm} (already RUNNING)"
    return 0
  fi
  echo "  start: ${vm}"
  lxc start "${vm}"
  sleep 1
}

echo "==> starting LXD lab"
for vm in "${ORDER[@]}"; do
  start_one "${vm}"
done

echo "==> current state"
lxc list -c ns
