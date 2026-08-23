# Moods and the activity line

> **§3 was rewritten on 2026-08-23.** The app-type classification work landed as
> commit `12accf0` ("fix: 'Coding in Brave' is now unrepresentable, not just
> unlikely") while this directory was being written. §3 below documents the
> **new** behaviour, read from the landed code. §1 and §2 (the moods
> themselves) were not part of that change.

Sources: `app/internal/engine/engine.go` (`Engine.mood`),
`app/internal/game/activity_line.go`, `app/internal/game/ticker.go`.
The governing decisions are
[ADR 0010](../adr/0010-mac-first-honest-mechanics.md) and
[ADR 0009](../adr/0009-app-identity-not-titles.md).

---

## 1. The three moods

`engine.Mood` is a closed set of exactly three values, and the constants **are**
the wire strings — the engine emits the wire format directly so no layer in
between has to remember to translate:

```go
MoodCoding  Mood = "coding"
MoodIdle    Mood = "idle"
MoodOnBreak Mood = "onBreak"
```

`docs/ui-spec.md` §6.1 pins `activeState` to exactly these three, lowerCamel.
There is no fourth value and pause did not add one — see §4.

What each mood drives:

| Mood | Character pose | Status dot | Ticker pool | Terminal |
| --- | --- | --- | --- | --- |
| `coding` | `type_a`/`type_b` at 5 fps | `var(--plant)` | "Compiling…", 7 lines | pushes a new line every 350 ms |
| `idle` | `idle`, plus ambient breath/stretch | `var(--screen)` | "Waiting on input…", 4 lines | frozen; last line has a blinking cursor |
| `onBreak` | `sleep` — hands off the keys, hood tipped, floating `z` | `var(--pot)` | "Idle timer running…", 4 lines | last line reads `-- idle --` |

---

## 2. The honesty rules

`Engine.mood` is nine lines of code and every branch is a decision about what
may be claimed:

```go
if !lastKeystrokeAt.IsZero() && now.Sub(lastKeystrokeAt) <= CodingRecencyWindow {
    return MoodCoding
}
if honesty == activity.HonestyGlobal && snap.IdleSeconds > OnBreakIdleThreshold.Seconds() {
    return MoodOnBreak
}
return MoodIdle
```

### `coding` requires a real keystroke, recently

`CodingRecencyWindow = 10 * time.Second`. A counted keystroke within the last
ten seconds means `coding`, and nothing else does. **Mouse motion alone never
produces `coding`** — "scrolling docs is not typing", and the code says so at
the constant.

Note the asymmetry with the economy: mouse activity earns work (0.02/second)
but never earns the *claim* that you are coding. Those are two different
questions and they are answered separately on purpose.

### `onBreak` requires two things, not one

`OnBreakIdleThreshold = 30 * time.Second`. `onBreak` needs **both**:

1. `snap.IdleSeconds` beyond 30 seconds — genuine idleness across every input
   type the provider tracks, and
2. `honesty == activity.HonestyGlobal` — a provider that can actually *see*
   global input.

The second condition is the whole point. A blind provider (`HonestyBlind`)
can never produce `onBreak`, however large its `IdleSeconds` claims to be,
because it cannot know. **"On break because you minimised me" is the exact lie
this guards against**, and it is the sentence ADR 0010 was written around.

In practice: on Linux, if the process cannot open any `/dev/input/event*`
node, the provider reports `HonestyBlind` and Dexel will sit at `idle`
forever rather than ever claiming you went away. That is the intended
degradation, not a bug.

### `idle` is the fallback that claims nothing

Everything else is `idle`. It is the non-claiming answer, which is why it is
also the mood the wire reports while paused (§4) and the mood a blind provider
is permanently stuck at.

`TestMoodHonestyTable` in `app/internal/engine/engine_test.go` is the table
form of all of the above, including the case that matters most: a blind
provider with a huge `IdleSeconds` must return `idle`, never `onBreak`.

### What the moods feed

- **The economy** does not read the mood at all. `WorkUnits` is computed from
  `KeystrokeDelta` and `MouseActive` directly — a mood is a *statement*, not a
  price.
- **`ActiveSeconds` / `IdleSeconds`** in the analytics counters *are*
  mood-derived: `ActiveSeconds` counts ticks whose mood was `coding`,
  `IdleSeconds` counts every other ticked second. See
  [`sessions.md`](sessions.md) §2 — and note that this makes "active seconds"
  a 10-second-window measure, not a measure of seconds spent typing.
- **`ActiveDayMinSeconds = 300`**, the streak threshold, is 300 `coding` ticks
  — i.e. five minutes of `ActiveSeconds`, not five minutes of keystrokes.

---

## 3. The activity line

`game.ActivityLine(mood, appID, appDisplay)` composes one short string from the
honest mood plus the sanitised app identity. It is the **one literal line in the
UI**: the ticker and the terminal are fiction, this line is not.

### The bug that shaped it

Until commit `12accf0` the rule was, effectively, `mood == Coding && !terminal`
→ `"Coding in " + app`. The moment app identity started working on macOS
(commit `b271c2e` fixed a frozen-cache bug), the very first thing it produced
was **"Coding in Brave"**.

That is false, and precisely so. The two signals being joined are:

- `mood == coding` — "a key went down **somewhere** in the last 10 seconds".
  The macOS provider polls a *global* HID timer and structurally cannot know
  which app received the keystroke.
- `appID` — "this application is frontmost **right now**".

Joining them into a work verb is an inference, and for a browser it is an
unsupported one. Same family as "Coding in dexel" and "On break because you
minimised me".

### App types replace substring guessing

The two ad-hoc predicates (`isTerminalApp`, `isBrowserApp`, both
`strings.Contains` over the id) are gone. In their place,
`activity.AppTypeOf(sanitizedID)` is an **explicit table lookup** —
`app/internal/activity/sanitize.go` — over ten classes:

| `AppType` | Count in the table |
| --- | --- |
| `coding` (editors, IDEs) | 20 |
| `browser` | 14 |
| `notes` (notes, docs, reading) | 14 |
| `comms` (chat, mail, meetings) | 11 |
| `design` | 11 |
| `terminal` | 10 |
| `media` | 7 |
| `files` | 1 |
| `unknown` | — the answer for any id not in the table |
| `self` | — answered before the table is consulted |

Four properties are structurally enforced rather than intended:

1. **No substring matching, ever.** The old `strings.Contains(id, "arc")`
   guessed a bucket from letters. `TestAppTypeIsNeverGuessedFromSubstrings`
   pins `arcade`, `my-file-browser`, `hello-kitty`, `chrome-remote`, `bravery`,
   `warpaint` and `terminal-emulator` all to `unknown`.
2. **`appTypes` and `friendlyNames` have identical key sets**, asserted by
   `TestAppTypeAndFriendlyNamesDoNotDrift` (88 entries each). So an app cannot
   be classified without a display name ("Coding in goland") or named without a
   class (silently unknown). The two maps answer the same question about the
   same key, so they are maintained as one thing in two shapes.
3. **`unknown` means "we do not know"**, never "we did not bother". It is a
   first-class answer with its own phrasing, which is why Finder gets a `files`
   class rather than being left to fall through.
4. **`SelfAppID` is in neither table** and is answered directly by
   `AppTypeOf`'s own `IsSelf` check, so no future table row can reclassify the
   one id Dexel may never narrate.

### Phrasing is tiered by what the signals license

Each app type has a pool of phrasings, split into three tiers by how much they
claim. This is the load-bearing structure, so it is worth reading as a
hierarchy:

| Tier | Claims | Offered when |
| --- | --- | --- |
| **`work`** | *the user was typing **here*** | mood is `coding` **and** the type has a `work` pool at all |
| **`atDesk`** | something about the **person** as well as the app | mood is **not** `onBreak` |
| **`always`** | only what the OS said — this app is frontmost | every mood, including `onBreak` |

And the decisive detail: **only `coding` and `terminal` have a non-empty `work`
pool.** Every other type's `work` tier is *empty*, which is what makes
"Coding in Brave" **unrepresentable** rather than merely unreachable. There is
no branch to get it wrong in; the string does not exist in any pool a browser
can select from.

| Type | `work` (mood `coding` only) | `atDesk` | class line in `always` |
| --- | --- | --- | --- |
| `coding` | "Coding in {app}", "Typing in {app}", "Heads-down in {app}" | — | "In the editor" |
| `terminal` | "Coding in {app}", "Typing in {app}", "In the terminal" | — | "In the terminal" |
| `browser` | **none** | "Browsing in {app}" | "In the browser" |
| `comms` | **none** | — | — (presence only) |
| `design` | **none** | — | "In the design tool" |
| `media` | **none** | — | "In the media app" |
| `notes` | **none** | — | "In the docs" |
| `files` | **none** | — | "In the file browser" |
| `unknown` | **none** | — | — (presence only) |

Every pool's `always` tier also carries the shared presence pool: "In {app}",
"Over in {app}", "{app} is up front", "{app} has focus", "On screen: {app}",
"Frontmost: {app}". Each of those says exactly one thing — the OS reports this
application as frontmost — which is true whether the user is typing, mousing, or
out at lunch.

Three refusals are stated explicitly in the code and are worth repeating,
because they are the pattern to follow when adding a type:

- **No "Chatting in {app}"**: Slack being frontmost does not mean a message is
  being written.
- **No "In a meeting"**: Zoom being frontmost is just as likely the launcher
  window.
- **No "Spotify is playing"**: Dexel never sees playback state, and the app is
  as often paused.

`atDesk` lines are withheld on `onBreak` for the same reason — "Browsing in
Chrome" while the user is provably away would be describing an empty chair.

`TestActivityLinePoolsContainNoUnearnedClaims` and `TestActivityLineMatrix` are
what hold this; `TestEveryAppTypeHasAPool` fails if a new type is added without
a row, so the degradation to presence-only is deliberate rather than accidental.

### Why the line varies, and why it does not flicker

The phrasing is drawn from the pool so the panel feels alive instead of printing
one frozen string forever. That immediately creates a risk the code names
outright: **the state broadcast runs at 1 Hz, so a phrasing re-rolled per tick
would flicker once a second and read as a broken widget.**

So the choice is a deterministic FNV-1a hash of

```
(seed, sanitized app id, floor(now / activityLineRerollInterval))
```

with `activityLineRerollInterval = 45 * time.Second`. It therefore changes on
exactly two events and no others: the frontmost app changes, or the 45-second
bucket advances. Between those it is byte-identical however many times
`ActivityLine` is called — `TestActivityLineDoesNotChurnAtOneHertz` ticks two
simulated minutes and asserts exactly that.

Two deliberate choices inside that:

- **The default seed is 0 and there is no `math/rand` import in the file.**
  Variety comes from the app and the clock, so a screenshot taken at a known
  time is reproducible — which the project relies on elsewhere (`screenLines`
  is server-owned specifically to stay "deterministic and
  screenshot-reproducible").
- **The mood is not part of the hash.** It selects the *pool*, so a mood change
  can still change the line — but that is a real state change the user is
  watching happen, with the mood dot next to the line changing too. It is not
  churn.

### Length is handled by not offering the line

`maxActivityLineLen = 34`, matching `#status-line`'s width. `pick` renders every
pool entry *first*, then offers only the ones that fit for this app's display
name — so a long friendly name ("Affinity Designer") simply never selects a
phrasing that the frontend would clip. If nothing fits, the shortest rendering
wins, on the grounds that being clipped is a display problem and not a lie.
`TestActivityLineFitsTheStatusLine` covers it.

### Dexel still never narrates itself

`appID == ""` or `AppTypeSelf` short-circuits before any pool is consulted:
`"Coding"` while coding, `"Working..."` otherwise. Those two strings stay fixed
rather than joining the pools, because with no app to name there is nothing left
to vary except the mood — and the mood is already on screen as the dot beside
the line.

`activity.SelfAppID` exists because Dexel has no text input, so nobody has ever
typed a character into it; clicking your companion's window right after typing
in your editor produced **"Coding in dexel"**.
`TestActivityLineNeverClaimsYouTypedInDexel` is the regression test.

This is deliberately **not** extended to browsers or chat apps as a *self* case
— you really can type in those. What changed in `12accf0` is that the *work
verb* is no longer offered for them, which is a different and stronger fix than
special-casing ids.

### Where the line is *not* the server's

`#status-line` renders the server's `activityLine` verbatim, truncated to 34
characters — with exactly one exception: while `state.paused` is true the
frontend substitutes `"PAUSED — tracking is off"`. That is the only string in
the status panel the client composes itself, and it is composed *from a server
field*, not invented.

### The fiction, kept separate

For contrast, since it is the same panel: `#ticker` and `#terminal` are
compile-time `[]string` pools in the backend (`game/ticker.go`), partitioned by
mood, selected by a deterministic formula
(`pool[(sprintIndex*7 + tickCount) % len(pool)]` for the ticker, the same shape
with `*11` and a separate counter for the terminal). Nothing in either pool is
derived from anything on the user's machine.

`TestTickerLinesNeverEqualActivityLine` exists to keep the two from converging.
The separation is spatial and typographic in the UI as well as structural in the
code: the fiction is allowed to be flavourful precisely because the one true
line sits next to it and stays literal.

Note that the activity line is now *also* varied and pooled, which makes that
boundary more important rather than less. The distinction is not "one varies and
one does not" — it is that **every** entry in every activity-line pool must be
true of the two signals for its tier, and no entry in the ticker pool needs to
be true of anything.

---

## 4. Pause reports `idle`, and says why separately

While paused, the wire's `activeState` is pinned to `idle` and a separate
boolean `paused: true` carries the reason.

This was a deliberate encoding choice rather than a fourth mood:

- `onBreak` would be ADR 0010's exact lie — "on break because you paused me".
- `coding` is obviously false.
- So the honest encoding is "the mood says nothing, and `paused` says why".

`SetPaused(true)` additionally parks `g.Mood` at `MoodIdle` and clears
`ActiveApp`/`ActiveAppDisplay`, because holding "coding in VS Code" frozen for
the length of a pause — the way the store gate legitimately holds the last
mood for a few seconds of shopping — would be asserting an observation nobody
is making any more. `State()` pins the mood to `idle` again on read, as
belt-and-braces for a hand-restored save that arrived paused with a mood from
somewhere else.

See [`surfaces.md`](surfaces.md) for what pause stops.
