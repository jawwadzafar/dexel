# Mac agent brief — frameless shell + one-command install on macOS

**You are an agent running on the owner's Mac.** This box (where the brief was
written) is Linux and **cannot see a macOS window** — every macOS behaviour
below is unverified until *you* verify it. Your job: investigate, fix, verify
with your own eyes (screenshots), and write a verdict back (last section).

Read the repo's `CLAUDE.md` first — it is binding. The rules that bite here:
- **No `Co-Authored-By: Claude` trailer** in any commit, ever. A
  `Claude-Session:` trailer is fine.
- **Version is frozen at v0.1.0** — never bump or tag. Refresh assets in place.
- The **gate is the real running app**: build it, run it, screenshot it, judge
  with your own eyes. Isolated reasoning about macOS has already been wrong.
- If you spawn sub-agents, give each **exclusive file ownership**; re-verify
  every "done" yourself from a clean build.

---

## Background (what's true today, verified on Linux)

dexel ships as a Go single binary (the runtime + CLI) plus an **optional** Tauri
v2 desktop shell in `desktop/`. The shell is frameless by decision:
`desktop/src-tauri/src/lib.rs` calls `.decorations(false)` unconditionally (no
per-OS branch), and the web page draws its own 24px pixel titlebar with in-page
`−`/`✕` buttons and `#titlebar` as the drag region. The owner has chosen the
**"bare, like Linux"** macOS style: no native traffic lights, square corners,
frameless — identical to Linux. **Do not switch to a transparent/overlay
titlebar** unless the owner says so in a later message.

There is a documented four-file frameless contract in `lib.rs` (search
`WINDOW-POLISH — the frameless shell's four-file contract`) plus window
snap-sizing (min 640×400, max 1920×1200, snaps to crisp multiples). Those are
tested but were **never seen on a screen** — this is their first real display.

### The macOS gap you are here to close

1. `dexel open` on macOS looks for **`/Applications/Dexel.app`**
   (`app/cmd_lifecycle.go`, `desktopAppCandidates` → darwin → `/Applications/
   Dexel.app`; launched via `open -a`). If it is absent, `open` **falls back to
   the browser** — which is what the owner saw ("not frameless like Linux",
   but sound works, because the browser runs the same web game).
2. `install.sh` installs the desktop shell **only on Linux** (it downloads the
   Linux-only AppImage). On macOS it installs the CLI binary but **never builds
   or installs `Dexel.app`**. So the frameless window never appears from an
   install alone.

The owner wants the one-command install to be truly state-of-the-art:
`install.sh` on a Mac clone should **also build `Dexel.app` and place it in
`/Applications`** (best-effort — only when the Rust/Tauri toolchain is present),
so `dexel open` then finds it and the frameless window "just works."

---

## Phase 0 — Confirm the diagnosis (before changing anything)

```bash
cd <this repo on the Mac>
git pull
git rev-parse --short HEAD          # record it in the verdict
dexel status --json | grep -i url   # is a runtime up? note the url
dexel open                          # EXPECT: opens a BROWSER (the gap), not a window
```

Confirm: with no `/Applications/Dexel.app`, `dexel open` opens the browser.
Record exactly what happened. If a `Dexel.app` already exists in `/Applications`
(a stale one), note its build date — a stale app is its own explanation.

## Phase 1 — Build the shell and see it frameless (manual, your eyes)

```bash
# toolchain (skip if present): rustup, then the Tauri CLI
cargo install tauri-cli --version '^2'   # if `cargo tauri` is missing
cd desktop/src-tauri
cargo tauri build                        # produces target/release/bundle/macos/Dexel.app
cp -R target/release/bundle/macos/Dexel.app /Applications/
dexel open                               # EXPECT: frameless Dexel.app, NOT the browser
```

**Screenshot the running window** (⌘⇧4, or `screencapture -x file.png`) and
LOOK at it. It is correct only if ALL of these are true — record each:
- [ ] No native macOS title bar and **no red/yellow/green traffic lights**.
- [ ] dexel's own pixel titlebar shows at top with `☰` (left) and `−` `✕`
      (right); the `−`/`✕` actually minimize/close the window.
- [ ] Dragging the pixel titlebar moves the window; dragging a button does not.
- [ ] The game fills the window edge-to-edge (no letterbox bands); resizing
      snaps to crisp sizes and stays edge-to-edge; it won't shrink below
      640×400 or grow past 1920×1200.
- [ ] Clicking the dexel/monitor/mug/buddy reacts (and plays a sound);
      finishing a sprint plays the jingle.

If any check fails, that is a **real macOS bug the Linux box could not catch** —
capture the screenshot, the console (the shell logs to the runtime log; also try
launching the app from a terminal to see stderr), and diagnose. Likely suspects
if it is NOT frameless even from a fresh `Dexel.app`: a macOS-specific quirk of
`decorations(false)` at creation, or the window being created from
`tauri.conf.json` instead of `build_window` (it should be the latter — the conf
has an empty windows array). Fix in `desktop/src-tauri/src/**` (you own it),
keep `cargo fmt --check` + `cargo clippy --all-targets -- -D warnings` +
`cargo test` green.

## Phase 2 — Fold the Mac `.app` into `install.sh` (the real deliverable)

Extend `install.sh`'s macOS path so a source install also builds and installs
`Dexel.app`. Design (match the existing style — read the file first, especially
the source-selection ladder and `install_desktop_entry`):
- Only on `OS=darwin`, only on the **from-source** path (you are in the repo),
  and only when the shell toolchain is present (`cargo` on PATH and
  `cargo tauri` resolvable, or `rustup`). If absent: **skip with a clear
  notice** ("skipping the desktop window: install rustup + `cargo install
  tauri-cli` to get it") — never fail the whole install for it. The CLI binary
  must still install and start, exactly as now.
- Build `Dexel.app` (`cargo tauri build`, or reuse `scripts/mac-release.sh` if
  that is the cleaner path — read it; it also signs, which may prompt — prefer
  plain `cargo tauri build` for the unsigned local case and note signing as a
  separate owner step).
- Install to `/Applications/Dexel.app` when writable without sudo; if
  `/Applications` needs admin, fall back to `~/Applications/Dexel.app` (add that
  as a darwin candidate in `app/cmd_lifecycle.go`'s `desktopAppCandidates` if it
  is not already there — you may edit that one function; keep its tests green)
  and say which was used. Respect `--no-app` / `DEXEL_NO_APP`.
- Keep every existing property: no unconditional sudo, idempotency, checksum
  behaviour on the release path, `shellcheck -s sh` clean, `dash -n`/`bash -n`.

Then prove it: from a fresh temp `DEXEL_INSTALL_DIR`/`HOME`, run the installer
from the clone on the Mac and confirm it built `Dexel.app`, placed it, started
the runtime, and that `dexel open` launches the **frameless window** (not the
browser). Screenshot it. Then uninstall (`dexel uninstall`) and confirm the app
bundle is removed too (extend the darwin reversal in `app/cmd_uninstall.go` if
`/Applications/Dexel.app` / `~/Applications/Dexel.app` are not already covered —
you own that file for this; keep its tests green).

## Gates before you call anything done

- `cd app && go build ./... && go vet ./... && bash ../scripts/test-race.sh`
  (Darwin needs cgo+clang — that is normal on a Mac; it should build natively).
- `cd app/frontend && npx tsc --noEmit && npm run build` — no bundle drift
  (`git status` clean on `app/public/js/dexel.js`) unless you intended a change.
- `cd desktop/src-tauri && cargo fmt --check && cargo clippy --all-targets --
  -D warnings && cargo test`.
- `shellcheck -s sh install.sh`.
- The **real running frameless window**, screenshotted and judged by your eyes.

## Committing (only if the owner-facing behaviour is verified working)

Small, scoped commits. No `Co-Authored-By: Claude`. End messages with:
`Claude-Session: <the session URL, or omit if you don't have one>`.
Do **not** bump the version. If you refresh release assets, do it in place at
v0.1.0. Push to `main` (or a branch if the owner prefers to review — ask if
unsure).

---

## VERDICT — fill this in and report back

Write your findings here (and/or a sibling `VERDICT-<date>.md`), then tell the
owner. Be specific and honest — "it built" is not a verdict; "the window came up
frameless, screenshot attached, and install.sh now installs it" is.

- HEAD commit investigated:
- Phase 0 — did `dexel open` open the browser (confirming the gap)? 
- Phase 1 — did a built `/Applications/Dexel.app` come up **frameless**? Which
  of the checklist items passed/failed? (attach/point to screenshots)
- Any real macOS bug found + how you fixed it:
- Phase 2 — does `install.sh` now build + install `Dexel.app` on a Mac clone?
  Proven how? Does `dexel uninstall` remove it?
- Gates: which passed, which you couldn't run and why:
- Commits pushed (SHAs) or left uncommitted (why):
- Anything still needing the owner's decision:
