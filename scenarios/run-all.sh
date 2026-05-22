#!/usr/bin/env bash
# Run all scenario tests sequentially, collect results, and print a structured
# summary.
#
# Usage:
#   bash scenarios/run-all.sh               # skip 04-extended-dashboard (24h soak)
#   RUN_EXTENDED=1 bash scenarios/run-all.sh # include all scenarios
#   ONLY=99 bash scenarios/run-all.sh        # run only scenarios matching glob "99*"
#
# Exit code: 0 if all scenarios pass, 1 if any fail.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Per-run log directory (not committed — under /tmp so the repo stays clean).
RUN_ID="$(date +%Y%m%dT%H%M%S)"
LOG_DIR="/tmp/scenario-run-${RUN_ID}"
mkdir -p "$LOG_DIR"

# Scenarios to skip unconditionally (long-running / not assertions-based).
SKIP_ALWAYS=("04-extended-dashboard" "21-subtree-group-ramp")

# ---- helpers ---------------------------------------------------------------

tput_cmd() { command -v tput &>/dev/null && tput "$@" 2>/dev/null || true; }
BOLD="$(tput_cmd bold)"
RED="$(tput_cmd setaf 1)"
GREEN="$(tput_cmd setaf 2)"
YELLOW="$(tput_cmd setaf 3)"
CYAN="$(tput_cmd setaf 6)"
RESET="$(tput_cmd sgr0)"

should_skip() {
  local name="$1"
  # Always-skip list
  for s in "${SKIP_ALWAYS[@]}"; do
    if [[ "$name" == "$s" ]] && [[ -z "${RUN_EXTENDED:-}" ]]; then
      return 0
    fi
  done
  # ONLY filter: if set, skip anything that doesn't match the glob
  if [[ -n "${ONLY:-}" ]] && [[ "$name" != ${ONLY}* ]]; then
    return 0
  fi
  return 1
}

elapsed() {
  local s=$1
  printf '%dm%02ds' $(( s / 60 )) $(( s % 60 ))
}

# ---- discover scenarios ----------------------------------------------------

mapfile -t SCENARIO_DIRS < <(
  find "$SCRIPT_DIR" -mindepth 2 -maxdepth 2 -name run.sh \
    | sed 's|/run\.sh$||' | sort
)

# ---- run loop --------------------------------------------------------------

PASS_LIST=()
FAIL_LIST=()
SKIP_LIST=()
TOTAL=0

echo "${BOLD}=================================================${RESET}"
echo "${BOLD} bitcoin-multicast-test — run all scenarios${RESET}"
echo " Log dir: $LOG_DIR"
echo "${BOLD}=================================================${RESET}"
echo ""

for sdir in "${SCENARIO_DIRS[@]}"; do
  name="$(basename "$sdir")"
  log="$LOG_DIR/${name}.log"

  if should_skip "$name"; then
    echo "${YELLOW}SKIP${RESET}  $name"
    SKIP_LIST+=("$name")
    continue
  fi

  TOTAL=$(( TOTAL + 1 ))
  printf "${CYAN}RUN ${RESET} %s ... " "$name"

  t_start=$(date +%s)
  # Run with pipefail off for this subshell so a mid-scenario `set -e` trap
  # in the scenario script doesn't abort our loop.
  set +e
  bash "$sdir/run.sh" > "$log" 2>&1
  exit_code=$?
  set -e
  t_end=$(date +%s)
  dur=$(elapsed $(( t_end - t_start )))

  if [[ $exit_code -eq 0 ]]; then
    echo "${GREEN}PASS${RESET}  (${dur})"
    PASS_LIST+=("$name")
  else
    echo "${RED}FAIL${RESET}  (${dur})  — log: $log"
    FAIL_LIST+=("$name")
  fi
done

# ---- summary ---------------------------------------------------------------

echo ""
echo "${BOLD}=================================================${RESET}"
echo "${BOLD} RESULTS${RESET}"
echo "${BOLD}=================================================${RESET}"
echo " Ran:    $TOTAL"
echo " Passed: ${#PASS_LIST[@]}"
echo " Failed: ${#FAIL_LIST[@]}"
echo " Skipped: ${#SKIP_LIST[@]}  (${SKIP_LIST[*]:-none})"
echo ""

if [[ ${#PASS_LIST[@]} -gt 0 ]]; then
  echo "${GREEN}${BOLD}PASSED${RESET}"
  for n in "${PASS_LIST[@]}"; do
    echo "  ${GREEN}✓${RESET} $n"
  done
  echo ""
fi

if [[ ${#FAIL_LIST[@]} -eq 0 ]]; then
  echo "${GREEN}${BOLD}All scenarios passed.${RESET}"
  exit 0
fi

echo "${RED}${BOLD}FAILED${RESET}"
for n in "${FAIL_LIST[@]}"; do
  echo "  ${RED}✗${RESET} $n"
done
echo ""

echo "${BOLD}==============================${RESET}"
echo "${BOLD} FAILURE DETAIL for diagnosis${RESET}"
echo "${BOLD}==============================${RESET}"
echo ""
echo "Lab: LXD — proxy VIP=2001:db8:ffff::1:9000, listeners=fd20::21-23:9200,"
echo "     retry endpoints=fd20::24-26:9300"
echo ""

for n in "${FAIL_LIST[@]}"; do
  log="$LOG_DIR/${n}.log"
  echo "### FAIL: $n"
  echo ""
  # Extract PASS/FAIL assertion lines as a quick summary first.
  echo "--- assertions ---"
  grep -E '^(PASS|FAIL|WARN)\s' "$log" || echo "(no assertion lines found)"
  echo ""
  # Then the full output so metric values and timing context are available.
  echo "--- full output ---"
  cat "$log"
  echo ""
  echo "### END: $n"
  echo ""
done

exit 1
