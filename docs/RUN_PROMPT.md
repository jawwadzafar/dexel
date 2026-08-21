# Standing run brief for the dev-companion fleet

This file exists so invoking the fleet doesn't require re-pasting the full
brief every time — just point the orchestrator at this file:

```
/run-dev-companion read docs/RUN_PROMPT.md and follow it exactly
```

## Standing operating rule (user-mandated, permanent)

**The main/overseer session (Fable) ORCHESTRATES ONLY.** All implementation —
code, art, docs content — is done by parallel subagents (Sonnet for
implementation volume, Opus for design judgment and review), each with
exclusive ownership of its files. The overseer reads, briefs, verifies,
resolves ownership conflicts, and commits. Tiny fixes are not an exception;
they get batched into agent briefs.

## The brief

Implement `docs/implementation-plan.md`, milestones M0 through M5, all in one
run. The architect phase is already complete — read
`_fleet/local/handoffs/01-game-architect-to-game-engineer.md` and
`_fleet/local/LEDGER.md` (task 1 is done) — skip the Architecture phase.

Read `_fleet/local/LEDGER.md` before doing anything else — it has the real
current state, including a naming-collision bug that made an earlier "M1
backfill review" accidentally re-review M0 instead (now fixed in
`pr-review-lens`; two stale handoff files are renamed with a
`.stale-was-actually-m0` suffix so they aren't mistaken for real M1 verdicts).
As of this writing: M0 merged and fully reviewed. M1 merged without the
reviewer pipeline, and only `pr-reviewer-tests` has genuinely reviewed it —
**correctness and boundaries review for M1/PR #2 is still outstanding**, do
that before M2.

This whole implement-review-merge cycle is ONE orchestrator phase
("Milestone cycle") with a single loop spanning all six milestones (max 30
passes) — it does not stop and wait after M0 merges. If any single pass
stalls (an agent announces a plan and stops without finishing), re-invoke
that same phase's agent with exactly what's missing appended, per the
orchestrator's own loop rule — check `docs/milestone-log.md`/`docs/pr-log.md`
for real evidence of progress before assuming a pass is done, never trust an
agent's self-report alone.

Two models across the fleet's tiers (`fleet.yaml`'s `defaults.opencodeModels`),
both proven by completing real agentic work in this repo — **not** by a
synthetic probe. That distinction cost us a milestone's worth of retries: a
model can pass a one-shot tool-call probe and answer a reasoning question
perfectly, then still fail as an agent, because a multi-turn loop with real
tools is a different capability. Before trusting a new candidate with a role,
let it complete one real review or milestone end to end.
**All tiers currently -> `Qwen/Qwen3.8-27B`** (every agent). It is the only
model right now that is both proven on real agentic work here and reachable.

**DeepSeek V4 Flash is DOWN** (as of 2026-08-20) and was the cause of the
`AI_APICallError: <none>` stall: `POST /v1/chat/completions` returns
`http_code=000` — no response at all, 30s+ timeouts, reproduced twice — even
for a 10-token "say OK", while `GET /v1/models`, Qwen3.8-27B, and Nia-1.0 all
answer in ~1s. `<none>` literally means "no response body to report"; opencode
then retries on a ~4-minute timeout loop and the fleet makes no progress. When
it recovers, prefer restoring DeepSeek to `cheap` (pr-reviewer-tests) so the
test check runs on a *different* model than the implementer — that
independence is deliberate and is lost while everything shares one model.
Re-verify with a real chat call first; `/v1/models` listing it proves nothing.

Fallback if Qwen3.8 also goes down: `infiniatechnologies/Nia-1.0` responds
(~0.9s) and passes a tool_calls probe, but has never completed a real review
or milestone here — unproven as an agent, per the rule above.

Qwen3.8-27B notes (applies to every agent now). **Requires
  `chat_template_kwargs: {"enable_thinking": true→false}` in
  `~/.config/opencode/opencode.jsonc`'s model entry, already set.** Without
  it, this model burns its entire token budget on hidden reasoning and
  returns EMPTY content on anything past trivial one-shot tool calls. Tested
  exhaustively — reasoning scales to fill whatever budget it gets and never
  reaches an answer, and above ~8k tokens the request outlives the gateway:
  | max_tokens | time | reasoning | answer |
  | 2,000 | 23s | 6.8k chars | empty (finish_reason=length) |
  | 3,000 | 34s | 11.2k chars | empty (length) |
  | 4,000 | 43s | 14.7k chars | empty (length) |
  | 8,000 | 86s | 28.3k chars | empty (length) |
  | 15,000 | 125.7s | — | **HTTP 524 gateway timeout, no response** |
  | thinking off | 4-7s | 0 | correct |
  So there is no budget that works: below ~8k it runs out mid-reasoning,
  above that the Cloudflare gateway (~120s ceiling) kills the request first.
  `reasoning_effort: low` is ignored by this vLLM deployment. Don't retry
  this — with thinking disabled it's fast (~4s) and correct. If you ever add
  another Qwen3.x-family model, check for the same issue.
**`google/gemma-4-31B-it` is rejected — do not reintroduce it.** It passed a
single-shot probe (1.3s, correct, concise) and was pinned to `smart` on that
basis. It then FAILED AS AN AGENT: both `pr-reviewer-correctness` and
`pr-reviewer-boundaries` running on it returned "partial tool calls only"
(empty response, surfacing as an error with no message) twice each during
M2, and the orchestrator had to run both reviews itself to get past it.
Removed from `opencode.jsonc`'s models map as well.

No gemma, no gpt-oss, no Qwen3-Omni-30B-A3B-Instruct, no Fara1.5-27B, no
Qwen3.6-27B-FP8 — all four confirmed broken (either tool-calling disabled
server-side, malformed tool output, or the model doesn't exist through the
gateway despite being listed).

**opencode does not hot-reload agent/model config** — restart it (a genuinely
new session, not resumed) after any `fleet.yaml` or `opencode.jsonc` change
before relying on any agent. A resumed session keeps whatever model was
cached at its creation regardless of later config changes; only brand-new
subagent sessions it spawns pick up current config.

**There is no automatic model-fallback-on-failure** — opencode's config
schema has no such mechanism (checked directly against its schema). If a
pinned model starts failing, the fix is manual: edit the model in
`fleet.yaml`'s `opencodeModels` (or per-model `options` in
`opencode.jsonc` for provider-level quirks like the one above), then rebuild
the harness (`fleetsmith build fleet.yaml --target all --force`, then delete
`.goose/`) and restart opencode fresh. There's no way to make this automatic
within opencode itself — don't assume a stalled agent will self-heal onto a
different model.

For each milestone, in order, run the full cycle before starting the next:

1. **game-engineer** implements the milestone on its own branch
   (`milestone/m<N>-<slug>`), commits as it goes, runs the full command
   sequence from the plan's §5, appends a `docs/milestone-log.md` entry, pushes,
   and opens a PR against `main`.
2. **pr-reviewer-correctness**, **pr-reviewer-boundaries**, and
   **pr-reviewer-tests** each independently review that PR — in their own
   isolated git worktree, never the shared checkout — and each write their
   own verdict handoff to `pr-merge-decider`. They run concurrently and never
   coordinate with each other.
3. **pr-merge-decider** waits for all three verdicts, applies the decision
   rule (a boundaries veto blocks outright; otherwise 2-of-3 approval
   merges), and either merges the PR (`gh pr merge --squash --delete-branch`)
   or posts a consolidated change request back to game-engineer on the same
   branch/PR.
4. Only once the PR is merged does game-engineer branch for the next
   milestone.

Do not stop for confirmation at any point in this cycle — commits, pushes,
opening PRs, independent reviews, and merging on approval all happen without
per-action human confirmation. This project runs under standing
authorization for that (see `docs/pr-log.md`'s and the `pr-merge-decision`
skill's notes on this — it isn't a one-time approval, it's the standing
policy for this repo).

Only stop early if a milestone genuinely cannot be completed as scoped, or a
PR gets stuck in repeated Request-changes cycles — in that case, record the
blocker explicitly in `docs/milestone-log.md` or `docs/pr-log.md` and stop
there rather than skipping ahead. Do not introduce architecture beyond the
plan's §3, and do not let two milestones' branches be open unreviewed at the
same time.

## Repo

`origin` is `git@github.com:jawwadzafar/dev-companion.git` (private). All
commits, branches, and PRs go there under the current git identity
(`git config user.name`/`user.email`) and the `gh` CLI's already-authenticated
account — no credentials need to be supplied by hand.
