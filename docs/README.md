# Dexel documentation — the map

Four layers, each answering a genuinely different question. The failure mode
this index guards against is a fifth layer that restates the other four and
then rots out of sync with all of them. Start here, then go to the layer that
answers your question.

| Layer | Answers | Immutability | Start at |
| --- | --- | --- | --- |
| [`docs/adr/`](adr/README.md) | **Why** we chose this, and what we gave up | Never edited. Superseded only by a new ADR | [the ADR index](adr/README.md) — 20 records |
| [`docs/plan/`](plan/ROADMAP.md) | **What we are going to build next**, in phases | Rewritten per phase; goes stale by design once shipped | [ROADMAP.md](plan/ROADMAP.md), [PRODUCT-EVOLUTION.md](plan/PRODUCT-EVOLUTION.md), [BUGS.md](plan/BUGS.md) |
| [`docs/ui-spec.md`](ui-spec.md) | **How the frontend must be built** — DOM ids, pixel geometry, the WebSocket contract, keybindings | Normative spec; the frontend is checked against it | [ui-spec.md](ui-spec.md) |
| [`docs/game/`](game/README.md) | **How the game works today** — the rules and the numbers a player is actually subject to | Updated in the same commit as any behaviour change | [game/README.md](game/README.md) |

Two more normative design documents sit at this level, deliberately not inside
a subdirectory because so much of the repo cites them by path:

- [`docs/art-direction.md`](art-direction.md) — the palette, the light
  direction, and the pixel-art rules every sprite obeys.
- [`docs/upgrade-design.md`](upgrade-design.md) — the store catalog, item
  slots, and the economy's pricing intent.

## Quick answers

| Question | Go to |
| --- | --- |
| How much is a keystroke worth? | [`game/economy.md`](game/economy.md) |
| What exactly does the activity layer see (and never see)? | [`game/activity-signal.md`](game/activity-signal.md), [ADR 0002](adr/0002-activity-isolation-and-privacy.md), [ADR 0009](adr/0009-app-identity-not-titles.md) |
| What is on screen, and what can the player click? | [`game/surfaces.md`](game/surfaces.md), then [`ui-spec.md`](ui-spec.md) for the contract |
| What changed in the game's behaviour, and when? | [`game/CHANGELOG.md`](game/CHANGELOG.md) |
| What is being built next? | [`plan/ROADMAP.md`](plan/ROADMAP.md) |
| Why is the stack Go + HTML instead of Rust + Bevy? | [ADR 0011](adr/0011-engine-pivot-to-pdf-native-stack.md), and [ADR 0020](adr/0020-archive-the-frozen-rust-track.md) for where those crates live now |
| Why are `app/public/` and `app/assets/` where they are? | [`app/embed.go`](../app/embed.go)'s header, and [`plan/REPO-STRUCTURE-AUDIT.md`](plan/REPO-STRUCTURE-AUDIT.md) §4 |

## `dev_docs/` — the other directory, and why it is separate

[`dev_docs/`](../dev_docs/) holds in-flight **engineering investigations**,
not product/design material. Two audiences, two directories:

- [`dev_docs/production-runtime/`](../dev_docs/production-runtime/ARCHITECTURE.md)
  — the release/packaging/platform engineering (install, update, uninstall,
  per-OS paths, the release pipeline).
- [`dev_docs/rust-parallel/`](../dev_docs/rust-parallel/CONTRACT.md) and
  [`dev_docs/rust-port-evaluation.md`](../dev_docs/rust-port-evaluation.md) —
  the **experimental** Rust port of the current product (`app-rs/`) and its
  888 KB of captured WebSocket goldens, kept for a later decision
  ([`plan/ROADMAP.md`](plan/ROADMAP.md), RUST-PARALLEL track). Not the frozen
  Bevy game — that is [ADR 0020](adr/0020-archive-the-frozen-rust-track.md).

## Historical layer (v0.1, Rust/Bevy era)

Kept because history is cheap and the SHAs in them are still resolvable.
**None of these is a current plan:**
[`implementation-plan.md`](implementation-plan.md),
[`milestone-log.md`](milestone-log.md), [`pr-log.md`](pr-log.md). Each carries
a banner saying so.
