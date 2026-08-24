#!/bin/sh
# Run telemetry for the dev-companion subagents.
#
# Usage: sh _fleet/shared/scripts/log-event.sh <event> [agent] [detail]
#
# Appends one JSON line to _fleet/local/runs/<run_id>/events.jsonl. Event names follow
# OpenTelemetry GenAI conventions so these records can be exported later.
#
# The script is tracked (_fleet/shared/), the data it writes is not
# (_fleet/local/ is gitignored) — run logs are one machine's session history,
# never product. Hence the ../local/runs path below rather than ../runs.
#
# Fail-soft by contract: telemetry never blocks a run. Every path exits 0.
set -u

event=${1:-}
agent=${2:-}
detail=${3:-}
[ -n "$event" ] || exit 0

runs_dir=$(dirname "$0")/../../local/runs
mkdir -p "$runs_dir" 2>/dev/null || exit 0

# Runs are namespaced by actor so that two developers sharing a checkout do not
# interleave into one log, and so health metrics can tell "this agent fails for
# everyone" from "it fails for one person's setup" — only the first is a
# harness defect.
actor=${FLEETSMITH_ACTOR:-}
[ -n "$actor" ] || actor=$(git config user.email 2>/dev/null | sed 's/@.*//')
[ -n "$actor" ] || actor=${USER:-unknown}
actor=$(printf '%s' "$actor" | tr -c 'A-Za-z0-9._-' '-')
current="$runs_dir/CURRENT-$actor"

# run_start mints the id; every other event joins that actor's run in progress.
# A run that was never opened (someone invoked an agent directly) still gets a
# home rather than losing its events.
if [ "$event" = "run_start" ] || [ ! -f "$current" ]; then
    run_id="$actor-$(date -u +%Y%m%dT%H%M%SZ 2>/dev/null || echo unknown)"
    printf '%s' "$run_id" > "$current" 2>/dev/null || exit 0
fi
run_id=$(cat "$current" 2>/dev/null) || exit 0
[ -n "$run_id" ] || exit 0

out_dir="$runs_dir/$run_id"
mkdir -p "$out_dir" 2>/dev/null || exit 0

# Flatten and escape for JSON: backslash, quote, tab, then newlines to spaces.
esc() {
    printf '%s' "${1:-}" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g' -e 's/	/ /g' | tr '\n' ' '
}

ts=$(date -u +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || echo unknown)
printf '{"ts":"%s","run_id":"%s","event":"%s","agent":"%s","detail":"%s"}\n' \
    "$ts" "$(esc "$run_id")" "$(esc "$event")" "$(esc "$agent")" "$(esc "$detail")" \
    >> "$out_dir/events.jsonl" 2>/dev/null || exit 0

# run_end closes the run so the next run_start opens a fresh id.
[ "$event" = "run_end" ] && rm -f "$current" 2>/dev/null

exit 0
