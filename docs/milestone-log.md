# dev-companion — Milestone Log

One entry per milestone attempted, appended by game-engineer. Each entry:
files changed, exact commands run, their real output, remaining issues.
Never delete or rewrite a past entry — append corrections as new entries.

---

## M0 — Workspace scaffold

- **Date:** 2026-08-19
- **Resolved Bevy version:** `0.19.1` (resolved via `cargo add bevy -p companion`; 550 packages locked)
- **Branch:** `milestone/m0-workspace-scaffold`

### Files changed
- `Cargo.toml` (new) — root workspace manifest, members `["activity", "companion"]`, resolver 2.
- `activity/Cargo.toml` — lib crate, workspace-inherited metadata, **no Bevy dependency** (architecture boundary).
- `activity/src/lib.rs` — `ActivityEvent` enum, `ActivityProvider` trait, `MockProvider` (test-only) + 3 unit tests.
- `companion/Cargo.toml` — bin crate, deps `activity` (path) + `bevy 0.19.1`.
- `companion/src/main.rs` — minimal `App::new().add_plugins(DefaultPlugins).run()`.
- `Cargo.lock` — committed (deliberate, per repo `.gitignore` comment).

### Exact commands run and real output

1. `cargo fmt --all -- --check` → exit 0, no output (clean).
2. `cargo clippy --workspace --all-targets -- -D warnings` → `Finished dev profile ...` exit 0, **no warnings**.
3. `cargo test --workspace` → 3 tests pass (`mock_provider_replays_events_in_order`, `empty_mock_provider_yields_nothing`, `activity_events_are_content_free`); companion 0 tests; exit 0.
4. `cargo run -p companion` → window opened. Key log lines (real output):
   ```
   INFO bevy_render::renderer: AdapterInfo { name: "AMD Radeon 890M Graphics (RADV STRIX1)", ... backend: Vulkan ... }
   INFO bevy_winit::system: Creating new window companion (65v0)
   ```
   Process stayed alive until the 15s `timeout` (exit 124 = still running, window open).

### Toolchain note (environment-specific, applies to every milestone)
This machine has **no system C toolchain / pkg-config / libc-dev and no sudo**. A working toolchain was bootstrapped with **zig** as the C compiler+linker, a pkg-config shim, and user-local dev symlinks. Every shell that runs cargo must first run `source ~/.cargo/devcompanion-env.sh`. Do NOT install via apt/sudo and do NOT modify `~/.cargo/config.toml` or `~/.local/bin` shims.

### Remaining issues
- None for M0 scope. The M2 debug counter, anti-mashing clamp, and all later-milestone work are explicitly not part of M0 (see plan §4).

---

## M1 — Static scene + HUD skeleton

- **Date:** 2026-08-19
- **Branch:** `milestone/m1-hud-skeleton`

Note on provenance: the opencode session implementing this milestone was
interrupted mid-work (process closed) after writing the scene/HUD code but
before running the verification sequence or committing. The code was found
uncommitted on `main`, reviewed, fixed, verified, and committed directly in
this session rather than left for a fresh session to rediscover cold.

### Files changed
- `companion/src/main.rs` — `Developer` component (`mood: Mood`), `Mood` enum
  (`Idle`/`Coding`/`OnBreak`, only `Idle` constructed this milestone),
  `MoodLabel`/`ProgressBarFill` marker components, `setup_scene` Startup
  system spawning: a `Camera2d`, a placeholder desk (colored `Node` rects —
  a desk slab + a "computer" prop), and a HUD bar (progress-bar fill node at
  a hardcoded 0%, "Coins: 0" text, "Lv 1 · 0 XP" text, "Mood: Idle" text
  marked `MoodLabel`). All values hardcoded per M1 scope — no system updates
  anything yet.

### Two bugs found and fixed during verification (not present in the final commit)
1. Four `.register_type::<T>()` calls on types with no `Reflect` derive —
   compile error (`E0277`, `T: GetTypeRegistration` unsatisfied). Fix:
   removed the calls rather than adding derives — nothing in the M1 plan
   scope needs Bevy reflection, and the register_type calls were speculative,
   not required.
2. `Mood::Coding`/`Mood::OnBreak` and `Developer.mood` — `dead_code` clippy
   errors (`-D warnings`), since M1 spawns `Idle` only and no system reads
   the field yet. Fix: inline `#[allow(dead_code)]` on each, with a one-line
   comment stating M3 (`mood_render_system`/`idle_detection_system`, plan
   §3.2/§4-M3) is what will read/construct them — not a blanket allow, and
   not deleting the variants/field the plan's ECS design (§3.2) requires.

### Exact commands run and real output

1. `cargo fmt --all -- --check` → exit 0, no output (clean).
2. `cargo clippy --workspace --all-targets -- -D warnings` → after the two
   fixes above, `Finished dev profile ...` exit 0, **no warnings**.
3. `cargo test --workspace` → same 3 `activity` tests pass; `companion` 0
   tests (none added this milestone — nothing pure-function-testable yet);
   exit 0.
4. `cargo run -p companion` (15s/25s `timeout`) → window opened, no panics,
   no UI errors. Real output:
   ```
   INFO bevy_render::renderer: AdapterInfo { name: "AMD Radeon 890M Graphics (RADV STRIX1)", ... backend: Vulkan ... }
   INFO bevy_winit::system: Creating new window companion (65v0)
   WARN sctk_adwaita::buttons: Ignoring unknown button type: icon
   WARN sctk_adwaita::buttons: Ignoring unknown button type: menu
   ```
   Process stayed alive until timeout (exit 124 = still running, window
   open) — same healthy pattern as M0. The two `sctk_adwaita` warnings are
   this Linux desktop's window-decoration theme (Wayland/adwaita client-side
   decorations), unrelated to companion code; not treated as a defect.

### Remaining issues
- Visual confirmation of the HUD layout (bar/text positions) was via a
  headless-safe smoke run (window created, no render errors), not a human
  screenshot — worth a human glance when convenient, but not blocking per
  the plan's command-verifiable exit criterion for M1.

