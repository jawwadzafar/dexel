# Repo structure & dead-file audit — dexel

**Date:** 2026-08-24 · **HEAD at audit:** `f920e4b` ("desktop: first Linux build") ·
**Author:** structure-audit agent · **Status:** **EXECUTED 2026-08-24** —
Phases 0-5 landed on `main` (see below); Phase 6 (the `app/main.go` split) is
deliberately NOT done. The document is left exactly as written, as the record
of what was decided and why, so every path it names as a deletion target is
still named here — those files now live on branch `attic/legacy-rust-and-fleet`,
not in this tree, which is why a repo-relative link check reports them missing
from `main`. Execution notes, including the three places reality differed from
this plan, are at the end of §6.
**Owner's brief:** *"evaluate files that are not needed and improve code structure
as industry standard — but don't break anything."*

This document is **read-only analysis plus an execution plan**. No file was
deleted, moved, or committed to produce it. Every claim below carries the
command that produced it, so a future session can re-derive rather than
re-litigate.

---

## 0. Executive summary — the five things that actually matter

| # | Finding | Size | Risk to fix |
| --- | --- | --- | --- |
| **1** | **`.git` holds ~1.70 GiB of local-only garbage.** A local commit (`c6c311bf`, 2026-08-20) accidentally committed `target-m3-review/debug/` — three unstripped Bevy debug ELF binaries, 5.4 GB uncompressed. It was reset away, never pushed, and survives only in this machine's reflog, keeping a 699 MB packfile alive. **A fresh `git clone` of this repo is 2.6 MiB.** | **1.70 GiB** | **None** — local-only, does not touch history, origin, or anyone's clone |
| **2** | ~73.4 GiB of gitignored build residue on disk, including a `target/` at 55 GiB and an abandoned `target-m3-review/` at 8.8 GiB. | **73.4 GiB** | None — all gitignored, all regenerable |
| **3** | **The frozen Rust/Bevy track has no live consumer left.** ADR 0011 froze it as "the legacy-save import source"; review item B-2 then **deleted the importer entirely** (`app/internal/store/legacy.go` is gone; `app/main.go:1419` documents the deletion). `build.yml`'s `build` job still compiles it. | 495 KB tracked | Low code risk / **needs an owner call** (ADR 0011 says "not deleted") |
| **4** | **The opencode fleet harness is dead but still tracked**, pinning models that no longer exist (`tokenfactory/Qwen/Qwen3.8-27B`). Worse: `.claude/settings.json` (tracked, live) registers a `SubagentStop` hook pointing at `_fleet/local/scripts/validate-handoff.sh`, which is **gitignored** — a fresh clone has a tracked hook aimed at a missing file. | 128 KB tracked | Low — but the hook and `CLAUDE.md` need care |
| **5** | **CI has been 100% `startup_failure` for 100/100 runs since 2026-08-21T17:42.** Nothing in this plan can be gated by CI. Every gate below must run locally. | — | Blocking for the *gating strategy*, not for the cleanup |

**Honest framing on "reclaimable size":** the two big numbers (1.70 GiB + 73.4 GiB
= **~75.1 GiB**) are **local disk on this machine only**. The tracked-tree
reduction from every deletion recommended here is **~661 KB out of 5.35 MiB
(12%)**, and the *clone* size effect is **zero** — git history retains the blobs,
and we are explicitly **NOT** rewriting history. A fresh clone is 2.6 MiB today
and will still be 2.6 MiB afterwards. Anyone who justifies this cleanup by
"shrinking the repo" is wrong; the justification is **legibility** — a future
session should not have to work out which of two `activity/` directories is real.

---

## 1. Method — what was actually verified

Measurements:

```bash
git count-objects -vH                                   # 1.03 GiB loose + 668 MiB pack
git ls-tree -r -l HEAD | ...                            # tracked bytes per top-level path
git clone --no-local <repo> /tmp/clonetest && du -sh .git   # => 2.8M  <- true history size
git verify-pack -v .git/objects/pack/*.idx | sort -rn   # => three 1.2-1.5 GB blobs
git rev-list --objects --all --reflog | grep <oid>      # => reachable ONLY via reflog
git ls-tree -r c6c311bf | grep <oid>                    # => target-m3-review/debug/companion
du -sc target target-m3-review build ...                # ignored residue
```

Reachability (each verdict below cites its own grep):

```bash
git grep -n <path>              # inbound references, tracked files only
git status --porcelain --ignored=matching -uall   # ignored-vs-untracked (untracked list is EMPTY)
gh run list --limit 100 --json conclusion        # 100/100 startup_failure
gh api repos/.../actions/runners                 # runner "jwdlab-runner" ONLINE, labels correct
```

Gates re-proven green at `f920e4b` before proposing them as gates
(`PATH=/home/darkmirror/go-toolchain/go/bin:$PATH`,
`PATH=/home/darkmirror/.nvm/versions/node/v24.16.0/bin:$PATH`):

| Gate | Result |
| --- | --- |
| `cd app && go vet ./...` | clean |
| `cd app && go test ./...` | 8/8 packages `ok` |
| `bash scripts/test-race.sh` | **all `ok`** — picked `CC=gcc (/usr/bin/gcc)`; SF-3 is resolved *on this box* |
| `cd app && go build -o /tmp/dexel-probe .` | OK, 18,676,983 bytes |
| `cd app/frontend && npm run typecheck && npm run build` | clean; bundle 56.8 kb + map 258.5 kb |
| `git diff --exit-code -- app/public/js/dexel.js app/public/js/dexel.js.map` | **no drift** — committed bundle reproduces byte-for-byte |
| `cargo metadata --no-deps` in `/`, `desktop/src-tauri`, `app-rs` | all three workspaces resolve |
| `cd desktop/src-tauri && cargo check` | **FAILS on this box** — `libdbus-1-dev`/`pkg-config` missing (environment gap, not a repo defect; the desktop CI job targets a `desktop-linux` runner) |
| doc-link sweep (127 distinct repo-relative paths in `*.md`) | **15 already missing** — baseline recorded in §7 |

---

## 2. Verdict table — every top-level path

Tracked size = bytes in `HEAD`'s tree. "Reached by" = evidence of a live
consumer (build, CI, runtime, script, skill, or doc that a human follows).

### 2.1 The product — KEEP, do not touch

| Path | What it is | Tracked | Reached by | Verdict |
| --- | --- | --- | --- | --- |
| `app/` | **The product.** Go module `github.com/jawwadzafar/dexel/app`. 15 non-test `.go` files in `package main` (~3.2 kloc) + 8 `internal/` packages (~22 kloc). | 2.06 MiB / 228 files | Everything. `go build`, all 3 CI jobs, `scripts/build-release.sh`, `scripts/build-sidecar.sh`, the Tauri sidecar | **KEEP** (see §4 for the structure question) |
| `app/public/` | The served frontend: `index.html`, `css/nes.min.css`, `css/game.css`, `fonts/PressStart2P.woff2`, `js/dexel.js`, `js/dexel.js.map` | 692 KB / 6 files | `app/embed.go` go:embed; `index.html` links both CSS; `game.css:21` loads the woff2 | **KEEP — zero dead files.** Every one of the six is referenced. The `.map` is deliberately committed (CI drift check diffs it) and deliberately **not** embedded (N-9) |
| `app/assets/` | 92 sprite/thumbnail PNGs, generated by `tools/gen_assets.py` | 372 KB / 92 files | `app/embed.go` `//go:embed all:assets`; `internal/assets`; `internal/game` catalog tests | **KEEP — untouchable** |
| `app/frontend/` | TypeScript source of the committed bundle + esbuild config | 34 MB on disk (`node_modules` ignored); 31 tracked files | CI `frontend` job; `app/frontend/README.md`; the drift check | **KEEP.** `src/dev/dev-fixtures.ts` + `dev-tools.ts` are reachable from `main.ts` — not dead |
| `app/internal/` | 8 packages: activity, assets, autostart, engine, game, lifecycle, paths, store | 1.1 MiB | `app/` | **KEEP** |
| `desktop/` | Tauri v2 shell (ADR 0015). `Cargo.toml` header: "STATUS: BUILT" — bundles on macOS arm64 (2026-08-23) and Linux (HEAD commit, 2026-08-24) | 249 KB / 16 files | `.github/workflows/desktop.yml` (4 jobs); `README.md`; `scripts/build-sidecar.sh`; `docs/plan/F3-design.md`, `RUN-MODES.md`, `TAURI-FIRST-BUILD.md` | **KEEP.** `desktop/dist/index.html` is an intentional placeholder `frontendDist` (documented in-file); `icons/icon.png` is the 1024 master `tauri icon` re-derives from — **all six icon files are live** |
| `scripts/build-release.sh` | The release pipeline | 22 KB | `release.yml`; `app/version.go`; `app/main_test.go`; 6 docs | **KEEP** |
| `scripts/build-sidecar.sh` | Cross-compiles the Go server into Tauri sidecars | 9 KB | `desktop.yml`; `README.md`; `desktop/src-tauri/Cargo.toml` | **KEEP** |
| `scripts/visual-check.py` | Vision-model screenshot judge | 6 KB | 5 live `.claude/skills/*` + `.claude/agents/game-artist.md` | **KEEP** |
| `tools/gen_assets.py` | The art generator — `ASSETS = REPO/"app"/"assets"` | 141 KB | `pixel-art-authoring` skill; ADR 0004; README | **KEEP** |
| `tools/gen_icon.py` | Icon generator — imports `gen_assets`, writes `desktop/src-tauri/icons/` | 32 KB | `desktop/README.md:373` | **KEEP** |
| `.claude/skills/` | 10 skills; `feature-build-and-verify`, `add-a-menu-modal`, `orchestration-playbook`, `pixel-art-authoring` are current | 106 KB / 26 files (incl. `agents/`) | Claude Code loads them; they are the live working method | **KEEP** (3 need content updates — §2.4) |
| `README.md`, `LICENSE`, `THIRD-PARTY-LICENSES.md` | Product + licensing | 35 KB | `scripts/build-release.sh` packages all three into every archive | **KEEP** |
| `.github/workflows/{go,frontend jobs},desktop.yml,release.yml` | CI | 40 KB | GitHub (currently failing to start — §0/#5) | **KEEP** |
| `docs/adr/` (19 ADRs + README) | Decision record. Immutable by design | 140 KB | `docs/adr/README.md` indexes all 19; README links 6 | **KEEP — never delete an ADR.** Low inbound-link counts on individual ADRs are correct: the index is the entry point |
| `docs/game/` | "How Dexel works today", derived from the Go source (commit `715a109`) | 176 KB / 10 files | **Nothing links it** — but it is the newest and most accurate doc layer | **KEEP + fix the orphaning** (§7) |
| `docs/{ui-spec,art-direction,upgrade-design}.md` | Normative specs | 190 KB | `ui-spec.md`: **144 refs across 56 files**; `art-direction.md`: 58/26; `upgrade-design.md`: 74/21 | **KEEP — do not move** (§5) |
| `docs/plan/` | 14 live design docs + logs | 392 KB | `ROADMAP.md`: 48 refs/37 files; `P2-design.md`: 81/33 | **KEEP as-is** (§5) |
| `docs/images/` | 3 README screenshots | 68 KB | `README.md:5,45` | **KEEP** |
| `dev_docs/production-runtime/` | Production runtime architecture + platform notes + release pipeline | 184 KB | `ARCHITECTURE.md` 17 refs/13 files; `MIGRATION_PLAN.md` 34/21 | **KEEP** |
| `app-rs/` + `dev_docs/rust-parallel/` | The experimental Rust port (ROADMAP "RUST-PARALLEL track", declared **EXPERIMENTAL** by the owner 2026-08-22) | 23 KB + 888 KB | `ROADMAP.md:260,264`; `dev_docs/rust-port-evaluation.md` | **KEEP by owner decision.** `.gitignore` **already covers** `app-rs/target/` (via `**/target/`) — verified with `git check-ignore` |
| `.gitignore` | | 1.6 KB | git | **KEEP** — and it is *complete*: `git status --porcelain -uall` shows **zero** untracked-not-ignored files |

### 2.2 The frozen Rust/Bevy legacy — ARCHIVE (owner call required)

**The evidence that its last consumer is gone:**

- `app/internal/store/legacy.go` — **does not exist.** `ls app/internal/store/` returns 15 files, none of them `legacy.go`.
- `app/main.go:1419-1436` is now a tombstone comment: *"B-2 … this is where the legacy-Rust import used to live, and it is deliberately gone … It is deleted rather than clamped because there is nobody to migrate: the Rust/Bevy build's only public artifact (v0.1.0) has a single download."*
- `app/internal/store/store.go:265` — `ImportedFromRust`/`ImportedAt` are documented as **VESTIGIAL**: *"the legacy-Rust import that was the only thing that ever set them is deleted. Nothing sets them any more; nothing new should."*
- `.github/workflows/build.yml:47` still runs `cargo build --release -p companion` on every push, described in its own header as *"Kept building so the one-time legacy-save import path always has a real binary to produce a save with"* — a purpose that no longer exists.

| Path | What it is | Tracked | Reached by | Verdict |
| --- | --- | --- | --- | --- |
| `companion/` | The Bevy game crate (`lib.rs` 158 KB, `scene.rs` 69 KB) | 239 KB / 5 files | Root `Cargo.toml` member; `build.yml`'s `build` job; `tools/shotcap` | **ARCHIVE** to branch `legacy-rust` |
| `activity/` (root) | The Bevy-free input crate. **Not** `app/internal/activity/` | 102 KB / 4 files | `companion/Cargo.toml` only | **ARCHIVE** |
| `Cargo.toml` / `Cargo.lock` (root) | Workspace = `["activity","companion","tools/shotcap"]` | 150 KB / 2 files | the three members | **ARCHIVE** |
| `tools/shotcap/` | In-process Bevy framebuffer capture. `Cargo.toml`: `companion = { path = "../../companion" }` | 3.7 KB / 2 files | `.claude/skills/visual-verification/SKILL.md:31` (`cargo run -p shotcap`) — **and that skill is itself superseded** by `feature-build-and-verify`'s Playwright gate on the Go app | **ARCHIVE** with the track; **rewrite the skill** |
| `build.yml`'s `build` job (~75 lines) | Builds `companion` for 2 targets on every push | — | nothing consumes the artifact | **DELETE the job** |
| `target/`, `target-m3-review/`, `build/`, `tools/shotcap/target/` | Untracked, gitignored build residue | **0 tracked** | — | **DELETE from disk** (73.4 GiB) |

**Total tracked:** 495 KB / 13 files. **Risk of being wrong:** the *only* scenario
that hurts is ADR 0011's stated exit route — *"The Rust legacy build is retained
precisely for that exit"* (if the web stack disappoints). Archiving to a branch
**preserves** that exit (`git worktree add ../legacy-rust legacy-rust` restores a
working tree in seconds) but **contradicts the ADR's literal wording** ("Not
deleted"). → **Owner decision D-1, and it needs a superseding ADR 0020.**

### 2.3 The opencode fleet harness — ARCHIVE

The fleet was abandoned 2026-08-21 (*"i've stopped opencode now you do finish"*);
ADR 0011 records *"The fleetsmith/opencode fleet is likewise retired from the
critical path (files kept)"*.

| Path | Tracked | Reached by | Verdict |
| --- | --- | --- | --- |
| `.opencode/agents/` (9) + `commands/` (2) | 46 KB / 11 files | `AGENTS.md:9` only. Each file pins a **dead model**: `model: tokenfactory/Qwen/Qwen3.8-27B`, `mode: subagent`, opencode-only `permission:` blocks. Near-duplicates of `.claude/agents/*` (same prose, different frontmatter — both fleetsmith outputs) | **ARCHIVE** to `attic/fleet-harness` |
| `fleet.yaml` | 37 KB / 1 | `docs/RUN_PROMPT.md`; `orchestration-playbook` (as *history*); its own header (`fleetsmith build fleet.yaml`) | **ARCHIVE** |
| `opencode.json` | 267 B | nothing tracked | **ARCHIVE** |
| `.agents/checks/` (6) | 6.7 KB / 6 files | **zero inbound references anywhere** (`git grep "\.agents"` → empty). A third generated copy of the same agent checks | **ARCHIVE** — deadest thing in the repo |
| `discussion_context.md` | 27.5 KB / 1 | **zero inbound references.** The raw 2026-08-19 product conversation ("Then give the implementation plan to OpenCode") | **ARCHIVE** — genuine provenance, wrong place |
| `docs/RUN_PROMPT.md` | 8.6 KB / 1 | 6 refs / 3 files. Body: *"read `_fleet/local/handoffs/01-game-architect-to-game-engineer.md`"* — a **gitignored** path, and the M0-M5 Bevy brief | **ARCHIVE.** Its one durable paragraph (the "overseer orchestrates only" standing rule) is already in `orchestration-playbook` |
| `AGENTS.md` | 1 KB / 1 | agent-tooling convention | **RESTRUCTURE** — rewrite (drops opencode + goose, keeps Claude Code) |
| `_fleet/shared/CHANGELOG.md` | 540 B / 1 | `CLAUDE.md` + `AGENTS.md` point changes here; has exactly **one row** (2026-08-19 "Initial fleet build") | **RESTRUCTURE** — see D-3 |
| `_fleet/local/**` | **0 tracked** (gitignored, 508 KB on disk: 40 handoffs + `LEDGER.md` + 2 scripts) | `.claude/settings.json`'s hook + all 8 `.claude/agents/*` | **KEEP on disk, fix the tracked references** — §2.4 |
| `docs/{implementation-plan,milestone-log,pr-log}.md` | 110 KB / 3 files | 26/16, 20/11, 24/10 refs — but almost all from each other and from `ORCHESTRATION-LOG` | **KEEP.** They are the v0.1 Bevy-era *history*, and history is cheap. Add a status banner (§7) so nobody mistakes `implementation-plan.md` for the current plan |

**Total tracked to archive:** 128 KB / 23 files.
**Risk of being wrong:** near zero for `.agents/` and `.opencode/` (dead models —
they cannot run). The real risk is **losing provenance**, which archiving to a
branch fully mitigates.

### 2.4 The live-but-stale harness — RESTRUCTURE (highest-value, lowest-byte)

These files are **tracked, loaded, and wrong**. They cost 0 bytes to fix and
they are what actively misleads a future session.

| Path | The problem | Verdict |
| --- | --- | --- |
| `.claude/settings.json` | The `SubagentStop` hook runs `sh "$CLAUDE_PROJECT_DIR/_fleet/local/scripts/validate-handoff.sh"`. `git check-ignore -v` → `.gitignore:22:_fleet/local/`. **The tracked hook points at a gitignored file.** On a fresh clone the gate silently does nothing (and `CLAUDE.md` already admits the workspace-trust variant of this). Also allows `Bash(sh _fleet/local/scripts/log-event.sh:*)` | **RESTRUCTURE** — either commit the 2 scripts to `_fleet/shared/scripts/` and repoint, or drop the hook. **Owner decision D-2** |
| `.claude/agents/*.md` (8) | **LIVE** (Claude Code lists them as agent types) but every one describes a *"Rust + Bevy developer companion desktop game"* fleet, tells the agent to `cargo test --workspace`, and coordinates through gitignored `_fleet/local/handoffs/`. Pointed at the track ADR 0011 froze | **RESTRUCTURE** — retarget at the Go/TS product, or archive with the fleet |
| `CLAUDE.md` | `**Goal:** Rust + Bevy developer companion desktop game` — contradicted by ADR 0011. Routes all implementation to `run-dev-companion`, a Bevy fleet skill. Says it *"is regenerated on every build"* by a fleet build that no longer runs | **RESTRUCTURE — SENSITIVE.** Exact proposed text in §6 |
| `.claude/skills/run-dev-companion` | 9.9 KB, 4 cargo/Bevy mentions, 0 Go/app mentions. Orchestrates the dead fleet | **RESTRUCTURE or ARCHIVE** |
| `.claude/skills/milestone-driven-rust-implementation` | 10 cargo/Bevy lines, 0 Go lines. Its whole subject (`docs/implementation-plan.md` milestones M0-M5) is finished history | **ARCHIVE** |
| `.claude/skills/rust-bevy-game-architecture` | 10 cargo/Bevy lines, 0 Go lines | **ARCHIVE** |
| `.claude/skills/visual-verification` | Instructs `cargo run -p shotcap` — the crate this plan archives. Superseded by `feature-build-and-verify`'s real gate (build Go binary → run with fake provider → `npx playwright screenshot` → judge) | **RESTRUCTURE** — keep `scripts/visual-check.py`, drop the shotcap path |
| `.claude/skills/{pr-review-lens,pr-merge-decision}` | Fleet-era PR ritual, ~1 cargo mention. Harmless but describes a workflow nobody runs | **KEEP** (decide later; zero cost) |
| `scripts/test-race.sh` | **Written to fix SF-3 and never wired up.** `git grep -l "scripts/test-race.sh"` → only itself. `build.yml`'s go job still runs bare `go test -race ./...` behind a `command -v cc` guard — exactly the bug the script exists to fix | **KEEP + WIRE UP.** Verified working: picks `CC=gcc`, full suite green |
| `scripts/build.sh` | Builds the legacy `companion` binary into `./build/`. Inbound refs: itself and `.gitignore` | **ARCHIVE** with the legacy track |

### 2.5 Outright deletable

| Path | Tracked | Evidence | Verdict |
| --- | --- | --- | --- |
| `docs/screenshot.png` | 23.7 KB | `git grep "screenshot.png"` → **zero hits.** Superseded by `docs/images/{hero,store,history}.png` (README) | **DELETE** |
| `tools/__pycache__/` | 0 (ignored) | 152 KB residue | **DELETE from disk** |
| `.opencode/node_modules/` | 0 (ignored) | 63 MB for a dead runtime | **DELETE from disk** |
| Local orphan commits `c6c311bf`, `7413a5e` | 0 | Both **local-only** (`git branch -a --contains` → empty). `c6c311bf` carries 5.4 GB of `target-m3-review/debug/`. Their only useful content (the Qwen model-tier change to `fleet.yaml`/`.opencode/`) **is already on `main`** — verified by reading the current `.opencode/agents/game-architect.md` frontmatter | **EXPIRE** (§6 Phase 0) |
| Stale remote branches: `feat/v0.2-art-and-global-input`, `fix/hud-not-rendering`, `fix/v1-review-findings`, `milestone/m5-polish` | — | None is an ancestor of `HEAD` (squash-merged). Tips dated 2026-08-20/21 | **KEEP for now** — they *are* the pre-squash Bevy history, i.e. a free archive. Revisit after D-1 |

### 2.6 What is NOT clutter (checked and cleared)

- **No stray files.** `git ls-files` finds no `.bak`/`.orig`/`.tmp`/`~`/`.gitkeep`/`.DS_Store`/`__pycache__`/`.pdf`/`.zip`. The one grep hit, `dev_docs/rust-parallel/P0a-cross-compile-probe.md`, is a real design doc (matched on "probe").
- **No probe-workflow leftovers.** `.github/workflows/` = exactly 3 files; the probe workflows were removed in `7d3415a`/`7a78704`.
- **`.gitignore` has no gaps.** `git status --porcelain -uall` lists **zero** untracked-not-ignored files. `app-rs/target/`, `desktop/src-tauri/target/`, `tools/shotcap/target/`, `target-*/` are all covered — verified individually with `git check-ignore -v`.
- **All three `cargo metadata` calls resolve** — the three-workspace split (root / `desktop/src-tauri` / `app-rs`) is deliberate and documented in `app-rs/Cargo.toml`'s header.
- **The committed bundle is honest.** A fresh `npm run build` reproduces `app/public/js/dexel.js` and its `.map` **byte-for-byte**.

---

## 3. Size analysis — the honest version

### 3.1 Tracked bytes at `HEAD`

```
ALL TRACKED:  5,609,692 bytes (5.35 MiB) in 395 files
  app/            2,155,326   (38.4%)   <- the product
  dev_docs/       1,162,687   (20.7%)   <- 888 KB of that is rust-parallel goldens
  docs/             985,167   (17.6%)
  desktop/          255,330    (4.6%)
  companion/        244,978    (4.4%)   <- ARCHIVE
  tools/            178,348    (3.2%)
  Cargo.lock        153,027    (2.7%)   <- ARCHIVE
  .claude/          105,797    (1.9%)
  activity/         104,665    (1.9%)   <- ARCHIVE
  .opencode/         46,930    (0.8%)   <- ARCHIVE
  .github/           41,351    (0.7%)
  scripts/           39,061    (0.7%)
  fleet.yaml         37,949    (0.7%)   <- ARCHIVE
  discussion_ctx     28,200    (0.5%)   <- ARCHIVE
  app-rs/            23,726    (0.4%)
  (root docs/licence/config)  ~46,000
```

Removed from `main` by this plan: **661 KB / 12%** (legacy Rust 495 KB + fleet
harness 128 KB + orphan screenshot 24 KB + rounding). Result: a **4.7 MiB, 372-file**
working tree.

**Largest single tracked blob:** `dev_docs/rust-parallel/goldens/raw_capture_full.jsonl`,
773,653 bytes — a real 87-frame WS capture with its provenance SHA recorded in
`CONTRACT.md`. It has zero inbound references but it is the *evidence* for the
experimental Rust track the owner declared keep-for-later. **KEEP.**

### 3.2 History — and why an archive branch saves nothing clone-wise

```
git clone --no-local <repo> /tmp/clonetest
du -sh clonetest/.git       ->  2.8M
git count-objects -vH       ->  in-pack: 2233, size-pack: 2.59 MiB
```

**The entire reachable history of this repository is 2.6 MiB.** Every historical
version of every file — 8 versions of `dexel.js.map`, 7 of `Cargo.lock`, 3 of
`companion/src/lib.rs` — compresses to that. Largest blob in reachable history:
773 KB.

Therefore, stated plainly so nobody re-argues it:

- Deleting files from `main` **does not shrink history**. The blobs stay reachable via older commits.
- Moving them to an archive branch makes them **more** reachable, not less.
- The only way to shrink history is a rewrite (`filter-repo`), which we are **NOT** doing — it would invalidate every existing clone and every recorded SHA in `docs/pr-log.md` and `docs/milestone-log.md` for a 2.6 MiB → maybe 2.2 MiB gain. **Not worth it. Do not propose it again.**
- `git clone --depth 1` is already ~2.6 MiB. There is no clone problem to solve.

### 3.3 Local disk — where the real 75 GiB is

```
     55G   target/                        (root Bevy workspace: 53G debug, 1.9G release, 828M doc)
    8.8G   target-m3-review/              abandoned reviewer CARGO_TARGET_DIR
    6.9G   tools/shotcap/target/          stray — shotcap is a workspace member; something ran with an override
    6.7G   desktop/src-tauri/target/      LIVE cache (keep)
    1.9G   build/                         scripts/build.sh output (legacy)
    1.2G   app-rs/target/                 experimental track cache
     63M   .opencode/node_modules/        dead runtime
     33M   app/frontend/node_modules/     LIVE cache (keep)
     13M   desktop/src-tauri/binaries/    sidecars (regenerable)
    152K   tools/__pycache__/
    508K   _fleet/local/                  KEEP — the live hook + handoff archive
  -------
   80.1 GiB  total ignored residue
   73.4 GiB  safely deletable (excludes desktop + frontend live caches)
```

Plus **`.git`: 1.7 GiB, of which ~1.697 GiB is garbage** — see §0/#1. The
breakdown:

```
.git/objects/pack/pack-09e98720...pack   699,456,597 bytes
  largest blobs inside it:
    1,566,777,080  bb788134  -> target-m3-review/debug/companion        (in c6c311bf)
    1,280,510,656  413f64ab  -> .../deps/companion-338795404111612b
    1,279,395,640  881caaa4  -> .../deps/m2_smoke-79ea6b00f014a4d1
  next largest: 236,453 (dexel.js.map)     <- a 5,400x cliff
loose objects: 5,521 objects / 1.04 GiB (never packed)
```

All three giants are ELF executables (`7f 45 4c 46`), **not** in the index, **not**
reachable from any ref, reachable **only** through the reflog entry for local
commit `c6c311bf`. This is exactly the accident `.gitignore:5-9` documents:
*"one `git add -A` tried to commit ~9GB of artifacts."* It was reset, but the
objects were never expired.

**Grand total reclaimable local disk: ~75.1 GiB.**

---

## 4. `app/`'s Go structure — RESTRUCTURE, but not the way it looks

**Current shape:** `package main` at the module root (`app/`), 15 non-test files
(~3.2 kloc), 8 test files (~2.6 kloc) also in `package main`, plus 8 `internal/`
packages (~22 kloc). So ~87% of the Go code already lives in `internal/`.

### 4.1 `app/cmd/dexel/` is not merely churn — it is *impossible* without moving the assets

`app/embed.go` declares:

```go
//go:embed all:public/index.html
//go:embed all:public/css
//go:embed all:public/fonts
//go:embed all:public/js/dexel.js
//go:embed all:assets
```

`go:embed` patterns **cannot contain `..`**. Verified empirically rather than
from memory — a throwaway module with `//go:embed all:../../public` in
`cmd/x/main.go`:

```
cmd/x/main.go:5:12: pattern all:../../public: invalid pattern syntax
```

So moving `package main` to `app/cmd/dexel/` forces `app/public/` and
`app/assets/` to move with it. That cascade would break, at minimum:

- `tools/gen_assets.py` (`ASSETS = REPO/"app"/"assets"`, plus ~10 internal path uses)
- `app/internal/assets`' upward directory search and `internal/game`'s catalog tests
- CI's bundle drift check (`git diff -- app/public/js/dexel.js`)
- `app/frontend/build.mjs`' output path and `app/frontend/README.md`
- `scripts/build-release.sh`, `scripts/build-sidecar.sh`
- the README's `app/` tree diagram, `docs/ui-spec.md` (144 inbound refs), and every doc naming `app/public` or `app/assets`
- `app/embed.go`'s own 30-line rationale comment, and `internal/assets`' explicit *"Historical note, so the layout is not 'fixed' back: assets/ used to live at the REPOSITORY ROOT … Both are gone"*

That last note is the tell: **this exact layout question was already decided, in
the opposite direction, for a concrete reason (EMBED-1, single-binary product).**

**Verdict: DO NOT move to `app/cmd/dexel/`.** Go's own layout guidance calls
`cmd/` a convention for *multi-binary* modules. `app/` ships one binary, has
its `internal/` split already, and `package main` at the module root is
idiomatic and correct at this size. "Industry standard" ≠ maximal nesting.

### 4.2 What *is* worth doing: split `main.go` in place

`app/main.go` is 1,551 lines and contains one 531-line function:

```
153  func runServe(mode serveMode, args []string)   <- to line 684
725  func applyAction(...)
867  type sessionAppendQueue struct
899  func popEndedSession(...)
1013 func wsOriginPatterns / handshakeLine / originList / wsExtraOriginPatterns
1095 func selectProvider
1148 func resolvePublicSource / resolveAssetsSource
1240 type healthResponse / healthHandler / aggregateSource
1305 func buildVersion
1340 func loadOrImport / loadOrInitConfig
1492 func writeRuntimeFile / runtimeRecord / reachableHost
```

Proposed pure file-split — **same package, same directory, zero import changes,
zero behaviour change, zero doc-path churn**:

| New file | Moves | ~lines |
| --- | --- | --- |
| `main.go` | `main()`, `serveMode`, `serveFlags`, `newServeFlagSet`, `runServe` | ~600 |
| `actions.go` | `applyAction` + action constants | ~180 |
| `sessions.go` | `sessionAppendQueue`, `popEndedSession`, `sessionCompleteFlashText`, `formatSessionDuration` | ~120 |
| `origins.go` | `wsOriginPatterns`, `handshakeLine`, `originList`, `wsExtraOriginPatterns` | ~90 |
| `sources.go` | `resolvePublicSource`, `resolveAssetsSource`, `fsFileExists`, `diskIndexExists`, `selectProvider` | ~150 |
| `health.go` | `healthResponse`, `healthHandler`, `aggregateSource`, `buildVersion` | ~110 |
| `bootstrap.go` | `loadOrImport`, `loadOrInitConfig`, `writeRuntimeFile`, `runtimeRecord`, `reachableHost` | ~230 |

**Cost, measured:** docs contain **48** `file.go:NNN` references total, **10** into
`main.go`. All 10 sit in `docs/plan/REVIEW-2026-08-22.md` and
`dev_docs/production-runtime/*` — point-in-time records whose line numbers are
already stale by design. So the churn is genuinely small.

**Risk if wrong:** a mechanical cut/paste can drop a declaration or duplicate an
import. Fully caught by the gate (`go vet` + `go build` + `go test -race`) — a
file split that compiles and passes 8/8 packages under `-race` *is* correct.

**Priority: LOW.** Do it only when someone is already working in `main.go`.
Splitting `runServe` itself (531 lines) is a genuine refactor, not a move, and is
**out of scope** for a structure cleanup.

### 4.3 `internal/` — leave alone

`store` (6.3 kloc / 15 files) and `game` (7.1 kloc / 20 files) are large but
cohesive and each already file-split by concern. No sub-package split proposed;
splitting them would mean exporting identifiers that are currently package-private,
which is a real API-surface change, not a cleanup.

---

## 5. `docs/` — the argument for leaving it alone

The tempting move is `docs/{adr,design,ops,history}`. **Recommendation: DON'T** —
and here is the number that decides it.

Inbound reference counts (`git grep -c -F <path>` over all tracked files):

```
144 refs in 56 files   docs/ui-spec.md
 81 refs in 33 files   docs/plan/P2-design.md
 74 refs in 21 files   docs/upgrade-design.md
 58 refs in 26 files   docs/art-direction.md
 48 refs in 37 files   docs/plan/ROADMAP.md
 34 refs in 21 files   dev_docs/production-runtime/MIGRATION_PLAN.md
 ... 39 paths with >=1 inbound ref
```

**~700 inbound path references across ~90 files**, and they are not just in
markdown — `docs/ui-spec.md` is cited from Go source comments, TypeScript
comments, ADRs, and CI YAML. A `docs/{adr,design,ops,history}` reshuffle means a
~700-site rewrite whose only benefit is that the directory listing reads more
tidily. Every one of those 700 sites is a chance to introduce a broken link, and
there is **no link checker in CI** to catch it (there is no working CI at all).

Against that: the current layout is *already* a documented four-layer taxonomy.
`docs/game/README.md` spells it out in a table — `docs/adr/` = why,
`docs/plan/` = what next, `docs/ui-spec.md` = how the frontend must be built,
`docs/game/` = how it works today. That is a real information architecture, and
it is better than the one a mechanical `{adr,design,ops,history}` split would
produce.

**Verdict: KEEP the layout. Fix the two real defects instead** (§7):

1. **`docs/game/` is orphaned.** The newest, most accurate layer has **zero**
   inbound links — not from `README.md`, not from `docs/plan/ROADMAP.md`. Fix
   with **3 links**, not 700.
2. **No docs index.** There is no `docs/README.md`. One page describing the four
   layers (lift the table from `docs/game/README.md`) delivers the entire benefit
   of a reorg for ~40 lines and zero broken links.

**`dev_docs/` vs `docs/` — justified, keep the split.** `docs/` is
product/design/decision material a contributor reads. `dev_docs/` holds two
in-flight *engineering investigations* — `production-runtime/` (the
release/platform/migration engineering) and `rust-parallel/` + `rust-port-evaluation.md`
(the experimental Rust port and its 888 KB of goldens). `docs/plan/ROADMAP.md:260,264`
deliberately routes the Rust track's artifacts there: *"Rust reports/artifacts
live in `dev_docs/rust-parallel/` for a later decision."* Merging them would
bury 888 KB of raw WS captures next to `art-direction.md`. **Two named
audiences, two directories.** The only fix needed: `dev_docs/` is unmentioned by
`README.md` — the new `docs/README.md` should name it.

**`docs/plan/ORCHESTRATION-LOG.md`** (47 KB, 55 dated rows: 1×08-19, 22×08-21,
30×08-22, 2×08-24) is append-only session history. It is *supposed* to be long.
Leave it; if it ever gets unwieldy, split by month **inside** `docs/plan/` — do
not move it out (`orchestration-playbook` mandates that the plan and its event
log live under `docs/plan/`).

---

## 6. The safe execution plan

Ground rules: **one phase per commit or small PR; each phase independently
shippable; the full gate runs after every phase; no two phases in flight at once.**

### The gate (run from the repo root, all of it, after every phase)

```bash
export PATH=/home/darkmirror/go-toolchain/go/bin:/home/darkmirror/.nvm/versions/node/v24.16.0/bin:$PATH

# G1  Go: vet, build, and the race suite (test-race.sh sets CC — see SF-3)
cd app && go vet ./... && go build -o /tmp/dexel-gate . && cd ..
bash scripts/test-race.sh

# G2  Frontend: strict typecheck, fresh build, and NO bundle drift
cd app/frontend && npm ci && npm run typecheck && npm run build && cd ../..
git diff --exit-code -- app/public/js/dexel.js app/public/js/dexel.js.map

# G3  Release pipeline actually runs (writes to ./dist, which is gitignored)
bash scripts/build-release.sh && ls -la dist/

# G4  Desktop shell still resolves (cargo check needs libdbus-1-dev + webkit
#     dev packages, absent on this box -> metadata is the portable gate)
cd desktop/src-tauri && cargo metadata --no-deps --format-version 1 >/dev/null && cd ../../..
#     On a box with the dev packages, upgrade this to:  cargo check

# G5  Docs links resolve — no NEW breakage vs the 15-item baseline in §7
git grep -ohE '\b(docs|dev_docs|app|scripts|tools|desktop|app-rs|_fleet|\.claude|\.github)/[A-Za-z0-9_./-]+\.(md|go|ts|py|sh|yml|json|png|html|css|toml|jsonl)\b' -- '*.md' \
  | sort -u | while read -r p; do [ -e "$p" ] || echo "MISSING: $p"; done | sort > /tmp/links-after.txt
diff /tmp/links-baseline.txt /tmp/links-after.txt   # must be empty, or only REMOVALS

# G6  Working tree is clean and .gitignore still covers everything
git status --porcelain -uall     # must be EMPTY (zero untracked-not-ignored)

# G7  The real product still runs (feature-build-and-verify's gate)
#     Only for phases that touch app/ — Phases 5+ do not.
```

All of G1, G2, G4(metadata), G5, G6 were **executed at `f920e4b` before this plan
was written** and are green (§1). G3 and G7 were not run by this audit — run them
before Phase 1.

Record the §7 baseline first: `... > /tmp/links-baseline.txt`.

---

### Phase 0 — Reclaim 75 GiB of local disk (no commit, no tracked change)

**Zero repo risk.** Nothing here is tracked, nothing here is pushed.

```bash
cd /home/darkmirror/repo/jawwadzafar/dev-companion

# 0a  Build residue (73.4 GiB). All gitignored; all regenerable.
rm -rf target/ target-m3-review/ build/ tools/shotcap/target/ app-rs/target/ \
       .opencode/node_modules/ tools/__pycache__/
#  KEEP: desktop/src-tauri/target/ (6.7 GiB, live), app/frontend/node_modules/ (33 MB, live)

# 0b  The 1.70 GiB of local-only git garbage.
#     Expires unreachable reflog entries, then repacks. Does NOT rewrite history,
#     does NOT touch origin, does NOT affect any other clone.
git reflog expire --expire-unreachable=now --all
git gc --prune=now
du -sh .git      # expect ~3-10 MiB (a fresh clone is 2.8M)
```

**Exact risk of 0b being wrong:** it permanently discards local commits
`c6c311bf` and `7413a5e`. Verified safe: neither is on any branch
(`git branch -a --contains` → empty), and their only useful content — the
DeepSeek→Qwen model-tier change to `fleet.yaml`/`.opencode/agents/*` — is already
on `main` (the current `.opencode/agents/game-architect.md` reads
`model: tokenfactory/Qwen/Qwen3.8-27B`). `docs/pr-log.md`'s PR #4 row records the
same thing: *"one harness-only commit `7413a5e` … rides along in the squash."*
The rest of `c6c311bf` is 5.4 GB of Bevy debug binaries.
**If you want belt-and-braces:** `git bundle create /tmp/orphans.bundle c6c311bf 7413a5e`
first — but note that bundle will be ~700 MB, which rather defeats the purpose.

Gate: G6 only (no tracked file changed).

---

### Phase 1 — Delete the one genuinely dead tracked file

```bash
git rm docs/screenshot.png
```

Evidence: `git grep "screenshot.png"` → zero hits, repo-wide.
Gate: G5, G6. **Risk: none.**

---

### Phase 2 — Wire up `scripts/test-race.sh` and fix the doc-link rot

No deletions. Pure correctness, and it makes the gate real.

1. `.github/workflows/build.yml` — replace the `go` job's `go test -race ./...`
   step (and its `command -v cc` guard) with `bash scripts/test-race.sh`.
   Do the same for `release.yml`'s race step. This is SF-3's fix, finally
   connected: the script probes `gcc, clang, cc` in that order **and compiles a
   trivial C program to prove the compiler works** before running the suite.
2. Fix the two genuinely stale references (leave the forward references alone —
   see §7): `dev_docs/production-runtime/ARCHITECTURE.md`'s
   `app/internal/store/legacy.go`, and `docs/game/{activity-signal,economy}.md`'s
   bare `activity/provider.go` → `app/internal/activity/provider.go`.

Gate: G1, G5, G6. **Risk: none** — the script is verified green (§1).

---

### Phase 3 — Make `docs/` navigable (the whole benefit of a reorg, none of the churn)

1. **New `docs/README.md`** — the four-layer table, lifted from
   `docs/game/README.md`, plus a pointer to `dev_docs/`.
2. **Link `docs/game/`** from `README.md` (its "Full specs live in `docs/`"
   paragraph, line ~259) and from `docs/plan/ROADMAP.md`. **Three links total.**
3. **Status banners** on the three v0.1 Bevy-era history docs
   (`docs/implementation-plan.md`, `docs/milestone-log.md`, `docs/pr-log.md`):
   `> **HISTORICAL (v0.1, Rust/Bevy era).** Superseded by ADR 0011. The current
   plan is docs/plan/ROADMAP.md.`

Gate: G5, G6. **Risk: none.** Zero renames, zero moves — additive only.

---

### Phase 4 — Archive the fleet harness *(owner decisions D-2, D-3 first)*

```bash
git switch -c attic/fleet-harness && git push -u origin attic/fleet-harness
git switch main
git rm -r .opencode .agents
git rm fleet.yaml opencode.json discussion_context.md docs/RUN_PROMPT.md
git rm -r .claude/skills/milestone-driven-rust-implementation \
          .claude/skills/rust-bevy-game-architecture
```

Then, in the same commit:

- **`AGENTS.md`** — rewrite: drop the opencode and goose invocation lines, keep
  Claude Code, repoint coordination at whatever D-2 decides.
- **`.claude/settings.json`** — resolve D-2 (commit the two hook scripts to
  `_fleet/shared/scripts/` and repoint the hook + the two `Bash(...)` allow
  entries, **or** remove the `SubagentStop` block entirely). A tracked hook
  pointing at a gitignored path must not survive this phase either way.
- **`.claude/agents/*.md`** (8 files) — retarget at the Go/TS product or archive
  with the fleet. They are *live* agent definitions currently describing a
  frozen Bevy stack.
- **`.claude/skills/visual-verification/SKILL.md`** — drop the
  `cargo run -p shotcap` path; keep `scripts/visual-check.py`; point at
  `feature-build-and-verify`'s Playwright gate.

Gate: full G1-G6. **Risk of being wrong:** the branch is the mitigation. The one
thing to double-check by hand is that no `.claude/skills/*` still references a
deleted path — G5 catches it in markdown, so make sure the deleted skills are
also removed from any skill that links them.

---

### Phase 5 — Archive the frozen Rust/Bevy track *(owner decision D-1 first)*

```bash
git switch -c legacy-rust && git push -u origin legacy-rust
git switch main
git rm -r companion activity tools/shotcap
git rm Cargo.toml Cargo.lock scripts/build.sh
```

Then:

- **`.github/workflows/build.yml`** — delete the `build` job (~75 lines) and
  rewrite the file header comment (it currently describes three jobs and
  explains the legacy one at length). Keep `go` and `frontend` untouched.
- **`.gitignore`** — the `/target/`, `**/target/`, `target-*/`, `/build/` rules
  stay (`desktop/src-tauri/target/` and `app-rs/target/` still need them). Update
  the `/build/` comment, which names the now-deleted `scripts/build.sh`.
- **`README.md`** — rewrite the "Legacy: the Rust/Bevy implementation" section
  (lines ~263-273) to say the crates now live on branch `legacy-rust`, and drop
  the "legacy Rust build" mention in the CI paragraph (~line 390).
- **New `docs/adr/0020-archive-the-frozen-rust-track.md`** — supersedes ADR 0011's
  "kept in-tree" clause. **Mandatory**: ADR 0011 says the legacy build is
  *"retained precisely for that exit"*, and an ADR is only overturned by another
  ADR. State the evidence (B-2 deleted the importer; `main.go:1419` is the
  tombstone; the exit route is `git worktree add ../legacy-rust legacy-rust`).
- **`CLAUDE.md`** — see D-4 below. Do it in this commit, since this is the commit
  that makes the current text false.

Gate: full G1-G6, **plus** `cargo metadata` must now fail at the root (no
workspace there any more) while `desktop/src-tauri` and `app-rs` still resolve.

**Exact risk of being wrong:** if the web stack later disappoints and the owner
wants the Bevy game back, this costs one `git worktree add` — but only if the
branch was actually pushed. **Verify `origin/legacy-rust` exists before the
`git rm`.** Recovery if forgotten: the four stale remote branches
(`feat/v0.2-art-and-global-input` et al.) still carry the pre-squash Bevy tree,
and the deleted files remain in `main`'s own history regardless.

---

### Phase 6 (optional, LOW priority) — split `app/main.go`

Only when someone is already editing `main.go`. §4.2 has the file map. Pure
cut/paste, same package, same directory. Gate G1 + G7 is sufficient: a split that
builds and passes `-race` on 8/8 packages is correct.

---

## 7. What explicitly does NOT change

Re-litigating any of these is a regression. Every one is load-bearing.

1. **`app/public/js/dexel.js` + `dexel.js.map` stay committed.** Deliberate — see
   `app/frontend/README.md` "Why the bundle is committed" and CI's drift check.
   The `.map` is committed **and** deliberately excluded from `go:embed` (N-9).
   Both facts are intentional.
2. **`app/assets/` — all 92 PNGs stay exactly where they are.** `go:embed` cannot
   reach outside its own package directory (proven in §4.1). Moving them breaks
   the single-binary product.
3. **`app/public/` stays at `app/public/`.** Same reason.
4. **`package main` stays at `app/`.** No `cmd/dexel/`. §4.1.
5. **`docs/` keeps its current paths.** No `{adr,design,ops,history}` reorg.
   ~700 inbound references, no link checker. §5.
6. **`dev_docs/` stays separate from `docs/`.** Two audiences. §5.
7. **`.claude/skills/`** — `feature-build-and-verify`, `add-a-menu-modal`,
   `orchestration-playbook`, `pixel-art-authoring` are the live working method.
   Never delete a skill to tidy up.
8. **No ADR under `docs/adr/` is ever deleted or edited.** Low inbound-link counts
   on individual ADRs are correct; `docs/adr/README.md` is the index.
9. **`app-rs/` and `dev_docs/rust-parallel/` stay**, including the 773 KB
   `raw_capture_full.jsonl`. Explicit owner decision, 2026-08-22.
10. **`desktop/`, `tools/gen_assets.py`, `tools/gen_icon.py`,
    `scripts/{build-release,build-sidecar,visual-check}` stay.**
11. **`docs/{implementation-plan,milestone-log,pr-log}.md` stay.** History is
    cheap; they get banners, not deletion.
12. **No history rewrite. Ever.** §3.2.
13. **The four stale remote branches stay** until after Phase 5 — they are a free
    pre-squash archive of the Bevy tree.
14. **`_fleet/local/` on disk stays** — it holds the live hook scripts and 40
    handoff records.

### Doc-link baseline — the 15 already-missing paths (pre-existing; NOT caused by this plan)

Record this before Phase 1 so G5 can diff against it. **Do not "fix" the forward
references** — they are files a plan says *will* exist:

*Forward references in plan docs (leave alone):* `app/cmd_update.go`,
`app/cmd_uninstall.go`, `scripts/install.sh`, `scripts/make-manifest.sh`,
`scripts/publish-r2.sh`, `scripts/parity-check.sh`, `dev_docs/parity/CONTRACT.md`,
`dev_docs/parity/TEST-PARITY.md` — from `dev_docs/production-runtime/{MIGRATION_PLAN,RELEASE_PIPELINE}.md`
and `dev_docs/rust-{port-evaluation,parallel/CONTRACT}.md`.

*Genuinely stale (fix in Phase 2):* `app/internal/store/legacy.go` (deleted by
B-2), `activity/provider.go`, `activity/sanitize.go`, `activity/app_identity.go`,
`activity/content_free_test.go`, `activity/provider_darwin.go` (all mean
`app/internal/activity/...`), `companion/save.json` (the legacy save path, in
historical docs — leave those, they are records).

---

## 8. KEEP-list rationale — so nobody re-opens these

| Thing | Why it stays | Where the reasoning already lives |
| --- | --- | --- |
| Committed frontend bundle | Reproducibility + `go:embed` needs a real file; CI proves no drift | `app/frontend/README.md`; `build.yml`'s `frontend` job |
| `dexel.js.map` committed but not embedded | CI diffs it; `-public app/public` serves it in dev; 230 KB of debug data must not ship | `app/embed.go`'s `publicEmbed` comment (N-9) |
| `app/assets/` under `app/` | `go:embed` cannot escape its module/package dir | `app/embed.go`; `app/internal/assets`' "Historical note, so the layout is not 'fixed' back" |
| Three separate cargo workspaces | Root (frozen), `desktop/src-tauri`, `app-rs` — deliberate isolation from a 566-crate / 70 GiB tree | `app-rs/Cargo.toml` header |
| `docs/game/` as its own layer | Derived from source; updated with behaviour changes; the other layers are older than the code | `docs/game/README.md`'s four-layer table |
| `docs/plan/ORCHESTRATION-LOG.md` length | Append-only session log; the playbook requires the log live in-repo | `.claude/skills/orchestration-playbook/SKILL.md` |
| `nes.min.css` at 288 KB | The product's entire visual language (ADR 0011) | `app/public/index.html:14` |
| `dev_docs/rust-parallel/goldens/*` at 888 KB | The evidence for the parity decision, captured from the real server with its provenance SHA | `dev_docs/rust-parallel/CONTRACT.md` |
| `Cargo.lock` committed (while the track lives) | Application, not library — locked tree is the convention | `.gitignore:12-14` |

---

## 9. Owner decisions required

Nothing in Phases 4-5 starts until these are answered.

| ID | Decision | Recommendation | Why it needs the owner |
| --- | --- | --- | --- |
| **D-1** | **Archive `companion/`+`activity/`+`Cargo.*`+`tools/shotcap/` to branch `legacy-rust` and remove from `main`?** | **Yes — archive.** The last consumer (the legacy-save importer) was deleted by B-2; `build.yml` builds it for a reason that no longer exists; two directories named `activity/` in one repo is an active hazard for every future session | **ADR 0011 explicitly says "Not deleted … retained precisely for that exit."** Overturning an accepted ADR is the owner's call, and it requires ADR 0020 |
| **D-2** | **The `SubagentStop` hook: commit the scripts, or drop the hook?** | **Commit them** to `_fleet/shared/scripts/` (they are 2 KB + 3 KB, already exist, and the gate is genuinely useful) and repoint `.claude/settings.json` + its two `Bash(...)` allow entries | `.claude/settings.json` is harness config. It currently ships a hook aimed at a gitignored file — broken on any fresh clone. Only the owner should change harness config |
| **D-3** | **Keep `_fleet/shared/CHANGELOG.md` as the harness changelog?** | **Keep it, but move it** to `docs/HARNESS-CHANGELOG.md` — the `_fleet/` name means nothing once the fleet is archived, and both `CLAUDE.md` and `AGENTS.md` route changes there | Depends on D-2's shape |
| **D-4** | **`CLAUDE.md` rewrite** — it currently says the goal is a *"Rust + Bevy developer companion desktop game"*, which ADR 0011 falsified, and routes all work to the Bevy fleet skill | Proposed text below | `CLAUDE.md` steers every future session. Sensitive by nature |
| **D-5** | **`docs/RUN_PROMPT.md` and `discussion_context.md` — archive or keep?** | **Archive both.** `RUN_PROMPT.md` briefs M0-M5 of a frozen stack and cites gitignored handoff paths; `discussion_context.md` has zero inbound references | They are the project's origin story. Deleting provenance deserves an explicit yes |
| **D-6** | **CI is dead — 100/100 `startup_failure` since 2026-08-21T17:42.** The runner `jwdlab-runner` is **online** with correct labels; `actions/permissions` is `enabled: true`; all three YAML files parse and their jobs enumerate cleanly. The API returns `"path": "BuildFailed"` with zero jobs — consistent with the account-level Actions block recorded in `ROADMAP.md` ("Actions is account-blocked — SF-2 — so the pipeline runs by hand until billing is fixed") | **Fix the billing/account block, or accept local gating.** Until then every phase above is gated by a human running §6's G1-G7 by hand | Only the owner can fix GitHub billing. Until then, **CI cannot protect this cleanup** — which is the single biggest execution risk in this plan |

### Proposed `CLAUDE.md` (D-4)

Replacing the current Rust/Bevy text. Assumes D-1 and D-2 are "yes":

```markdown
## Project: dexel

**Goal:** Dexel — a cozy pixel-art desktop companion whose workday runs on your
real typing. Go + HTML/CSS/TypeScript (ADR 0011). The thing you run is `app/`.

**Stack:** Go server + WebSocket + a committed TypeScript bundle, shipped as ONE
binary (`app/embed.go` compiles `app/public/` and `app/assets/` in). A Tauri v2
shell lives in `desktop/`.

**Where to start:** `docs/README.md` indexes the four documentation layers.
`docs/game/` is how the game works today; `docs/plan/ROADMAP.md` is what's next;
`docs/adr/` is why.

**The gate:** no visual/UX change is done until it is rendered in the REAL running
game — build the Go binary, run it with the fake provider, screenshot it, judge it
with your own eyes. Isolated mockups have lied twice. See the
`feature-build-and-verify` skill.

**Before shipping:** `cd app && go vet ./...`, `bash scripts/test-race.sh`, and
`cd app/frontend && npm run typecheck && npm run build` with no bundle drift.
GitHub Actions is currently blocked at the account level — run the gates locally.

**Legacy:** the Rust/Bevy implementation is archived on branch `legacy-rust`
(ADR 0011, ADR 0020). Two things named `activity` used to exist; now only
`app/internal/activity/` does.

**Changelog:** harness changes go in `docs/HARNESS-CHANGELOG.md`.
```

---

## 10. Summary of numbers

| Metric | Value |
| --- | --- |
| Tracked bytes at `HEAD` | 5,609,692 (5.35 MiB) / 395 files |
| **True history size (fresh clone `.git`)** | **2.8 MB / 2.59 MiB pack** |
| Local `.git` | 1.7 GiB → **~1.697 GiB is local-only garbage** |
| Ignored build residue on disk | 80.1 GiB total / **73.4 GiB safely deletable** |
| **Total reclaimable local disk** | **~75.1 GiB** |
| Tracked bytes removed from `main` | ~661 KB (**12%** of the tree) |
| Effect on clone size | **zero** (no history rewrite) |
| Tracked files removed | 23 (fleet) + 13 (legacy Rust) + 1 (screenshot) = **37** |
| Files whose *content* needs correcting | 8 (`CLAUDE.md`, `AGENTS.md`, `.claude/settings.json`, 8×`.claude/agents/*`, 1 skill, `README.md`, `build.yml`, `.gitignore`) |
| Pre-existing broken doc links | 15 (8 forward references — leave; 7 genuinely stale) |
| Gates verified green at `f920e4b` | `go vet`, `go test`, **`go test -race`**, `go build`, `tsc`, `esbuild` + **zero bundle drift**, 3× `cargo metadata` |
| Gates NOT verifiable here | `desktop cargo check` (missing `libdbus-1-dev`), all of CI (account-blocked) |

---

## 6b. Execution record (2026-08-24) — what actually happened

Phases 0-5 executed on `main`, one commit per phase, the full local gate
(§6 G1-G6 plus a YAML/`bash -n` check on every changed workflow run-block) green
after each. Phase 6 was explicitly out of scope for this pass.

**Owner decisions as resolved:** D-1 **yes, archive** (hence ADR 0020, which
amends ADR 0011's "Not deleted" clause). D-2 **commit the scripts** — they now
live at `_fleet/shared/scripts/`. D-3 **move the changelog** — now
`docs/HARNESS-CHANGELOG.md`. D-4 **apply the proposed text**, keeping the
standing "orchestrate only, all implementation through subagents" rule and a
pointer to `docs/plan/{ROADMAP,ORCHESTRATION-LOG}.md`. D-5 **archive both**.
D-6 **acknowledged** — CI is dead, every gate was run locally.

**Three places this plan was wrong or incomplete, with evidence:**

1. **One archive branch, not two.** §6 proposed `attic/fleet-harness` (Phase 4)
   and `legacy-rust` (Phase 5). Both archives went to a single branch,
   **`attic/legacy-rust-and-fleet`**, cut at `d005454` and pushed to `origin`
   *before* either deletion commit. One branch, one recovery command, and the
   branch predates both deletions — which is what the safety argument actually
   requires.
2. **Committing the hook script needed one more guard than D-2 anticipated.**
   D-2 called the gate "genuinely useful" and stopped there. But the reason it
   was harmless before is that it pointed at a missing file and therefore never
   ran; making it real also makes it run in a checkout where
   `_fleet/local/handoffs/` does not exist (it is gitignored), where every
   subagent would then be blocked forever on a handoff file it cannot have
   written yet. `validate-handoff.sh` now treats a **missing handoffs directory**
   as "no run in progress" (report, exit 0) and stays fully strict once the
   directory exists. Verified by hand: terminal agent → 0, real workspace → 0,
   simulated fresh clone → 0 with the notice, workspace-present-but-no-handoff
   → 2. Relatedly, `log-event.sh` resolved its output as `$(dirname $0)/../runs`,
   which after the move would have written untracked JSONL into the *tracked*
   `_fleet/shared/`; it now targets `_fleet/local/runs/` explicitly.
3. **§2.4 left the 8 `.claude/agents/*.md` as an either/or ("retarget … or
   archive") and wrote out no text for them.** They were retargeted at the Go/TS
   product, because they are live agent types and the standing rule is that all
   implementation goes through them: `cargo fmt/clippy/test` → `go vet` +
   `scripts/test-race.sh` + `npm run typecheck && npm run build` with no bundle
   drift, `ActivityProvider` trait → the `app/internal/activity` boundary,
   `assets/` → `app/assets/`, and `docs/{implementation-plan,milestone-log,pr-log}.md`
   → `docs/plan/{ROADMAP,ORCHESTRATION-LOG}.md`. The same pass retargeted
   `run-dev-companion` (which §2.4 also left as either/or) and
   `visual-verification`, and repointed `orchestration-playbook` and
   `pr-merge-decision` at the archived `RUN_PROMPT.md`/`fleet.yaml` *as archived*
   rather than as live paths.

**Two measurements this plan reported that came out differently:**

- The **doc-link baseline is 12 paths, not 15.** The §6 G5 regex only matches
  paths prefixed `docs|dev_docs|app|scripts|tools|desktop|app-rs|_fleet|.claude|.github`,
  so the bare `activity/*.go` references §7 counts are invisible to it; and
  committing this document added three of its own forward references
  (`docs/README.md`, `docs/HARNESS-CHANGELOG.md`,
  `docs/adr/0020-archive-the-frozen-rust-track.md`), two of which Phases 3-4 then
  created. After Phase 5 the sweep's only *new* entries are paths this very
  document names as deletion targets — verified one by one with
  `git grep -l -F <path> -- '*.md'`, every hit being this file (plus
  `docs/HARNESS-CHANGELOG.md` citing its own former location).
- **`.git` was 1.70 GiB and is now 3.1 MiB**, and the ignored residue came to
  **~75 GiB reclaimed** — both as predicted. `git fsck` is clean afterwards.
