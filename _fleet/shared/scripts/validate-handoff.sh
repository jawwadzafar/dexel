#!/bin/sh
# Handover gate for the dev-companion subagents.
#
# Runs as a Claude Code SubagentStop hook. Exit 2 blocks the subagent from
# stopping and returns stderr to it as further instructions; exit 0 accepts.
# Registered in .claude/settings.json — do not rename without updating it.
#
# This script is TRACKED (docs/plan/REPO-STRUCTURE-AUDIT.md, D-2). It used to
# live in _fleet/local/scripts/, which .gitignore excludes, so every fresh
# clone shipped a tracked hook aimed at a file that was not there.
#
# The coordination workspace it polices (_fleet/local/) is still deliberately
# untracked: handoffs are session-private working notes, not product. That
# asymmetry is handled below — a checkout with no handoffs directory has no
# run in progress to gate, so the hook reports and accepts instead of
# blocking every subagent in a fresh clone forever.
set -u

payload=$(cat 2>/dev/null || true)

# The hook payload is JSON on stdin; agent_type is a flat string field.
agent=$(printf '%s' "$payload" | sed -n 's/.*"agent_type"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')
[ -n "$agent" ] || exit 0   # not a fleet subagent stop we can attribute

dir='_fleet/local/handoffs'
log="$(dirname "$0")/log-event.sh"

# No handoffs directory => no fleet run was ever opened in this checkout
# (run-dev-companion Phase 0 creates it). Enforcing here would block every
# subagent in a fresh clone on a workspace that does not exist yet, so say
# so once and accept. Once the directory exists the gate is fully strict.
if [ ! -d "$dir" ]; then
    echo "Handover gate: '$dir' does not exist — no fleet workspace in this checkout, so nothing to gate. Create it (run-dev-companion Phase 0) to turn this gate on." >&2
    exit 0
fi
# Telemetry is advisory: a missing or failing logger must never change a gate
# verdict, so every call is guarded and its exit status ignored.
log_event() { [ -f "$log" ] && sh "$log" "$1" "$2" "$3" >/dev/null 2>&1 || true; }
required=''
case "$agent" in
    game-architect) required='Objective|Output format|Sources and tools|Boundaries' ;;
    game-engineer) required='Objective|Output format|Sources and tools|Boundaries' ;;
    pr-reviewer-correctness) required='Objective|Output format|Sources and tools|Boundaries' ;;
    visual-verifier) required='Objective|Output format|Sources and tools|Boundaries' ;;
    game-artist) required='Objective|Output format|Sources and tools|Boundaries' ;;
    pr-reviewer-boundaries) required='Objective|Output format|Sources and tools|Boundaries' ;;
    pr-reviewer-tests) required='Objective|Output format|Sources and tools|Boundaries' ;;
    pr-merge-decider)
        # Terminal agents produce the deliverable, not a handoff file.
        exit 0 ;;
    *) exit 0 ;;   # unknown agent: not ours to police
esac

# Handoff files are named {seq}-{from}-to-{receiver}.md
file=$(ls "$dir"/*-"$agent"-to-*.md 2>/dev/null | head -n 1)
if [ -z "$file" ]; then
    echo "Handover gate: no handoff file found for '$agent' in $dir/." >&2
    echo "Write $dir/{seq}-$agent-to-{receiver}.md using $dir/HANDOFF.template.md before finishing." >&2
    log_event gate_block "$agent" "no handoff file"
    exit 2
fi

missing=''
old_ifs=$IFS
IFS='|'
for section in $required; do
    [ -n "$section" ] || continue
    grep -qi "^#\{1,6\}[[:space:]]*$section" "$file" || missing="$missing $section"
done
IFS=$old_ifs

if [ -n "$missing" ]; then
    echo "Handover gate: $file is missing required section(s):$missing" >&2
    echo "A receiver with zero shared context reads only this file — fill those sections, then finish." >&2
    log_event gate_block "$agent" "missing sections:$missing"
    exit 2
fi

if [ -f '_fleet/local/LEDGER.md' ] && ! grep -q "$agent" '_fleet/local/LEDGER.md'; then
    echo "Handover gate: '$agent' has no row in '_fleet/local/LEDGER.md'." >&2
    echo "Add or update your ledger row (status + artifact path) before finishing." >&2
    log_event gate_block "$agent" "no ledger row"
    exit 2
fi

log_event gate_pass "$agent" ""
exit 0
