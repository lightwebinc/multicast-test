#!/usr/bin/env bash
set -euo pipefail
SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "$SCENARIO_DIR/../.." && pwd)"

bash "$REPO_DIR/lab/07-firewall-verify.sh"

if grep -q '^FAIL' "$SCENARIO_DIR/report.txt"; then
  echo "Scenario 00: FAIL — see $SCENARIO_DIR/report.txt"
  exit 1
fi
echo "Scenario 00: PASS"
