---
description: Status of the dev-companion fleet
agent: run-dev-companion
---

Report the current state of the dev-companion fleet.

Handoffs written so far:

!`ls -1 _fleet/local/handoffs/*.md 2>/dev/null | grep -v HANDOFF.template || echo "(none yet)"`

Ledger:

@_fleet/local/LEDGER.md

From that state: say which phase the fleet is in, which agents have finished, what is outstanding, and anything that looks stalled or contradictory. Do not start any fleet work — this is a status report.
