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

