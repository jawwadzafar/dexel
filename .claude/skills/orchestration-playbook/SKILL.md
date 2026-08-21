---
name: orchestration-playbook
description: |
  How the dev-companion project is actually run, for a future overseer
  session: the overseer orchestrates only and subagents implement with
  EXCLUSIVE file ownership (the collision lessons this repo already paid
  for), every agent's "done" is re-verified from a clean build/cache before
  it's trusted (agents have reported green off a stale cache while the tree
  didn't actually build), the plan and its event log live in the repo under
  docs/plan/ (not only in chat), commit+push happens per landing, and the
  model/provider realities recorded from the opencode-fleet era (Qwen
  thinking-off, gemma's canned critiques, a DeepSeek outage) — noted as
  history now that the critical path is Claude subagents. Use when acting as
  the overseer/orchestrator for the dev-companion fleet: planning waves,
  briefing subagents, gating merges, or resuming a session after a break.
x-fleetsmith-origin: human
---

# Orchestration playbook

This project has run two different orchestration substrates: an opencode
fleet of named agents (`fleetsmith`, model-pinned, `_fleet/` workspace — v0.1
through the start of v0.4) and, since ADR 0011's engine pivot, **Claude
subagent orchestration**, which "has outperformed it throughout" (ADR 0011).
Everything below is the standing operating rule regardless of substrate;
the model-specific notes at the end are opencode-era history, kept because
the next provider swap will rediscover the same failure modes.

## The standing rule

**The overseer session orchestrates only. It never implements.** All code,
art, and docs content is done by parallel subagents, each with **exclusive
ownership of its files** for the duration of its task. The overseer reads,
briefs, verifies, resolves ownership conflicts, and commits.
`docs/RUN_PROMPT.md`: "Tiny fixes are not an exception; they get batched
into agent briefs." Don't make a one-line edit yourself because delegating it
feels like overhead — the discipline is the point.

## Exclusive file ownership — the collision lessons

Two different collision bugs already cost this project real time:

- **Ownership seams left ambiguous between two agents working the same wave.**
  Wave 2 briefly had `app/public/` unclear between the Go-core agent and the
  frontend agent; the overseer had to resolve it explicitly before either
  could be trusted ("Ownership seam resolved: app/public/ belongs to W2-β,
  not W2-α" — `docs/plan/ORCHESTRATION-LOG.md`). **State the file/directory
  ownership boundary in the brief, explicitly, before dispatching**, not
  after a diff conflict shows it was ambiguous.
- **A naming collision in review artifacts made one milestone's backfilled
  review accidentally re-review the wrong milestone** (an M1 backfill review
  silently re-reviewed M0 because of a handoff-file naming collision;
  `docs/RUN_PROMPT.md`). The fix that stuck: handoff/log filenames encode
  enough identifying detail (milestone number, PR number, from/to agent) that
  two different pieces of work can never collide under the same name, and a
  stale file gets an explicit `.stale-was-actually-<x>` suffix rather than
  being silently overwritten or mistaken for current.

When briefing parallel agents: write down who owns which paths, in the brief,
before launching them in the same message. If two agents' scopes might
overlap even slightly, resolve it before dispatch, not after both report done.

## Never trust "done" without independent, clean-cache re-verification

An agent's own self-report is not evidence. This project's actual policy
(`docs/RUN_PROMPT.md`): "check `docs/milestone-log.md`/`docs/pr-log.md` for
real evidence of progress before assuming a pass is done, never trust an
agent's self-report alone." Concretely:

- **Reviewers work in their own isolated git worktree, never the shared
  checkout**, and never coordinate with each other — independence is the
  point of running three reviewers instead of one.
- **The merge gate re-runs everything from a clean cache itself** before
  merging: PR #9's merge gate is recorded as "independent Opus merge-gate
  reproduced both blocker fixes live + clean-cache green" — not "the
  engineer said tests pass." An agent has reported a green build that was
  actually stale-cache green while the real tree did not build; the fix is
  structural (always re-verify from a fresh clone/worktree/cache), not
  "ask the agent to double check."
- **Visual/UX claims get the same treatment** — see the `feature-build-and-verify`
  skill's GATE: render the real product yourself and look, don't trust a
  description of what it should look like.
- A PR/milestone is "landed" only once the overseer (or the designated
  independent reviewer) has reproduced its claimed evidence, not once the
  implementing agent says so.

## Keep the plan and its log in the repo, not only in chat

`docs/plan/v0.4-behind-view-plan.md`'s own opening: "the orchestration plan
previously lived only in the overseer's conversation. A session dying = the
plan dying, and agents diverging. This document IS the plan." Concretely for
any future orchestration:

- A living plan doc (wave structure, ownership table, exit criteria, a status
  board that is **appended to, never rewritten** — "append, don't rewrite
  history") under `docs/plan/`.
- An append-only event log (`docs/plan/ORCHESTRATION-LOG.md`'s pattern): one
  line per agent launched/landed, verdict, commit, or plan change, so "any
  future session can reconstruct WHERE THE PROJECT IS from the repo alone."
- Every agent brief references specs **by path** (`docs/ui-spec.md` §4, not a
  pasted excerpt) so the spec and the brief cannot drift apart silently.
- ADRs (`docs/adr/000N-*.md`) for any decision with lasting consequences,
  each with Context / Decision / Consequences — see any existing ADR for the
  shape. A plan document records *what's happening now*; an ADR records *why
  a decision was made*, permanently.

## Commit and push per landing

Each wave/milestone lands as its own commit (or PR + merge, when the PR
ceremony is in force); don't batch unrelated waves into one commit. The v1
fix-wave restored full PR ceremony deliberately ("restoring the PR ceremony
trunk-work bypassed for velocity") once the project's risk profile called for
it — velocity-mode direct-to-main commits are a deliberate, temporary choice
for early/low-risk phases, not a permanent default once real users depend on
`main`.

## Gate order that actually worked here (v0.4, Wave 2/3)

1. Foundations in parallel with disjoint ownership (mechanics vs. spec
   translation).
2. Build on the target stack in parallel, disjoint ownership, cross-agent
   contracts pinned in a spec doc *first* (the WS contract bound the backend
   and frontend agents; the sprite manifest bound the frontend and art
   agents — "Deviations go through the overseer, spec updated FIRST").
3. **Composed verification** — the overseer runs the real, fully-assembled
   product (not any one agent's piece of it) and judges it against the
   original source of truth (the user's design PDF, or the spec). This step
   found real defects fully-green unit tests missed (a broken character
   silhouette, an unformatted float, a clipping button) and dispatched
   targeted fixes before re-verifying — do not skip straight to "ship" on the
   strength of per-agent green.
4. Adversarial read-only review pass (an agent whose only job is finding
   problems, not fixing them) before the fix wave, so review and fix stay
   independent.
5. Fix wave on its own branch/PR, independently re-verified by the merge
   gate, then merged.

## Model/provider realities — opencode-fleet era, kept as history

These applied when the fleet ran on opencode's model-pinned agents
(`fleet.yaml`), before the pivot to Claude subagent orchestration. Kept
because the failure *shapes* — not the specific model names — will recur with
any future provider swap:

- **A model passing a one-shot tool-call probe proves nothing about it
  surviving as an agent.** `docs/RUN_PROMPT.md`: "a model can pass a
  synthetic probe... and answer a reasoning question perfectly, then still
  fail as an agent, because a multi-turn loop with real tools is a different
  capability." Before trusting a new candidate with a role, let it complete
  one real review or milestone end to end — not a probe.
- **`google/gemma-4-31B-it` was rejected after passing its probe** because it
  then returned empty "partial tool calls only" responses as a real
  multi-turn reviewer agent, twice, during a real milestone.
- **A reasoning-mode model can silently starve itself of an answer.** A
  Qwen3.x deployment burned its entire token budget on hidden reasoning and
  returned empty content on anything past trivial calls — worse at *higher*
  token budgets (more room to keep reasoning, never converging), until
  `enable_thinking: false` was set explicitly; there was no working budget
  with thinking left on. If a future reasoning model behaves strangely on
  real agentic tasks, check whether its thinking/reasoning mode can be
  disabled before assuming it's simply unfit for the role.
- **A vision model can return canned, image-independent answers to
  open-ended critique prompts while answering closed yes/no questions about
  the same image correctly** (`scripts/visual-check.py`'s own comment on
  `gemma`'s behavior). If a vision model's open-ended critique reads
  suspiciously generic or repeats verbatim across different images, verify
  with a closed question, or trust your own eyes if you have vision.
- **An upstream outage (DeepSeek V4 Flash going down) stalled the fleet on a
  4-minute retry loop** before anyone noticed the model, not the code, was
  the problem. Re-verify a suspected-down model with a real chat call, not a
  `/v1/models` listing (a model can be *listed* while its inference endpoint
  is dead).
- **Config changes are not hot-reloaded** in an agent-runtime session — a
  resumed session keeps whatever model/config was cached at its creation;
  only a genuinely new session picks up a config change.

None of the model-specific names above apply to the current Claude-subagent
critical path; the durable lesson is: verify a model/agent's fitness for a
role with real end-to-end agentic work, not a probe, and re-verify after any
substrate or config change rather than assuming yesterday's proof still holds.
