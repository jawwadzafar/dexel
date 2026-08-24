# The activity signal chain

Everything Dexel knows about your workday travels one path, and every stage of
it narrows what is possible to know. This page describes that path as it is
built today, and the structural tests that stop it from widening.

The governing decisions are
[ADR 0002 (activity isolation)](../adr/0002-activity-isolation-and-privacy.md),
[ADR 0009 (app identity, never titles)](../adr/0009-app-identity-not-titles.md),
[ADR 0010 (permissionless capture, honest moods)](../adr/0010-mac-first-honest-mechanics.md)
and [ADR 0019 (CGWindowList and the availability bit)](../adr/0019-app-identity-via-cgwindowlist-and-an-availability-bit.md).

```
  OS input                app/internal/activity/     platform capture, build-tagged
      │                   provider_darwin.go         one file per OS, cgo quarantined
      ▼                   provider_linux.go
  Snapshot ───────────────────────────────────────── the privacy boundary
      │                   6 fields, allow-listed
      ▼
  engine.Engine.Tick()    app/internal/engine/       work units + mood, pure & clock-injectable
      │
      ▼ TickResult        10 fields
  game.Game.Tick()        app/internal/game/         economy, analytics, sessions
      │
      ▼ StateMessage      19 fields, allow-listed
  the WebSocket           app/main.go, app/hub.go    1 Hz broadcast
      │
      ▼
  the frontend            app/frontend/src/          renders; asserts nothing of its own
```

---

## 1. The provider — what the OS is asked

`activity.Provider` (`app/internal/activity/provider.go`) is a four-method
interface: `Start`, `Stop`, `Snapshot`, `Honesty`. The game and the server
import nothing else from the package; every platform capture strategy lives
behind it.

Which one you get is decided at startup by `selectProvider`
(`app/main.go`, ~line 1079) plus the build-tagged `platformProvider()` in
`app/provider_select_{darwin,linux,other}.go`:

| Platform | Provider | Mechanism | `Honesty()` | App identity |
| --- | --- | --- | --- | --- |
| macOS | `DarwinProvider` | `CGEventSourceSecondsSinceLastEventType` on `kCGEventSourceStateHIDSystemState`, polled every 50 ms | `HonestyGlobal`, always | `CGWindowListCopyWindowInfo` → `kCGWindowOwnerName`, re-read every 500 ms |
| Linux | `LinuxProvider` | raw reads of `/dev/input/event*` (evdev), no cgo | `HonestyGlobal` if any device opened, else `HonestyBlind` | **none** — `ActiveApp` is always `""` and `AppIdentityAvailable` is always `false` |
| anything else | blind `FakeProvider` | nothing | `HonestyBlind` | none |
| `-provider=fake` / `-fake-script` / `DEXEL_FAKE_SCRIPT` | `FakeProvider` | a scripted `type`/`mouse`/`idle` timeline, pure function of elapsed time | `HonestyGlobal` | a constant, default `code` / `VS Code` |

Two details worth knowing because they explain observed behaviour:

- **macOS capture asks for no permission at all.** It reads a
  system-maintained "seconds since the last event of this type" scalar. There
  is no event tap, no Accessibility prompt, no run loop, and the event's
  content is never available to be read even in principle. That is the
  entire point of ADR 0010's approach.
- **The non-native fallback is deliberately blind, not a demo.**
  `provider_select_other.go` returns `NewFakeProvider(nil, HonestyBlind)`
  rather than the env-driven demo script, because a demo script on a platform
  we cannot see would fabricate a constant "Coding in VS Code". A blind
  provider that says nothing is the honest degradation.

### The anti-mash coalescing happens here, not in the economy

`activity.MouseSampleInterval = 100 * time.Millisecond` (`provider.go`) is the
single source of truth for the coalescing window, referenced by both providers
and re-exported as `engine.AntiMashSampleInterval`. It used to be three
independently declared `100ms` constants in three files; hoisting it was the
fix for that.

On macOS the coalescing is in `DarwinProvider.sample()`: an event is only
*counted* if `MouseSampleInterval` has elapsed since the last count of that
signal. So no matter how fast a human or a script drives the input:

- `KeystrokeCount` rises by **at most 10 per second**, and
- `MouseActive` is a recency flag whose maximum honest sustained rate is
  **one flag per 100 ms**, which is where `engine.MouseSustainedRate = 10.0`
  comes from — it is derived from the interval, not hand-copied.

This matters for reading the economy: by the time the engine sees a keystroke
count, mashing has already been flattened. See [`economy.md`](economy.md).

---

## 2. `Snapshot` — the privacy boundary

`activity.Snapshot` is the boundary. It has exactly **six** fields, and a
reflection test refuses to compile past a seventh:

| Field | Type | What it can hold |
| --- | --- | --- |
| `KeystrokeCount` | `uint64` | a monotonic count of presses. Never *which* key |
| `MouseActive` | `bool` | a recency flag. Never a position |
| `IdleSeconds` | `float64` | a duration since the last input of any kind the provider can see |
| `ActiveApp` | `string` | a sanitised **application** identity. Never a window title, document, or URL |
| `ActiveAppDisplay` | `string` | a static-table lookup on `ActiveApp` and nothing else |
| `AppIdentityAvailable` | `bool` | whether the *provider* can see app identity here at all |

### How that is enforced structurally

`app/internal/activity/content_free_test.go` does three separate jobs, and the
third is the interesting one:

1. **`TestSnapshotIsContentFree`** walks `Snapshot` by reflection against an
   explicit allow-list keyed by field name *and type*, and fails if the field
   count differs. Every entry in that map is a written justification, not a
   registration. It additionally rejects any field whose name contains
   `title`, `text`, `content`, `keycode`, `key_code`, `clipboard`, `url`,
   `path`, `document`, `message`, `body`, `keyname` or `char` — which catches
   renaming an allowed field into something that smells like content.
2. **`TestFriendlyNamesCarryNoLongText`** caps every display name at 40 bytes
   and rejects control characters, closing the other route by which app
   identity could grow into prose.
3. **`TestDarwinProviderNeverReadsWindowTitle`** reads `provider_darwin.go` as
   *text* and asserts that the expression reading `kCGWindowName` does not
   appear in it. `CGWindowListCopyWindowInfo` hands back the window title on a
   key adjacent to the owner name, so "read the title too" is a
   three-character edit away from looking correct. The allow-list can stop a
   title from becoming a *field*; only a source scan can stop one from being
   read and logged. It scans rather than executes deliberately, so it still
   runs on a Linux CI box with no window server.

The equivalent allow-list tests exist at the two layers downstream:
`app/internal/game/content_free_test.go` (19 fields on `StateMessage`, plus
`SprintView`, `ConfigView`, `StatsView`, `StatCounters`, `CoinBreakdown`,
`DayStat`, `StreakView` and the four session wire types) and
`app/internal/store/content_free_test.go` for what reaches disk. So the
boundary is asserted three times: what may be observed, what may be sent, and
what may be stored.

### App identity: sanitising, and the availability bit

`SanitizeAppID` (`app/internal/activity/sanitize.go`) is the only transform allowed between
"what the OS said" and "what leaves the package". It lowercases, keeps only
`[a-z0-9._-]`, maps a space to `-` without ever emitting a doubled or trailing
dash, **drops** every other byte rather than substituting it, and caps the
result at `MaxAppIDLen = 32` bytes. Dropping is chosen over mapping because
dropping can never reconstruct content, while substitution rules accumulate
bugs.

`FriendlyName` then maps the sanitised id through a hand-written table of **88
entries** (`code` → `VS Code`, `brave-browser` → `Brave`, …), falling back to
the raw id when there is no entry — an honest raw id beats a fabricated
friendly one. A parallel table, `appTypes`, gives each of those same 88 ids a
coarse **app type**; `TestAppTypeAndFriendlyNamesDoNotDrift` asserts the two
maps have identical key sets, so an app can never be classified without a
display name or named without a class. See [`moods.md`](moods.md) §3.

`AppIdentityAvailable` exists because `ActiveApp == ""` was doing two
incompatible jobs. `AppIdentity.Available` (`app/internal/activity/app_identity.go`) spells
out the resulting three-state table:

| State | Meaning |
| --- | --- |
| `Available && ID != ""` | a real app is frontmost, here it is |
| `Available && ID == ""` | looked, and genuinely nothing is frontmost |
| `!Available` | this provider cannot see app identity here at all |

The third case is not "0 app switches"; it is "not measured". Note that this
bit is deliberately **not** folded into `Honesty`: a provider can see every
keystroke system-wide while being unable to name a single app, and degrading
its `Honesty` to suppress app claims would silently also suppress `onBreak`,
trading one lie for another.

### Dexel is transparent to itself

`activity.SelfAppID = "dexel"` and `activity.IsSelf()` exist because the
keystroke signal is global and instantaneous while the app identity is
"frontmost right now". Joining them produced **"Coding in dexel"** — provably
false, since Dexel has no text input and nobody has ever typed a character
into it. Two places consume this:

- `engine.Engine.Tick` does not count a switch into or out of Dexel's own
  window (`app/internal/engine/engine.go`), so `editor → dexel → editor` is
  one continuous stretch in the editor rather than two switches. Pinned by
  `TestGlancingAtDexelIsNotAnAppSwitch`.
- `activity.AppTypeOf` answers `AppTypeSelf` **before** consulting the
  classification table, and `SelfAppID` is deliberately absent from it — so no
  future table row can reclassify the one id Dexel may never narrate.
  `game.ActivityLine` then treats it exactly like "no app identity at all" —
  see [`moods.md`](moods.md) §3.

This is deliberately *not* extended to browsers or chat apps: you really can
type in those, so "Coding in Chrome" is an honest reading of the same two
signals.

---

## 3. `engine.TickResult` — one second, decided

`engine.Engine.Tick()` samples the provider once and returns a `TickResult`.
The engine is pure and deterministic beyond an injectable clock — no file I/O,
no direct OS access — so both the economy calibration and the honesty rules
are unit-testable without a real provider.

| Field | What it is |
| --- | --- |
| `Mood` | `coding` / `idle` / `onBreak` — the honesty rules, see [`moods.md`](moods.md) |
| `WorkUnits` | this second's economy output, see [`economy.md`](economy.md) |
| `Honesty` | the provider's input visibility, passed through |
| `ActiveApp`, `ActiveAppDisplay` | passed through from `Snapshot` |
| `KeystrokeDelta` | keystrokes counted *this tick*, `0` on the first-ever tick |
| `MouseActive` | passed through |
| `FocusSessionsCompleted` | 0 or 1 |
| `AppSwitches` | 0 or 1; always 0 on Linux |
| `FocusRunSeconds` | length of the current sustained-typing run, 0 when none |

Two guards live in `Tick` and are easy to miss:

- **The first tick contributes zero work, always.** `wasInitialized` is
  captured before the baselines are overwritten, so a provider that starts
  with a nonzero counter — or a restart — cannot hand out work it never
  earned. Pinned by `TestFirstTickNeverAwardsFreeWork`.
- **`Engine.Reset()` is the resume seam.** It clears `initialized`,
  `lastKeystrokeCount`, `lastKeystrokeAt`, `lastActiveApp`, `focusRunActive`
  and `focusRunStart`. Without it, resuming from a pause would inherit a stale
  keystroke baseline, briefly claim `coding` for typing that happened ten
  hours ago, pay a focus bonus for a "sustained" run with a ten-hour hole in
  it, and count one fabricated app switch. See [`surfaces.md`](surfaces.md) on
  pause.

`TickResult.SeesGlobalInput()` is a **method**, not a field — it just reports
`Honesty == HonestyGlobal`. It exists so the session idle auto-end has a name
for the one honesty question it depends on, without adding data to the
boundary.

---

## 4. Into the game, and onto the wire

`game.Game.Tick(r)` folds one `TickResult` into state. The order inside it is
load-bearing and documented at each step in `app/internal/game/game.go`:

1. `checkSessionAutoEnd` — first, on the *pre-existing* session watermark, so
   a session that went stale while the process was closed ends backdated to
   the last activity actually seen rather than to "now".
2. `recordStats` — the analytics tally, **unconditionally**.
3. `advanceSessionActivity` — session bookkeeping, also unconditionally.
4. **the store gate** — if any client holds the store open, return here.
5. mood/app assignment, per-signal work accrual, sprint progress, payout.

The split at step 4 is the one asymmetry in the design and it is intentional.
The economy freezes while the store modal is open, because the game cannot
know whether a keystroke was aimed at the store, so it must not claim work for
it. The analytics counters do **not** freeze, because they are a passive tally
of the same already-honest signal and shopping is a few seconds inside a
tracked session. Details on both in [`economy.md`](economy.md) and
[`sessions.md`](sessions.md).

The server broadcasts `game.Game.State()` — a whole `StateMessage` — once per
second from the single-owner loop in `app/main.go`. `game.Game` does no
locking of its own; that loop *is* the lock, and every mutation of game state
happens on it.

### What the frontend is allowed to do with it

It renders it. The client never asserts state the server did not send, and
never re-derives a number the server could have computed: the activity line,
the level, the streak, and `isActive` on a history day all arrive finished. The
one place the frontend adds behaviour of its own is presentation timing — the
character's ambient breathing and stretch beats — and that is explicitly
display-only, reads nothing but `activeState` and
`stats.today.mouseActiveSeconds`, and adds no wire field. See
[`surfaces.md`](surfaces.md).
