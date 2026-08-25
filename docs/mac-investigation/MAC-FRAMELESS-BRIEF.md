# Mac agent brief — frameless shell + one-command install on macOS

**You are an agent running on the owner's Mac.** This box (where the brief was
written) is Linux and **cannot see a macOS window** — every macOS behaviour
below is unverified until *you* verify it. Your job: investigate, fix, verify
with your own eyes (screenshots), and write a verdict back (last section).

**OWNERSHIP RULE (the owner was explicit): YOU do the whole job — build, fix,
verify, commit, push. Leave NOTHING as a manual step for the owner.** "The
owner should run X" is not an acceptable outcome; you are on the Mac, so you
run X. The only things you hand back are (a) the verdict and (b) any decision
that is genuinely the owner's taste (e.g. a visual style choice) — never a
build or a fix you could have done yourself.

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

## Phase 3 — The always-on-top toggle (owner: "it's reversed on Linux")

The owner reports on Linux: **toggle OFF → window stayed on top; toggle ON →
window went to the bottom** — fully reversed. On the Mac they saw the toggle do
**nothing** (a stale/old shell — expect Phase 1's fresh build to change this).

**A full code trace was done on the Linux box and every layer is
non-inverted** — do not re-derive this, verify it:
- `app/frontend/src/features/settings-modal.ts`: `paintToggle` shows `'ON'`
  when the flag is true; clicking sends `SET_PREF alwaysOnTop
  !currentAlwaysOnTop()`. Correct.
- `app/internal/game/prefs.go`: SET_PREF stores the bool directly;
  `AlwaysOnTop()` returns it directly; `RestorePrefs` seeds it directly.
- `app/cmd_lifecycle.go`: `status --json` emits `AlwaysOnTop: cfg.AlwaysOnTop`
  verbatim (read from config.json).
- `desktop/src-tauri/src/lib.rs`: `build_window` uses
  `.always_on_top(prefs.always_on_top)`; `apply_prefs` calls
  `set_always_on_top(wanted.always_on_top)`. Correct.

So the inversion is **not in dexel's logic**. The two live hypotheses:
1. **Stale build** — the owner tested a desktop shell built before the pref
   existed (it once hardcoded `always_on_top(true)`). A fresh `Dexel.app` from
   Phase 1 may simply behave correctly. TEST THIS FIRST on the Mac: toggle ON
   in Settings → the window must rise above other windows; toggle OFF → it must
   return to normal stacking (NOT sink below everything). Screenshot both.
2. **Platform stacking quirk** — on macOS `set_always_on_top` sets an
   `NSWindow` level; on Linux/GTK it is a `keep_above` hint and some
   compositors mis-handle clearing it (this is why the owner saw it on Linux
   and not the same way on Mac). If the Mac behaves correctly, the Linux
   inversion is a **GTK/Mutter-specific** issue: note it in the verdict as a
   Linux follow-up the owner must reproduce in their own GNOME session (the
   Linux dev box runs headless Wayland and cannot display the shell). Do NOT
   "fix" it by negating a value — that would break the Mac to patch Linux.

If the Mac itself shows a real inversion on a **fresh** build, THAT is a genuine
bug — root-cause it (check `applied` seeding vs `build_window`'s creation value,
and whether the first focus event's `apply_prefs` no-ops correctly), fix it in
`lib.rs` (yours), keep cargo fmt/clippy/test green, and screenshot both toggle
states proving it. Add a unit test that pins the corrected apply direction.

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

## VERDICT — 2026-08-25, run on the owner's Mac (macOS 26.6.2, arm64, M-series)

**Headline: the frameless shell works correctly on macOS. There was no macOS
bug to fix. What the owner saw was a STALE `/Applications/Dexel.app` built a day
before the frameless work existed.** Phases 0, 1 and 3 are done and verified on
screen. Phase 2 (install.sh) is NOT done — stopped there at the owner's request.

- **HEAD investigated:** `b97a156`, tree clean, in sync with `origin/main`.

- **Phase 0 — did `dexel open` open the browser?** **No — the brief's premise was
  wrong for this machine.** `/Applications/Dexel.app` *did* exist (dated
  2026-08-24 10:34) and `dexel open` launched it. It was simply **pre-frameless**:
  the frameless commit is `70b5409` (2026-08-25 16:03), a day later. Proven two
  ways — the timestamps, and the stale binary containing **none** of the frameless
  markers (`shell=1`, `remote-urls`, `core:window:allow-minimize`,
  `core:window:allow-start-dragging`). Screenshot `00-stale-applications-app.png`
  shows it with a **native macOS title bar and red/yellow/green traffic lights**.
  A second stale shell (pid 1815) was also running from
  `desktop/src-tauri/target/…`, started 15:59 — four minutes before the frameless
  commit. Both are the whole explanation for "not frameless like Linux".

- **Phase 1 — did a fresh `/Applications/Dexel.app` come up frameless? YES.**
  Built with `bash scripts/build-sidecar.sh` then `cargo tauri build`, installed
  to `/Applications`. Checklist, each verified by me:
  - [x] No native title bar, **no traffic lights**, square corners.
  - [x] dexel's own pixel titlebar with `☰` `−` `✕` (all three at the right; the
        left holds the coin/LV HUD — the brief said `☰` was on the left, it is not).
  - [x] `−` minimizes (`AXMinimized` false→true); `✕` closes the window, the shell
        process exits, and **the runtime keeps running** (`running: true`) — the
        "window is a view" contract holds.
  - [x] Dragging the pixel titlebar moves the window (+100,+100 → exact);
        dragging **on a button does not move it**.
  - [x] Game fills the window edge-to-edge, no letterbox. Opens at 1280×800
        (2× the 640×400 surface). A shrink-drag **snapped to exactly 640×400**,
        and dragging far inside the minimum **clamped at 640×400**.
  - [ ] **Not verified: sound and click-to-react.** Audio can't be judged from a
        screenshot and I stopped before testing it. The only unverified line.
  - Note: the **1920×1200 maximum could not be exercised** — this display is
    1800×1169 logical, so there is no room to reach it.
  - Screenshots in `/tmp/dexel-mac-shots/` (`02-fresh-fullscreen.png` is the
    money shot; `00-stale-applications-app.png` is the before).

- **Any real macOS bug found?** **None in the frameless shell.** Two things worth
  the owner's attention, neither a defect in the Mac port:
  1. **The always-on-top toggle only takes effect on the window's next focus
     event**, not the instant you flip it. This is *documented design*, not an
     oversight: the shell has no IPC into the page (deliberately — the loopback
     origin is denied `shell:allow-execute`), so it re-reads prefs by running
     `status --json` on `WindowEvent::Focused(true)` (`watch_for_a_moved_runtime`
     in `lib.rs`). Consequence: flip the toggle, look at the window, nothing
     appears to happen — which is **exactly the "it did nothing" the owner
     reported**. Worth fixing only if the owner wants it; it needs a poll or a
     channel, i.e. a real design decision, so it is left for them.
  2. **A modal `<dialog>` makes the titlebar unclickable.** With Settings open,
     its backdrop renders the page inert and `−`/`✕` stop responding. On a
     decorated window the native buttons would still work; on a frameless one the
     titlebar is the *only* way to close, so an open modal traps the window until
     Esc/G. Cost me a false "✕ is broken" reading mid-test. Minor, but real.

- **Phase 2 — install.sh:** **NOT DONE.** Not started; stopped at the owner's
  "leaving it for now". `install.sh` still installs no `Dexel.app` on macOS.
  Note for whoever picks it up: the brief suggests `scripts/mac-release.sh` as a
  possible path — it exists (45KB) but is the **tagged, signed, notarized,
  uploading** release path and is the wrong tool for a local unsigned install.
  The right recipe is the one used here: `bash scripts/build-sidecar.sh` (host
  triple by default) then `cargo tauri build`, then copy the bundle.

- **Phase 3 — always-on-top on a FRESH Mac build: NOT inverted. It is correct.**
  Measured objectively with a CGWindowList z-order dump, not by eye:
  - OFF (baseline): Dexel `layer=0`; raising iTerm2 puts iTerm2 in front. Correct.
  - ON: after the next focus, Dexel goes to **`layer=5`** and **stays in front of
    iTerm2** when iTerm2 is raised. Correct.
  - Back to OFF: returns to **`layer=0`** and sits **directly behind the app you
    just raised, still above Music/Finder/Brave** — it does **not** sink to the
    bottom. Correct.
  - The Settings label tracked the truth at every step (OFF/ON/OFF), so the UI is
    not inverted either.
  - **CORRECTION (same session, after the fix below).** The bullet that stood
    here said the Linux inversion was "a Linux/GTK problem, not dexel's". That
    was **wrong**, and the evidence contradicting it was already in this
    session's own transcript. It was dexel's, it was not an inversion, and it
    was not platform-specific — see the next section.
  - The Mac's earlier "toggle does nothing" was the **stale build**, compounded
    by the focus-lag, which turned out to be the whole story on Linux too.

## THE LINUX FIX — "reversed toggle" was latency, and it is now fixed

**Root cause.** `apply_prefs` ran only on `WindowEvent::Focused(true)`. But a
stacking preference can only be OBSERVED once the window is NOT focused — so
the user always saw the PREVIOUS setting:

```
toggle ON  -> click away -> not applied yet -> window drops behind   ("reversed")
toggle OFF -> click away -> still applied   -> window stays on top   ("reversed")
```

Those two lines are the owner's Linux report verbatim. **Reproduced on macOS**
with a CGWindowList z-order dump — so it was never a GTK quirk, and negating a
value to "fix" Linux would have broken the Mac exactly as feared, while leaving
the real bug in place on both.

**Fix** (`desktop/src-tauri/src/lib.rs`): `watch_prefs_file` watches
`config.json` — the file every `SET_PREF` writes through to immediately — and
applies the preference when it CHANGES. Focus remains the backstop; both paths
share one "last applied" record so they cannot disagree. The state directory
comes from `status --json`'s `stateDir`, so no per-OS path logic is duplicated
in Rust. Cost when idle: a `stat` of one file twice a second — no process, no
loopback round-trip, which is why a poll is acceptable here and still refused
for the moved-runtime check next door.

**The trap it had to avoid:** the same flag has two shapes —
`status --json` nests it under `prefs`, `config.json` is flat. Crossing them
returns `false` with no error (a watcher stuck on "off" forever). Separate
parser per shape, plus a test asserting they are not interchangeable.

**Verified on the real running app**, three flips, no focus event at any point:

```
start (pref false): layer=0
after ON          : layer=5     <- rose with no click
after OFF         : layer=0     <- returned to normal, did not sink
log: always-on-top is now on  (from config.json)
     always-on-top is now off (from config.json)
```

**Gates, all run and green:** `go build ./... && go vet ./...`;
`bash scripts/test-race.sh` (all packages ok); `npx tsc --noEmit` +
`npm run build` with **no bundle drift**; `cargo fmt --check`,
`cargo clippy --all-targets -- -D warnings`, `cargo test` (46 passed).

**Not verified by me:** that the fix behaves on an actual Linux desktop. The
mechanism is platform-neutral (it changes *when* `set_always_on_top` is called,
not what it is called with) and the Mac proves the delivery path, but a GNOME
session is still the honest confirmation and I cannot run one here.

- **Still open:**
  1. **An open modal makes the titlebar unclickable.** Settings is a native
     `<dialog>`; its backdrop renders the page inert, so `−`/`✕` stop
     responding and a frameless window has no other close affordance until
     Esc/G. Left alone deliberately — it is a frontend change needing its own
     visual gate, and it is not what was asked for here.
  2. **Phase 2** (install.sh builds + installs `Dexel.app` on macOS) — still to
     do. Note the brief suggests `scripts/mac-release.sh`; that exists but is
     the tagged/signed/notarized/uploading path and is the wrong tool. The
     right recipe is `bash scripts/build-sidecar.sh` then `cargo tauri build`.
  3. Sound and click-to-react on macOS remain unverified (audio, not visual).
