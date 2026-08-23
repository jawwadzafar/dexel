# 0019 — App identity from CGWindowList, and saying so when we can't see it

Status: accepted (2026-08-23, after measuring the production LaunchAgent on the owner's machine)

## Context

Dexel's foreground modes appeared to name the frontmost app correctly, but the
production mode — the runtime as a launchd LaunchAgent installed by
`dexel autostart enable`, `ProcessType=Background` — named no app at all.
`Snapshot.ActiveApp` was empty forever, so the UI read "Working..." and the
Activity modal's app-switch counter sat at 0 while keystrokes and active
seconds accumulated normally (2275 keystrokes, 0 switches on the owner's real
day). App identity was silently dead in the one mode that matters most.

The suspected causes were all contextual: no `Info.plist`/bundle context,
`ProcessType = Background`, not being registered with the window server or
LaunchServices, being a non-main executable inside an `.app`, session
attachment. **Every one of them was wrong.** Measured with a purpose-built
Objective-C probe, same binary, run first from a terminal and then under a
throwaway LaunchAgent mirroring the real plist's shape:

- `CGSessionCopyCurrentDictionary()` returned a live session with
  `onConsole=1` under launchd. The agent *is* attached to the GUI session.
- `[[NSWorkspace sharedWorkspace] frontmostApplication]` returned the
  **correct** app under launchd on a one-shot query. `runningApplications`
  had all 90 entries. `ProcessType=Background` costs nothing here.

The real fault is not contextual at all — it is **temporal**. `NSWorkspace`'s
running-application state is a cache AppKit refreshes from LaunchServices
notifications delivered on the process's **main run loop**. A Go server never
runs a `CFRunLoop`: `main` blocks in `http.Serve` and sampling happens on a
goroutine. So the cache is populated by the first query and then **never
updates again**.

A probe of exactly the daemon's shape (no run loop, polling from a secondary
thread, 24 samples over 48s) with focus changed four times via `osascript`:

```
[ 0] NSWorkspace=Brave Browser  (running=91)  CGWindowList=Brave Browser
[ 6] NSWorkspace=Brave Browser  (running=91)  CGWindowList=Finder
[12] NSWorkspace=Brave Browser  (running=91)  CGWindowList=Terminal
[15] NSWorkspace=Brave Browser  (running=91)  CGWindowList=Finder
```

`NSWorkspace` never moved — not once, and `running` never left 91 either.
The real stock binary under a LaunchAgent froze on `"In Finder"` across three
focus changes.

Two consequences worth naming explicitly:

1. **The production symptom is the same freeze with a different first
   sample.** The LaunchAgent starts at *login*, when nothing is frontmost
   yet, so the frozen value is `nil` — empty forever.
2. **Foreground mode was never working either.** It froze on whatever app
   launched it, which for `dexel serve` in a terminal is the terminal — a
   plausible-looking answer that would have been just as wrong an hour later.
   The bug was always total; only the frozen value made it look partial.

## Decision

1. **`CGWindowListCopyWindowInfo` is the sole source of app identity on
   macOS.** With `kCGWindowListOptionOnScreenOnly | kCGWindowListExcludeDesktopElements`
   and `kCGNullWindowID`, walk the (front-to-back) list to the first window at
   `kCGWindowLayer == 0` with nonzero alpha, and read **`kCGWindowOwnerName`**
   — the owning application's name. It needs no TCC grant, needs no run loop,
   and is queried fresh every time, so it cannot go stale.

2. **`NSWorkspace` is removed, not kept as a fallback.** A frozen value is
   worse than no value: it is a confident wrong answer, which is precisely the
   ADR 0010 failure mode ("On break because you minimized me"). There is no
   context in which the stale cache is better than admitting we don't know.

3. **`kCGWindowName` is never read.** The window TITLE is exactly what ADR
   0002/0009 forbid — the document you have open, the URL of your tab — and it
   additionally requires a Screen Recording grant, which would destroy ADR
   0010's permissionless property. Either reason alone is disqualifying.
   Measured confirmation that owner-name is the permissionless half of this
   API: under the LaunchAgent the returned dictionaries **do not even contain**
   the `kCGWindowName` key, while `kCGWindowOwnerName` is present and correct.
   Because the two are adjacent keys on the same dictionary, this is guarded by
   a test that scans the provider source for the read expression
   (`TestDarwinProviderNeverReadsWindowTitle`), not merely by a comment.

4. **App-identity availability becomes its own state:
   `Snapshot.AppIdentityAvailable`.** `ActiveApp == ""` was doing two
   incompatible jobs — "I looked, and nothing is frontmost" and "I cannot see
   apps from here at all" — and both rendered as "Working...", so a total
   capture failure was indistinguishable from a real observation. That is the
   property that let this bug survive in production while every other signal
   looked healthy. The state table now reads:

   | `AppIdentityAvailable` | `ActiveApp` | meaning |
   |---|---|---|
   | true | `"code"` | a real app is frontmost |
   | true | `""` | looked; nothing frontmost (bare desktop, all minimized) |
   | false | `""` | cannot see app identity here; claim nothing, and the switch counter is "not measured", not "0" |

5. **This is NOT folded into `Honesty`.** `Honesty` is about *input*
   visibility and the engine gates its `OnBreak` claim on it. A provider can
   see every keystroke system-wide while being unable to name a single app;
   degrading `Honesty` to suppress app claims would silently also suppress
   `OnBreak`, trading one lie for a different one.

6. **App identity is sampled on its own 500ms cadence**, not on the 50ms
   input-poll tick. `CGWindowListCopyWindowInfo` builds a CFArray of a
   dictionary per on-screen window (44 on a normal desktop) where the idle
   timers are a scalar read; doing that 20×/s to answer a question the engine
   consumes once per second would burn CPU in a process whose whole pitch is
   being invisible.

## Consequences

- Verified under a throwaway LaunchAgent (`com.jawwadzafar.dexel.probe`,
  `ProcessType=Background`, `RunAtLoad`, temp `DEXEL_HOME`, fixed port), read
  over `/ws`, focus driven by `osascript`:

  | focus | before (stock) | after (fixed) |
  |---|---|---|
  | Finder | `In Finder` | `In Finder` |
  | Brave Browser | `In Finder` | `Browsing in Brave` |
  | Terminal | `In Finder` | `In the terminal` |
  | Finder | — | `In Finder` |
  | Brave Browser | — | `Browsing in Brave` |

  and `stats.today.appSwitches` went from a permanent `0` to `5` — exactly the
  five focus changes made.

- `Snapshot` grows one `bool`, justified in `activity/content_free_test.go`'s
  allow-list. It is a fact about the *provider's capability*, carrying nothing
  about the user, and is strictly less revealing than the app identity it
  qualifies.

- The Linux provider now states `AppIdentityAvailable: false` rather than
  implying it with an empty string. It is not looking at a bare desktop; it
  cannot look at all.

- Deliberately deferred: **nothing surfaces `AppIdentityAvailable` to the UI
  yet.** The wire contract (`StateMessage`) and `ActivityLine` live in
  `internal/game`, and the honest rendering — distinguishing "no app is
  frontmost" from "app identity unavailable on this platform", and showing the
  app-switch counter as unmeasured rather than as zero — belongs in that layer
  with a `docs/ui-spec.md` change beside it. The provider boundary now carries
  the fact; the presentation of it is a follow-up.

- The general lesson, which is not macOS-specific: **any AppKit/Cocoa API
  backed by a notification-fed cache is unusable from a Go daemon**, because
  there is no main run loop to deliver the notifications. Prefer the
  CoreFoundation/CoreGraphics query APIs, which compute an answer per call.
  `CGEventSourceSecondsSinceLastEventType` (ADR 0010) already had this
  property; that is why input capture never broke.
