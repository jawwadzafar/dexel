# The surfaces — window, HUD, modals, and run modes

What a player can actually see and touch. This page is deliberately *not* the
DOM contract: `docs/ui-spec.md` owns ids, pixel geometry, and the exact
WebSocket schema, and is far more precise about them. This page is what exists
and what it does.

Sources: `app/public/index.html`, `app/frontend/src/**`, `app/main.go`,
`app/cli.go`, `app/cmd_lifecycle.go`, `desktop/src-tauri/src/lib.rs`.

---

## 1. The window

**660 × 460**, and that is also the **minimum** — resizable up, never down.
Native title bar (a frameless look is explicitly deferred), centred on first
launch, **always on top** ([ADR 0007](../adr/0007-always-on-top.md): "a
companion the editor buries never gets seen"). **No transparency** anywhere:
`body` and `#root` are painted opaque, and there is no `.transparent()` call in
the Tauri shell.

The layout inside is a fixed **640 × 400** design, and the *whole* of it is
scaled as one unit by a single `transform: translate(...) scale(...)` on
`#root`. The scale is `min(vw/640, vh/400)`, snapped **down** to the nearest
crisp factor when that costs at most ⅛. At the default window size this gives
exactly **1×**, centred inside a 10 px pillarbox and 30 px letterbox filled
with the same shadow colour, so the leftover area reads as a bezel rather than
a gap.

One consequence worth knowing, because it explains an odd-looking piece of
CSS: a `showModal()` dialog is promoted to the browser's *top layer* and is
therefore **not** affected by an ancestor's transform. Each dialog carries the
same transform itself and positions with custom properties instead of
`left`/`top`. Because scaling is done with `transform` rather than `zoom`, hit
testing needs no coordinate maths at all — there is no `getBoundingClientRect`
and no `clientX` anywhere in the frontend.

---

## 2. The HUD

| Region | What it shows |
| --- | --- |
| **the scene** | the pixel desk: character, chair, keyboard, mouse, beverage, plant, wall, buddy, monitor — built once by `render/scene.ts` and then mutated |
| **the terminal** | exactly 11 fictional code lines; a blinking cursor on the last line while `idle`; `-- idle --` while `onBreak` |
| **title bar, left** | the coin icon + Dev Cash, then `LV n`. **Coin-then-level and nothing else** — a standing owner directive, and the mood dot was deliberately removed from here |
| **title bar, middle** | the session pill (`<name> H:MM:SS`), visible only while a session is active; the `PAUSED` badge, visible only while paused |
| **title bar, right** | the hamburger, which opens the menu panel |
| **the menu panel** | its title is the Dexel's name once named (else `MENU`), then five items: `[S] STORE`, `[A] ACTIVITY`, `[H] HISTORY`, `[W] SESSIONS`, `[P] PAUSE`/`RESUME` |
| **the sprint panel** | `SPRINT: <name>`, a progress bar, and `N / M units` |
| **the status panel** | the mood dot, the activity line, a rule, three ticker lines, and the Dexel's name |
| **the flash** | a transient toast |

The mood dot's colour comes from the mood, or the dimmed screen colour while
paused. The activity line is the server's string verbatim (truncated to 34
characters) — with the single exception of the paused substitution. See
[`moods.md`](moods.md) §3.

### The character's animation

Nine frames, all presentation-only: `render/scene.ts` sends no action, reads
nothing but `activeState` and `stats.today.mouseActiveSeconds`, and adds no
wire field.

| Frame | Driven by |
| --- | --- |
| `idle` | `activeState: "idle"` |
| `type_a` / `type_b` | `activeState: "coding"`, alternating at 5 fps |
| `mouse` | a **rise** in `stats.today.mouseActiveSeconds`, held 8 ticks |
| `sleep` | `activeState: "onBreak"` |
| `breath` | the ambient scheduler, every 4–6 s (jittered), 600 ms long |
| `stretch` | the ambient scheduler, every 18–34 s (jittered), 1.2 s long |
| `cheer_a` / `cheer_b` | `onCelebrate()` only, 1.4 s |

Precedence, highest first: **celebration → sleep → mouse → typing → ambient**.
Ambient plays only while `idle`, never while coding — that would be motion
competing with the typing animation, and the typing animation is the one that
means something.

Three honesty properties hold this together:

- **The mouse pose is signal-driven, not decorative.** The hand moves to the
  mouse exactly when the player's really did, and never otherwise. Inventing a
  periodic mouse beat would be the client asserting state the server never sent.
- **Celebration has exactly two triggers, both server-originated**: the
  `sessionComplete` message (sent only for a session the server actually
  *kept*, so a discarded sub-60 s session can never be celebrated) and
  `flash{kind:"sprint"}` (broadcast from one place only — the tick loop, when a
  sprint completed). It is deliberately *not* wired to `kind:"session"`,
  because that kind also carries "Session started." and the too-short notice.
- **`onBreak` suppresses celebration entirely**, because the sleep pose means
  30 s+ of genuine idleness and an auto-ended session would otherwise have a
  sleeping Dexel cheer at an empty chair.

The ambient stretch band stops at 34 s on purpose: `idle` is bounded above by
`OnBreakIdleThreshold` (30 s), so a band centred beyond that would mean the
stretch essentially never played. Both countdowns keep running in every mood
and only *fire* while eligible, so a stretch that came due mid-keystroke plays
as soon as the hands come off the keys — which is when a person stretches
anyway.

**Doc drift found:** `docs/ui-spec.md` §10.1 says the mouse pose is held
"~1.4 s". The code holds it for `MOUSE_HOLD_TICKS = 8` on a 200 ms timer, i.e.
**1.6 s**, and the constant's own comment says so. The spec is the stale one.

Two invariants exist purely because of an observed bug: the scene subtree is
built **once** and then mutated (nothing on a render path creates, removes or
clears an element), and all nine frames are **stacked**, each permanently
pointed at its own file, so a frame swap is two style writes with no image
decode. The form layers are hidden with `opacity: 0` rather than
`display: none` — an unpainted layer's CSS mask is never decoded, so the first
appearance of each new pose masked the tint away entirely and the character
flashed **white** for one composited frame.

Cost: one 200 ms interval, and the composite is only repainted when the frame
actually changed. An idle Dexel repaints about 3 of every 25 ticks and nothing
in between; `onBreak` repaints nothing at all.

---

## 3. The modals

**Five exist.** There is no settings modal and no achievements modal.

| Modal | Opens by | Closes by | Gates anything |
| --- | --- | --- | --- |
| **Store** | click, `[S]`, or **`Tab`** | X, `[S]`, `Tab`, Esc | **yes** — freezes earning, §3.1 |
| **Activity** | click or `[A]` | X, `[A]`, Esc | no |
| **History** | click or `[H]` | X, `[H]`, Esc | no |
| **Sessions** | click, `[W]`, or automatically on a `sessionComplete` | X, `[W]`, `NICE`, Esc | no — "gates nothing; no open/close action is ever sent" |
| **Onboarding** | **only** the server's `onboarding: true`. No button, no key | `SAY HELLO`, `SKIP`, Enter, Esc | no, but Esc has a side effect — §3.2 |

The menu panel is not a dialog: `[M]` toggles it, and it closes on Esc, on an
outside mousedown, or on any menu item click. It deliberately does **not** take
modal keyboard ownership, so `[S]`/`[A]`/`[H]`/`[W]`/`Tab` keep working whether
or not it is open.

**Keyboard ownership** is one global handler with a strict order: a text-entry
target returns immediately (this is a real bug fix — typing the name "sasha"
used to fire `[S][A][S][H][A]` and open three modals over the input); then
whichever modal is open owns the keyboard entirely; then the menu's Esc; then
the launcher letters. **Esc is never intercepted** — every modal relies on
native `<dialog>` behaviour and hangs its cleanup on the dialog's own `close`
event, which is what makes "every dismissal path does the same thing" true
rather than four separate code paths.

**A verified gap:** the menu's pause button is labelled `[P] PAUSE` /
`[P] RESUME`, but **`[P]` is not bound to any keydown handler.** The bracketed
hint is a label with no key behind it; pause and resume are reachable only by
clicking that button, or from the CLI. Filed in [`BACKLOG.md`](BACKLOG.md) §4.

### 3.1 The store gate, end to end

This is the one modal that changes the game's behaviour, and the chain is worth
following because it is a good example of the project's "server is the only
source of truth" rule:

1. Opening sends `STORE_OPEN`; the dialog's `close` event — which **every**
   dismissal path funnels through — sends `STORE_CLOSE`.
2. The server records the hold in `openStoreConns`, **keyed by connection id**.
   `StoreOpen()` is `len(set) > 0`, so one client's close can never release
   another client's hold.
3. `Game.Tick` returns early while the gate is held: no mood, no active app, no
   work accrual, no progress, no Dev Cash, no XP, no sprint completion. The
   last honest mood is *held*.
4. The engine is **still ticked every second**, so its keystroke baseline keeps
   advancing and a shopping burst cannot retroactively count as work.
5. A disconnect fires a synthetic, connection-scoped `STORE_CLOSE`; a
   reconnect re-asserts `STORE_OPEN` under the new id, and re-asserts once more
   if a state frame contradicts a hold it still wants.

Not frozen: analytics, session counters, session auto-end, and the cosmetic
ticker. See [`economy.md`](economy.md) §8.

### 3.2 Onboarding

Opened and closed **solely** by the server's `onboarding` bool, which is decided
exactly once at boot as `(no save existed) && (no name in config.json)`. The
client never decides it and never holds the modal open against a `false`.
`loadOrImport` is deliberately conservative: a tampered, future-schema or
unreadable save all count as "existed", so a user who *did* have a save is never
shown a first-run greeting.

Submitting equips the chosen starter hoodie colour first and *then* sends
`SET_NAME`, so "Hello, `<name>`!" is the toast you are left looking at.
**A bare Esc is treated as a skip** and sets the default name: "there is no path
out of here that leaves the Dexel nameless", because leaving the flag up would
nag again on the next boot.

---

## 4. Pause

Pause is the user withdrawing consent to be observed, so **Dexel stops
observing** rather than sampling and discarding. That distinction is what makes
"nothing accrued" structurally true instead of a promise.

What pause does:

- `provider.Stop()` — capture actually stops.
- `engine.Engine.Tick()` is **not called**. No mood is computed, no work unit
  produced, no focus run tracked, no analytics counter moved.
- `g.SetPaused(true)` parks the mood at `idle` and clears the active app, so
  nothing keeps asserting an observation nobody is making.
- An **immediate** save — "a crash right after pausing cannot come back
  tracking".

What keeps running:

- `TickPaused()` credits the second to `pausedSeconds`, its own third bucket.
- **The state is still broadcast every second.** "The UI stays live and shows
  PAUSED — a frozen window would be indistinguishable from a crash."
- Autosave, the terminal scroll, the ticker rotation, and the HTTP server.
- **A running session survives.** Pause is "stop watching me", not "abandon my
  intention". Its idle auto-end deliberately cannot fire while paused; it fires
  on the first real tick after resume, backdated to the last activity actually
  seen.

On **resume**: `Engine.Reset()` runs **before** the provider is restarted, then
an immediate save. Without that ordering, resume would inherit a stale
last-keystroke time (and briefly claim `coding` for typing from before the
pause) and a stale focus run (and pay a bonus for a "sustained" run with the
whole pause missing from its middle).

Pause **persists across a restart**: a Dexel that was paused when it exited
comes back paused, and says so loudly in the log and in `dexel status`. The
provider is *selected but never started* in that case, so a paused Dexel never
observes even for the milliseconds it would take to start and stop one.

`activeState` gained no fourth value — see [`moods.md`](moods.md) §4.

CLI semantics differ from `stop` on purpose: `dexel pause` against nothing
running is an **error**, because "silently exiting 0 would let a user believe
tracking had been turned off when the next `dexel start` would happily start
observing". There is no signal fallback either — no signal can express "stop
the activity provider".

---

## 5. Run modes

One binary, three server modes, two front doors. The full status matrix,
including what is and is not built, is
[`docs/plan/RUN-MODES.md`](../plan/RUN-MODES.md); this is the mechanical
summary.

### The CLI

`dexel` with no arguments means "start if needed, then open". Anything starting
with `-` is treated as the legacy foreground server (recognised by **shape**,
not by a flag list). Otherwise the first word is looked up in a table of 13:

| | |
| --- | --- |
| `start` / `stop` / `restart` | lifecycle of the detached background runtime |
| `status [--json]` | pid, port, url, version, uptime, paused — **exits 0 either way**, because it succeeded at answering the question; the answer is the `running` field |
| `pause` / `resume` | stop and start observing |
| `open` | start if needed, then show the UI |
| `logs [-n N] [-f] [--path] [--truncate]` | the runtime log, `-n` default 40 |
| `serve` | run the server in the **foreground** — the developer path |
| `runtime` | the detached runtime's own entry point; `start` execs this |
| `autostart enable\|disable\|status` | the login item — **never** enabled implicitly |
| `version`, `help` | — |

An unknown word exits 2 with a usage dump. `update` and `uninstall` are
deliberately **absent**: they are specified elsewhere but not built, and "a word
that is listed but does nothing is worse than a word that honestly reports
'unknown command'".

### The three server modes

| Mode | Default address | Takes the lock + writes `runtime.json` |
| --- | --- | --- |
| legacy (`dexel -flags…`) | `127.0.0.1:8080` | no |
| `serve` | `127.0.0.1:8080` | no |
| `runtime` | `127.0.0.1:0` (ephemeral) | **yes** |

`runtime`'s ephemeral port is why every detached runtime no longer fights over
8080 — that default was once hardcoded and a test now guards it. The port is
bound with an explicit `net.Listen` so the real address is knowable before
anything needs it, and printed to stdout as a `DEXEL_LISTENING <url>` handshake
line.

Only `runtime` takes the single-instance machinery, so a developer can run a
scratch `serve` beside a real runtime.

### Background runtime, concretely

`dexel start` discovers whether one is already running (and exits 0 if so —
idempotent by design, because that is the verb an autostart entry invokes),
rotates an oversized log, then `exec`s **itself** with `runtime`, stdio pointed
at the log file and platform detach attributes set, and calls
`Process.Release()` — it never waits, handing the child to init/launchd.
Readiness is then **polled**, every 25 ms up to 10 s, rather than parsed out of
the child's output. On failure it dumps the last 20 log lines.

Discovery never trusts a pid on its own: it round-trips an authenticated HTTP
call to the runtime it found, and requires the returned pid to *equal*
`runtime.json`'s — the last defence against pid reuse. `runtime.json` is removed
on clean exit only; a crash leaves it, and the next failed round-trip resolves
it.

**Three layers of single-instance**, in order: the OS lock on `runtime.lock`,
the explicit `net.Listen` failure, and `runtime.json` plus a live round-trip
used by the CLI *before* it ever spawns.

### The two front doors

**Browser** — the server serves its own embedded frontend at the loopback URL.
This is the path that actually ships today.

**The desktop window** (`desktop/`, Tauri, [ADR 0015](../adr/0015-tauri-desktop-shell.md))
**attaches; it does not spawn.** It resolves the `dexel` binary, runs
`status --json`, starts a runtime if there is none, and points a webview at the
URL. Its exit handler is deliberately empty of termination logic: **closing the
window terminates nothing.** The first version of this shell spawned its own
child and killed it on close, so `dexel status` said "not running" while it was,
and closing the window silently stopped capture. That history is recorded in the
module docs.

Built and running on macOS arm64; other platforms' bundles are unbuilt.
`dexel open` falls through to the browser whenever the desktop app cannot be
launched, and that fallback "is not a consolation prize — it is the path that
actually ships today".

### A third, developer-only surface

Appending `?dev=1` to the URL puts the frontend in a mode that **never opens a
WebSocket at all**, driven instead by `window.devApply` and friends. Worth
knowing exists, because a screenshot taken in that mode is not the real game —
which is the exact failure this project has a hard rule against.

---

## 6. Timings

| | Interval | What it drives |
| --- | --- | --- |
| `tickInterval` | 1 s | the engine tick, the economy, **and the state broadcast** |
| `terminalInterval` | 350 ms | pushes a terminal line |
| `tickerInterval` | 2.5 s | rotates a ticker line |
| `autosaveInterval` | 30 s | writes `state.db` |
| frontend `TICK_MS` | 200 ms | the character's frame timer |
| cursor blink | 500 ms | the terminal cursor |
| WS reconnect backoff | 500 ms → 8 s | doubling |

**There is no separate broadcast ticker**; the state broadcast interval *is*
`tickInterval`, plus one on connect and one immediately after any mutation.

**Observation:** the 350 ms terminal branch mutates the buffer but does **not**
broadcast, so the client only ever receives `screenLines` on the 1 s state
frame. The visible effect is roughly three lines advancing at once, once a
second, rather than a smooth 350 ms scroll. No test covers this and no comment
acknowledges it. It may well be intentional — the 1 Hz broadcast is the
documented contract — but the constant's stated purpose is not what the user
sees. Filed in [`BACKLOG.md`](BACKLOG.md) §4. *(Inference from reading the
loop; not verified against a running build.)*

---

## 7. The lifecycle HTTP surface

Four token-gated endpoints exist **only** on a `runtime`-mode process:
`GET /api/lifecycle/status`, and `POST` for `stop`, `pause` and `resume`. On
`serve` and legacy they are simply absent and 404 — "rather than existing in
some half-authenticated form with nothing to check a token against".

The token is minted per start and lives only in `runtime.json` (mode `0600`).
`requireLifecycleToken` answers 401 for a missing header and 403 for a wrong
one, comparing with `subtle.ConstantTimeCompare` after a length branch, and
**never sets any `Access-Control-*` header**. Each route is registered under its
one exact method, so an `OPTIONS` preflight 404s and a browser can never send
the real cross-origin request.

**`stop` is deliberately not a WebSocket action**: "a web page must never be
able to kill the runtime". It reaches the runtime only through the token-gated
endpoint. `pause` and `resume`, by contrast, exist on *both* the endpoint and
the WebSocket, because the UI legitimately offers them — and the endpoint
**waits** for the action to be applied before answering 200, so a 200 means
tracking really is off now. A loop that cannot accept the action within 3 s
answers 503 rather than a fabricated 200.

`stop` is endpoint-primary with a signal fallback: SIGTERM, 5 s, then a **loud**
hard kill that warns "up to the last 30 seconds of progress since the previous
autosave may be lost", then 3 s, then "STILL alive after a hard kill".

The WebSocket itself is `/ws`, with `OriginPatterns` restricted to
`127.0.0.1:<port>` and `localhost:<port>`. Malformed JSON is answered with an
error flash and the socket stays open — the raw frame is read deliberately
rather than through a typed helper, because the typed helper would close the
connection on a bad payload.

The complete client→server action list is `BUY_ITEM`, `BUY_TINT`,
`EQUIP_ITEM`, `SET_NAME`, `PAUSE`, `RESUME`, `STORE_OPEN`, `STORE_CLOSE`,
`SESSION_START`, `SESSION_STOP` — ten, and anything else is answered with
`unknown action`. Server→client message types are `catalog` (once, on connect),
`state` (1 Hz), `flash`, and `sessionComplete`. There is no ack: every success
is answered by a state broadcast plus a flash, every failure by an error flash.

---

## 8. Autostart

Never enabled by anything except an explicit `dexel autostart enable`. Not the
installer, not `dexel start`, not first run. `status` **always asks the OS** and
reports honestly when `config.json` disagrees, because the OS is authoritative.

| Platform | Mechanism | Identifier / path | Runs |
| --- | --- | --- | --- |
| macOS | launchd user agent | `com.jawwadzafar.dexel` at `~/Library/LaunchAgents/com.jawwadzafar.dexel.plist` | `<program> runtime` |
| Linux | systemd user unit (preferred) | `dexel.service` at `~/.config/systemd/user/` | `<program> runtime` |
| Linux | XDG autostart (fallback) | `dexel.desktop` at `~/.config/autostart/` | `<program> start` |
| Windows | HKCU Run key | value name `dexel` | — |

The launchd plist uses `KeepAlive = {SuccessfulExit: false}` and that is
load-bearing: restart if it crashed, do **not** restart after a clean
`dexel stop`. A bare `KeepAlive = true` would make `dexel stop` unstoppable.

Linux's `--linger` (keep running with nobody logged in) is never implied: "a
companion that tracks *your keyboard* running with nobody logged in is a
surprise, not a sensible default". `Disable` on Linux probes both mechanisms
regardless of what `config.json` records, so switching distros cannot leave an
orphaned entry.

### The login item is named "Dexel Runtime" ⚠ being renamed right now

> **This subsection is in flight.** The working tree (uncommitted at the time of
> writing) renames the Tauri sidecar from `Dexel Runtime` to **`Dexel`** and the
> shell's own main binary from `dexel` to **`dexel-desktop`** — resolving the
> same case-insensitivity collision from the other direction, so the GUI shell
> gets the distinct name instead of the daemon getting a spaced one.
> `bundleServerExecutables` gains `"Dexel"` at the front of its probe order and
> keeps the two older names so pre-rename bundles still resolve. Nothing below
> is wrong about the *committed* build; all of it is about to change. When that
> lands: update this subsection and add a `CHANGELOG.md` entry.
>
> A side effect worth noticing: with `mainBinaryName: "dexel-desktop"`, the
> shell's binary name finally matches the `desktopAppName = "dexel-desktop"`
> that `findDesktopApp` has always looked for on `PATH` — two names that never
> met until now.


macOS names a background login item after **the executable actually exec'd** —
not the launchd label, not the enclosing bundle. Under the old name the Login
Items pane read, verbatim, `dexel-server`.

It cannot simply be `Dexel`, because the Tauri shell's own main binary is
already `Contents/MacOS/dexel` and the default APFS volume is
case-*in*sensitive, so they would be one file — and the bundler copies external
binaries *before* the main binary, so the GUI shell would silently overwrite the
Go daemon and launchd would open a window at every login instead of starting the
runtime. The old name is still probed so a pre-rename bundle does not silently
fall through to the bare-binary plist.

The icon and the "unidentified developer" subtitle in that pane require a paid
Developer ID and are **not fixable in code**.

The launchd path carries a dated honesty table in
`app/internal/autostart/launchd_darwin.go`: bootstrap/bootout/print round-trip,
enable-twice idempotency, disable-removes-everything and the bundle-attributed
plist are all **verified on hardware**; load at actual login and the
crash-restart behaviour are **never observed**; and that the pane now reads
"Dexel Runtime" is **not verifiable from a shell** — it needs a human looking at
the pane.
