# 0021 — A real Windows activity provider: low-level hooks, counted not read, with a GetLastInputInfo watchdog

Status: accepted (shipping unverified on hardware — see *Verification status*)

## Context

Windows has been **blind** for this product's whole life. `provider_select_other.go`
caught every OS that was not macOS or Linux and handed it
`NewFakeProvider(nil, HonestyBlind)`: the game ran, the store worked, sessions
and analytics worked, and **nothing a Windows user typed ever accrued**.
`docs/production-runtime/PLATFORM_NOTES.md` put it plainly — *"dexel runs and
the economy works, but earns nothing from real input"* — and the README and
`install.ps1` both said so out loud. That was honest (`HonestyBlind` is exactly
the machinery for saying "I cannot see"), and being honest about it for a year
is better than the alternative ADR 0010 was written to kill. It was still not a
product on Windows.

Two constraints shape every option:

1. **No cgo.** `scripts/build-release.sh` cross-builds `windows/amd64` and
   `windows/arm64` from Linux, and that matrix is why a release can be cut at
   all (`darwin/arm64` already needs a macOS host and is the exception that
   proves how expensive the exception is). A Windows provider that needs a C
   toolchain does not get built.
2. **ADR 0002/0009's boundary is structural, not promised.** Counts, durations,
   and a sanitized application identity cross it. Key identity, typed text,
   clipboard, window titles, and cursor positions do not — and the enforcement
   has to be something a reviewer can check, not a comment.

Constraint 1 alone rules out most of the Win32 input surface. Every *global*
hook type except the low-level pair (`WH_KEYBOARD`, `WH_MOUSE`, `WH_CBT`, ...)
is implemented by the OS **injecting the hook's DLL into every process that
generates the event**. That requires a native DLL, which requires a C
toolchain, which breaks the matrix.

## Decision

**`WH_KEYBOARD_LL` + `WH_MOUSE_LL`, installed by `SetWindowsHookExW` from a
dedicated `runtime.LockOSThread` thread running a Win32 message loop, via
`golang.org/x/sys/windows` and `syscall.NewCallback` — no cgo.**

Low-level hooks are the only hook types whose callback runs **inside the
installing process, on the installing thread**. Nothing is injected anywhere,
which is what makes them reachable from pure Go — and, as a free consequence,
means the 32/64-bit question never arises: a 64-bit dexel sees a 32-bit app's
typing and vice versa.

Seven decisions follow.

### 1. The callbacks COUNT. They do not identify.

The keyboard callback increments a counter on `WM_KEYDOWN`/`WM_SYSKEYDOWN`. It
dereferences the hook's `KBDLLHOOKSTRUCT` for exactly one field, `vkCode`, and
only to reject the `vkCode == 0` pseudo-event the ABI can deliver — the same
shape as `provider_linux.go` reading an evdev `code` into a one-line predicate
(`code < keyCodeCeiling`) and dropping it. This is ADR 0003's argument
restated for a different ABI: **the key identity is an internal predicate; the
count is what crosses the boundary.**

Two structural guards make that checkable rather than trusted, and both run on
this repo's Linux CI box (`windows_signals_test.go` **parses**
`provider_windows.go` as an AST, so the file's own HARD BOUNDARY comments
cannot satisfy or break the checks):

* `winKbdLLHookStruct` declares `scanCode`, `flags`, `time` and `dwExtraInfo`
  as **blank fields** (`_`). The struct still has to describe the right number
  of bytes for the ABI, but the physical key identity has *no name in this
  program* — reading it means editing the type declaration, where a test is
  waiting.
* `MSLLHOOKSTRUCT` is **not declared at all**. The mouse hook's `lParam` is
  never dereferenced, because its first field is the cursor position (where
  you clicked is what you were doing — content in ADR 0009's sense) and
  `wParam` alone already says which kind of mouse event happened. The same
  test forbids `GetWindowTextW`, `ToUnicode`, `MapVirtualKey`,
  `GetKeyNameText`, `GetKeyboardState`, `GetAsyncKeyState`, `GetCursorPos` and
  `GetClipboardData` — the neighbouring calls that turn a keystroke *count*
  into a keylogger.

### 2. The pure decisions live in an untagged file, and are tested.

`provider_windows.go` is `//go:build windows` and this repo has **no Windows
runner**, so anything left inside it is code nobody executes before a user
does. So the three decisions that are not syscalls live in
`windows_signals.go`, deliberately untagged and tested on Linux: the
coalescing state machine (`winTally`), the eviction rule (`winWatchdog`), and
the image-path narrowing (`windowsAppNameFromImagePath`). This is the same
argument `app_identity.go` already makes for `SanitizeAppID`/`NewAppIdentity`,
applied harder.

It also makes a *cross-implementation* test possible, which is the point:
`windows_linux_parity_test.go` (`//go:build linux`, so both providers compile)
drives `LinuxProvider.handleEvent` and `winTally` with the same input and
asserts they count the same. `MouseSampleInterval` being one shared constant
(see its doc comment) stops the *number* drifting; this stops the *semantics*
drifting.

### 3. One documented divergence from Linux: mouse buttons.

On Linux a mouse button arrives as `EV_KEY` with a `BTN_*` code and is dropped
by **both** branches of `handleEvent` — too high for the keystroke ceiling, not
`EV_REL`. That is an artifact of evdev sharing one code space between keys and
buttons, not a decision anybody made. Windows hands buttons to the *mouse*
hook unambiguously, so on Windows a click refreshes `MouseActive`. It cannot
inflate earning (`MouseActive` is a coalesced boolean, never a count); what it
changes is that an afternoon spent clicking — a designer in Figma, a reviewer
in a browser — is not misread as idle. Pinned by a test so it stays a decision
rather than becoming a discrepancy someone "fixes" the wrong way.

### 4. Honesty is a property of the INSTALL, not of the platform.

`SetWindowsHookEx` can fail: enterprise Group Policy hook restrictions, a
secure/locked desktop at the moment of start. When it does, `Start` returns a
descriptive error and the provider reports `HonestyBlind` — so ADR 0010's
engine gating refuses to read "no input" as "the user is idle", exactly as it
does for a Linux box with no `input` group. *A provider that cannot see must
not be counted as seeing nothing.* Both hooks install or neither does: a
keyboard-only provider would report `MouseActive` false forever while claiming
`HonestyGlobal`, which is a confident wrong answer rather than a missing one.

### 5. Belt and braces: the GetLastInputInfo eviction watchdog.

Windows enforces `HKCU\Control Panel\Desktop\LowLevelHooksTimeout` (300ms by
default) on every low-level hook callback. **A callback that overruns it does
not get an error — the OS stops calling the hook and leaves the handle looking
perfectly valid.** Nothing in the provider's own state changes. So a hook-only
provider cannot distinguish "the user stopped typing" from "we stopped being
told about it", and the second read as the first is *precisely* the field
failure the Linux provider already paid for: a frozen keystroke counter
feeding an idle clock that climbed for nineteen hours while the engine
honestly-but-wrongly accrued a break.

And there is a reason specific to *Go* that makes this more than a
theoretical worry: even a callback that does nothing but atomic increments can
be delayed past 300ms by the runtime underneath it — a stop-the-world GC pause,
a preemption at a safe point inside the callback, a scheduler hiccup on a
loaded machine. We cannot make that impossible; we can only make it rare, and
then detect it. **That is why the watchdog is not optional decoration: it is
the part of this design that covers the failure mode we do not control.**

Two answers, both applied:

* **Keep the callback inside the budget.** Atomic increments only — no
  allocation, no lock a `Snapshot` caller could be holding, no logging, no map.
  All observation state is atomics (`winTally`), and the chained
  `CallNextHookEx` goes through `syscall.SyscallN` on a pre-resolved address
  (escape analysis confirms the variadic does not escape, so the hot path
  allocates nothing).
* **Cross-check against a second, independent source.** `GetLastInputInfo` is
  an OS-maintained tick count of the last input in this session. It is
  content-free by construction — there is no event in it, only a timestamp,
  the same property that makes macOS's `CGEventSource` timers usable under ADR
  0010 — and crucially it is maintained by the OS rather than by a callback of
  ours that can be evicted. If it says input happened while our hooks counted
  none, across four consecutive 30s checks, the hooks were evicted: the
  watchdog posts a thread message and the hook thread reinstalls them (on
  *that* thread, because a low-level hook is bound to the message loop that
  services it).

Four strikes rather than one, because there is a legitimate way for the two to
disagree: input delivered to a *different* desktop (a UAC consent prompt, the
lock screen, Ctrl-Alt-Del). Reinstalling on account of one interval would mean
re-hooking every time the user typed a password. And after a reinstall the
idle clock **restarts** — the evicted stretch was time we could not see, and
handing it to the engine as observed idleness would be the ADR 0010 lie on a
delay. Same invariant, same reasoning, as `LinuxProvider` restarting its idle
clock when a rescan recovers its first device.

The watchdog **abstains** when `GetLastInputInfo` itself fails: with no second
opinion, guessing is what this provider must not do.

### 6. App identity: the foreground window's process, never its text.

`GetForegroundWindow` → `GetWindowThreadProcessId` → `OpenProcess` with
`PROCESS_QUERY_LIMITED_INFORMATION` (the narrowest right that answers the
question, and unlike `PROCESS_QUERY_INFORMATION` it is granted across integrity
levels, so a focused elevated app is still nameable) →
`QueryFullProcessImageName`, sampled on its own 500ms cadence like the darwin
provider's.

From an HWND exactly two things are reachable: the owning **process** (ADR 0009
allows it) and the window **text** (`GetWindowTextW` — the document you have
open, the URL of your tab). We take the first; the second does not appear in
the file, and a test asserts it.

The image **path** is narrowed to its base name immediately, and that is a
privacy decision rather than tidiness: a real path is
`C:\Users\<name>\AppData\Local\Programs\Microsoft VS Code\Code.exe`, and for a
portable or project-local binary it can carry a client name or a repository
name. The directories are dropped in the one function that sees them, which
never logs or stores them. A trailing `.exe` is stripped case-insensitively,
which is what makes the existing `friendlyNames` table — written against macOS
bundle names — work unchanged for `code`, `chrome`, `firefox`, `slack`,
`spotify`, `obsidian`; and it is load-bearing for `IsSelf`, since dexel's own
image is `dexel.exe`.

`AppIdentityAvailable` (ADR 0019's bit) is **sticky**: false until the
mechanism has answered at least once, true from then on. `GetForegroundWindow`
returns NULL both on a bare desktop *and* permanently in a process with no
interactive desktop to look at; reporting the first as "unavailable" would be a
lie, and reporting the second as "nothing is frontmost" is exactly the
conflation ADR 0019 was written to kill. "Have we ever succeeded" separates
them. Note also that app identity is reported **even while input-blind** — the
two capabilities are independent per ADR 0019, so hooks refused by policy do
not stop us naming the foreground app.

### 7. Lifecycle: the message loop is what makes `Stop` cheap.

`Stop` posts `WM_QUIT` with `PostThreadMessageW`, which wakes a blocked
`GetMessageW` immediately — this provider structurally cannot have the problem
BUG-9 was (a blocking `read(2)` that `close()` could not interrupt, which made
`dexel stop` escalate to SIGKILL on every installer gate). The bounded 500ms
ceiling from `provider_linux.go` is kept anyway, and the post is retried inside
it, because `PostThreadMessage` fails if the target thread has no message queue
yet — so the hook thread forces its queue into existence with one
`PeekMessageW` *before* it reports ready. The provider is restartable
(`dexel pause` / `dexel resume` tears the hooks down and puts them back), and
each `Start..Stop` is its own generation object so a straggler from one cannot
blind the next.

## Alternatives considered

* **`WH_KEYBOARD` / `WH_MOUSE` (the non-low-level global hooks).** The OS
  injects the hook DLL into every process that generates input. Needs a native
  DLL → needs cgo → breaks the cross-compile matrix. Also vastly more
  surface: our code would be running inside every application on the machine.
* **Raw Input (`RegisterRawInputDevices` with `RIDEV_INPUTSINK`).** Genuinely
  viable, and it has one real advantage: **no hook timeout, so nothing to
  evict**. Rejected for v1 because it needs a real window — `RegisterClassExW`
  + `CreateWindowExW` + a `WndProc` callback + `WM_INPUT` handling — which is
  more Win32 surface to get right sight-unseen than two `SetWindowsHookExW`
  calls, and because its buffers carry full HID reports (more data adjacent to
  the boundary, not less). It is the documented escape hatch if hook eviction
  turns out to be common in the field; the watchdog exists precisely so we
  will *know* whether it is (each reinstall logs).
* **`GetLastInputInfo` polling alone.** Content-free, trivially reliable, no
  hooks at all — and it yields **idle only**. There is no keystroke count in
  it, so the economy would have nothing to run on. Worse, the resulting state
  ("honest about idle, permanently frozen counts") is not expressible by the
  `Honesty` enum: it would be a provider that looks `HonestyGlobal` while one
  of its two signals is dead, which is a worse lie than `HonestyBlind`. Kept
  as the watchdog's cross-check, which is the job it is actually good at.
* **Counting only while dexel's own window has focus.** ADR 0003 already
  settled this: it makes the premise inert, because the user's real work
  happens in their editor.
* **`SetWinEventHook(EVENT_SYSTEM_FOREGROUND)` for app identity.** An
  event-driven focus source instead of a 500ms poll. Rejected as extra surface
  for the same answer — and ADR 0019's lesson cuts against it: a live query
  beat a notification-fed cache on macOS, and a frozen value is worse than no
  value.

## Consequences

* Windows stops being blind. `provider_select_other.go` is split: Windows gets
  `provider_select_windows.go`, and the residual `!darwin && !linux && !windows`
  file stays — "some OS Go supports that we have never thought about" is a
  permanent category, and a build that silently reused another platform's
  provider would be worse than one that says it cannot see.
* **No new dependency.** `golang.org/x/sys` was already a direct requirement of
  `app/go.mod` (`spawn_windows.go` uses it), and everything here is either
  already typed there (`GetForegroundWindow`, `GetWindowThreadProcessId`,
  `OpenProcess`, `QueryFullProcessImageName`) or a `LazyProc` address resolved
  from `user32.dll`. `GOOS=windows go build` and `go vet` stay clean for both
  amd64 and arm64, with no cgo.
* **Verification status: unverified on hardware, and said so rather than
  implied.** Everything decidable without a Windows box has been decided on
  Linux — the coalescing semantics, the eviction rule and the path narrowing
  are pure tested code; the two privacy boundaries are AST-enforced; the
  cross-build and vet are clean; the syscall surface is written against
  documented signatures. The hook install itself, the message loop, and app
  identity have never run. The README and `install.ps1` are reworded
  accordingly — from "not wired up yet" to "new in this build, field
  verification pending" — because promising a verified provider we have not run
  would be its own ADR 0010 violation. `PLATFORM_NOTES.md` keeps Windows at
  **tier 2**: the capability is there, the verification is not.
* **What a first field session should check**, in order:
  1. `dexel status` reports the windows provider and `HonestyGlobal`; the
     startup log line names both hooks.
  2. Typing in another application advances the keystroke count, and the count
     is *coalesced* — leaning on a key does not farm Dev Cash faster than
     typing.
  3. `IdleSeconds` grows when you stop and resets when you start, and mouse
     motion alone keeps `MouseActive` true without inflating the count.
  4. The activity line names the foreground app, changes when you alt-tab, and
     never says "Coding in dexel".
  5. `dexel stop` completes in milliseconds, not after the 5s grace (the BUG-9
     symptom).
  6. Leave it running for a day and grep the log for
     `activity(windows): ... EVICTED`. Zero occurrences means the watchdog is
     belt to a brace nobody needed; frequent occurrences is the signal to
     revisit Raw Input.
  7. Behaviour across a lock/unlock, a UAC prompt, and sleep/resume — the
     three places the hook and the watchdog interact.
* **Follow-ups, deliberately not done here.** Windows-flavoured friendly names
  (`msedge` → Edge, `devenv` → Visual Studio, `windowsterminal` → Terminal)
  need an `appTypes` entry as well as a `friendlyNames` one —
  `sanitize_test.go` enforces that the two maps never drift — so they are a
  separate change with its own reasoning about app *classification*, not a
  drive-by. Until then `FriendlyName` falls back to the honest raw id. A
  Windows CI runner remains the thing that would actually retire the
  "unverified" caveat.
