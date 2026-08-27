# dexel — platform notes for the background runtime

Companion to `ARCHITECTURE.md`. Everything the CLI abstracts away, written down
per OS so an implementer never has to guess. The rule the whole document serves:
**the user sees one CLI; the differences live here.**

Support tiers, inherited from the repo as it is:

| OS / arch | Runtime | Activity capture | Autostart | Notes |
|---|---|---|---|---|
| linux/amd64 | tier 1 | evdev, needs `input` group | systemd --user | the only platform with CI and real users today |
| linux/arm64 | tier 1 | same | same | cross-compiled, untested on hardware |
| darwin/arm64 | tier 1 (product's primary — ADR 0010) | CoreGraphics polling, permissionless | launchd user agent | needs a macOS build host (cgo) |
| windows/amd64 | tier 2 | WH_KEYBOARD_LL + WH_MOUSE_LL low-level hooks, permissionless (ADR 0021) | HKCU Run key | real capture since ADR 0021; **unverified on hardware** — no Windows runner |
| windows/arm64 | tier 3 | same | same | cross-compiled, never run |
| darwin/amd64 | not built | — | — | omitted from the matrix; add if an Intel Mac ever matters |

**The Windows honesty note changed shape, it did not go away.** Until ADR 0021
Windows fell through to `provider_select_other.go` and was flatly blind: the
store, the UI and the save all worked while Dev Cash never accrued from real
typing. `provider_select_windows.go` ends that — `internal/activity`'s
`WindowsProvider` counts keystrokes and mouse activity globally through two
low-level hooks, with no cgo, so `scripts/build-release.sh`'s cross-compile
matrix is untouched.

What remains to be honest about is now narrower, and it is *verification*
rather than *capability*:

* **Nobody has run it on Windows.** This repo has no Windows runner. The pure
  decisions (anti-mash coalescing, the eviction rule, app-name narrowing) are
  tested on Linux and the two privacy boundaries are enforced by tests that
  parse `provider_windows.go` as an AST, but the hook install, the message
  loop, and app identity have never executed. Windows therefore stays **tier
  2**: the capability is there, the field verification is not. ADR 0021 lists,
  in order, what a first field session should check.
* **The hooks can be refused** — enterprise Group Policy hook restrictions, a
  secure/locked desktop at the moment of start. `Start` then returns a
  descriptive error and the provider reports `activity.HonestyBlind`, so ADR
  0010's engine gating refuses to claim idle or onBreak from it. The old blind
  state is now the *exception path*, and it still exists precisely so nothing
  lies about it.
* **Windows silently evicts a slow low-level hook** (`LowLevelHooksTimeout`,
  300ms by default) with no error and a handle that still looks valid. The
  provider cross-checks itself against `GetLastInputInfo` and reinstalls when
  the two disagree; every reinstall logs one line containing `EVICTED`, which
  is the thing to grep for in `<StateDir>/logs/runtime.log` when a Windows user
  reports that earning stopped.

`dexel status` and the download page must say which of those states a given
install is in, or the first Windows user will file "dexel is broken" — the same
requirement as before, now with three answers instead of one.

---

## 1. Filesystem locations

| | Linux | macOS | Windows |
|---|---|---|---|
| **StateDir** | `$XDG_CONFIG_HOME/dexel`, else `~/.config/dexel` | `~/Library/Application Support/dexel` | `%LOCALAPPDATA%\dexel` |
| state | `<StateDir>/state.db` | same | same |
| config | `<StateDir>/config.json` | same | same |
| discovery | `<StateDir>/runtime.json` (0600) | same | same |
| lock | `<StateDir>/runtime.lock` | same | same |
| sticky port | `<StateDir>/lastport` (0600) | same | same |
| logs | `<StateDir>/logs/runtime.log` | same | same |
| update cache | `<StateDir>/cache/` | same | same |
| **BinDir** | `~/.local/bin` | `~/.local/bin` | `%LOCALAPPDATA%\dexel\bin` |
| desktop app | `~/.local/bin/dexel-desktop` or an AppImage | `/Applications/Dexel.app` (whose main binary is `Contents/MacOS/dexel-desktop` — §3.1.3) | `%LOCALAPPDATA%\Programs\dexel` |
| legacy Rust save (READ ONLY, never removed) | `~/.local/share/dev-companion/save.json` | `~/Library/Application Support/dev-companion/save.json` | n/a |

`DEXEL_HOME` overrides `StateDir` on every platform.

**Linux keeps `~/.config/dexel` deliberately.** It is what
`store.DefaultPath()` returns today, it is where the only real saves in
existence live, and the owner confirmed it. `$XDG_CONFIG_HOME` is honoured so
the value is configuration rather than a hardcode.

**One-time relocation (macOS, Windows only).** Because
`store.DefaultPath()`/`store.ConfigPath()` currently hardcode `~/.config/dexel`
on *every* OS, a from-source run on macOS or Windows put files in the Linux
place. At startup, if `<StateDir>/state.db` is absent and
`~/.config/dexel/state.db` is present, move (never copy) `state.db`,
`config.json`, and any `state.db.invalid` / `.future` / `.corrupt` /
`state.json.imported` siblings into `<StateDir>`, then log one line naming both
paths. Ordering follows `importJSON`'s precedent in
`app/internal/store/db.go`: create the destination, move, and only then treat
the new location as authoritative. Linux never takes this branch.

---

## 2. Detaching a background process

Go cannot `fork()` — the runtime's threads do not survive it — so
`dexel start` `exec`s a fresh copy of itself (`ARCHITECTURE.md` §5). The only
per-OS part is the `SysProcAttr`, in build-tagged files that mirror the
existing `provider_select_{linux,darwin,other}.go` pattern.

### unix (`spawn_unix.go`, `//go:build unix`)

```go
&syscall.SysProcAttr{Setsid: true}
```

`Setsid` makes the child a session leader with no controlling terminal, so
closing the terminal that ran `dexel start` cannot deliver SIGHUP to the
runtime. Stdin is `os.DevNull`; stdout and stderr are the append-opened log
file. After `cmd.Start()`, call `cmd.Process.Release()` and **never** `Wait()`
— the parent exits immediately and the child is reparented to init/launchd.

There is deliberately **no** `chdir("/")`: the runtime opens nothing by relative
path (EMBED-1 removed the last one — `app/embed.go`), and keeping the cwd makes
a dev-mode `-public` override still work if someone passes one.

### windows (`spawn_windows.go`, `//go:build windows`)

```go
&syscall.SysProcAttr{
    CreationFlags: windows.DETACHED_PROCESS |
                   windows.CREATE_NEW_PROCESS_GROUP |
                   windows.CREATE_NO_WINDOW,
    HideWindow: true,
}
```

`DETACHED_PROCESS` denies the child the parent's console; `CREATE_NO_WINDOW`
prevents a console window from flashing; `CREATE_NEW_PROCESS_GROUP` stops a
Ctrl-C in the parent console from reaching it. Constants come from
`golang.org/x/sys/windows`, already reachable via `app/go.mod`'s existing
indirect `golang.org/x/sys v0.47.0`.

### Stopping it

* unix: `dexel stop` prefers `POST /api/lifecycle/stop` (in-process, ordered,
  and the response confirms the save started). Fallback if the HTTP call fails
  but the pid is alive: `SIGTERM`, which the existing
  `signal.Notify(sigCh, SIGINT, SIGTERM)` handler in `app/main.go` already
  handles by saving, stopping the provider and shutting the server down. Escalate
  to `SIGKILL` after a 5s grace (matching the shell's existing 3s + the server's
  own 5s shutdown timeout) and say so loudly — autosave bounds the loss to 30s.
* windows: there is no SIGTERM. The lifecycle endpoint **is** the graceful path
  and the only one; fallback is `TerminateProcess` via `os.Process.Kill()`, with
  the same loud warning. This asymmetry is exactly why `stop` is an HTTP call
  first and a signal second.

---

## 3. Autostart — one CLI verb, three mechanisms

`dexel autostart enable | disable | status`. All three are **user-level, no
sudo, no root, no system-wide daemon**. Never enabled by the installer;
`ARCHITECTURE.md` treats explicit consent as the whole point.

`enable` records which mechanism it used in `config.json`
(`autostart: "launchd" | "systemd-user" | "xdg-autostart" | "windows-run" | ""`)
so `disable` removes the right thing even after a platform's preferred mechanism
changes. `config.json` is the correct home: it is the unsigned, user-editable
file by design (ADR 0014 / `app/internal/store/config.go`), and its doc comment
already anticipates "room for future cosmetic prefs on this same struct".

### 3.1 macOS — launchd **user agent**

Chosen over Login Items. Login Items via `SMAppService` requires a real app
bundle and an Objective-C/Swift call; via AppleScript it requires Automation
permission and a scripting round-trip. A launchd plist is a file write plus one
`launchctl` call, works for a bare CLI with no bundle, is scriptable, is
inspectable by the user, and is what every developer tool on macOS does.

Path: `~/Library/LaunchAgents/com.jawwadzafar.dexel.plist`
(identifier matches `desktop/src-tauri/tauri.conf.json`'s
`"identifier": "com.jawwadzafar.dexel"` — one identity for the product).

There are two shapes of this plist, differing in exactly two things —
`ProgramArguments[0]` and the presence of `AssociatedBundleIdentifiers`.
Which one `enable` writes depends only on whether an installed app bundle is
present; §3.1.1 explains why. Everything supervision-related is identical in
both, and that identity is pinned by a byte-for-byte unit test
(`app/internal/autostart/autostart_test.go`).

**A. With an installed `/Applications/Dexel.app`** (what `enable` writes when it
finds one):

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>              <string>com.jawwadzafar.dexel</string>
  <key>ProgramArguments</key>
  <array>
    <string>/Applications/Dexel.app/Contents/MacOS/Dexel</string>
    <string>runtime</string>
  </array>
  <key>AssociatedBundleIdentifiers</key>
  <array>
    <string>com.jawwadzafar.dexel</string>
  </array>
  <key>RunAtLoad</key>          <true/>
  <key>KeepAlive</key>
  <dict><key>SuccessfulExit</key><false/></dict>
  <key>ProcessType</key>        <string>Background</string>
  <key>StandardOutPath</key>
  <string>/Users/USER/Library/Application Support/dexel/logs/runtime.log</string>
  <key>StandardErrorPath</key>
  <string>/Users/USER/Library/Application Support/dexel/logs/runtime.log</string>
</dict>
</plist>
```

**B. Bare-binary fallback** — no app bundle installed, or `enable --bare`. This
is byte-identical to what shipped before bundle attribution existed: no
`AssociatedBundleIdentifiers` key at all, and `ProgramArguments[0]` is the
resolved `os.Executable()`:

```xml
  <key>ProgramArguments</key>
  <array>
    <string>/Users/USER/.local/bin/dexel</string>
    <string>runtime</string>
  </array>
```

* Absolute paths only — launchd has almost no environment and no `PATH`.
* `KeepAlive → SuccessfulExit: false` means "restart if it crashed, do not
  restart after a clean `dexel stop`". A bare `KeepAlive: true` would make
  `dexel stop` un-stoppable, which is a bug, not supervision.
* `ProcessType: Background` opts into background CPU/IO scheduling — correct for
  a 1 Hz ticker.
* launchd owns the log redirection here, so the child's stdio is launchd's
  business and `dexel start` is not involved.
* `Dexel` inside the bundle is the SAME Go program as the bare `dexel` (Tauri
  ships it as a sidecar; it was called `dexel-server` before §3.1.2 and
  `Dexel Runtime` between §3.1.2 and §3.1.3); it accepts `runtime` and every
  other subcommand, and it resolves the same state directory, because
  `app/internal/paths` derives `StateDir` from `$DEXEL_HOME`/`$HOME` and never
  from the executable's own path. It is a DIFFERENT file from
  `Contents/MacOS/dexel-desktop`, which is the Tauri shell's own main binary
  (`mainBinaryName`) — the GUI window, not the daemon. `enable` never points at
  that one, and §3.1.3 explains the case-folding trap that makes "never" take
  actual code rather than just a shorter list.

Commands: `launchctl bootstrap gui/$(id -u) <plist>` and
`launchctl bootout gui/$(id -u)/com.jawwadzafar.dexel`, falling back to
`launchctl load -w` / `unload -w` on older macOS. `status` = `launchctl print
gui/$(id -u)/com.jawwadzafar.dexel` exit code, plus the plist's existence, plus
`ProgramArguments[0]` read back off disk (so `status` reports which of the two
shapes above is actually installed rather than assuming).

Not designed here: the first launch will prompt nothing (the macOS provider is
permissionless by ADR 0010 decision 1 — that is why it was chosen), so autostart
needs no consent dialog of its own. That same permissionlessness is why moving
the program path from the bare binary into the app bundle costs nothing: there is
no TCC (Accessibility / Input Monitoring) grant tied to the old path to lose,
because there was never a grant.

### 3.1.1 System Settings → Login Items & Extensions: the icon, the name, and "unidentified developer"

Investigated 2026-08-23 on the owner's Mac (macOS 26.6, Apple Silicon), because
the entry under **General → Login Items & Extensions → Allow in the Background**
showed a generic `exec` icon named "dexel", subtitled *"Item from unidentified
developer"* — next to properly-branded entries like Docker. Written down so
nobody re-derives it.

That pane is a UI over **BTM** (Background Task Management, macOS 13+), whose
store is `/var/db/com.apple.backgroundtaskmanagement/BackgroundItems-v*.btm`
(dump it with `sudo sfltool dumpbtm`). Each record carries a `Name`,
`Developer Name`, `Team Identifier` and `Assoc. Bundle IDs`. Apple's own framing
is *responsible code*: the pane shows the name of the app it holds responsible
for the item, resolved through the LaunchServices database.

**What drives the NAME and ICON — three findings, at three different confidence
levels. The difference matters, so it is stated per item.**

1. **`AssociatedBundleIdentifiers` — documented.** `launchd.plist(5)`, verbatim:
   "This optional key indicates which bundles are associated with this job in
   the System Settings Login Items UI. If an app installs a legacy plist the
   plist should include this key with a value of the app's bundle identifier."
   This is the only officially supported lever for a hand-installed
   `~/Library/LaunchAgents` plist, and Apple DTS recommends it repeatedly. So
   `enable` now sets it (to `com.jawwadzafar.dexel`, which is deliberately the
   same string as the launchd `Label` and the Tauri bundle's `identifier`).
2. **Naming an executable *inside* the `.app` — strong circumstantial evidence,
   not documentation.** On this machine, every third-party LaunchAgent that
   shows a real icon and name points at an executable inside an app bundle,
   often a nested one — OneDrive's `StandaloneUpdater.app` and
   `SyncReporter.app`, `Microsoft Defender.app`'s helper, Intune's
   `Microsoft Intune Agent.app`, Company Portal's SSO XPC service. Every one
   that shows a generic icon points at a bare Unix executable — FortiClient's
   `.../FortiClient/bin/*`, XQuartz's `/opt/X11/libexec/launchd_startx`. That
   is why `enable` prefers
   `/Applications/Dexel.app/Contents/MacOS/Dexel` over
   `~/.local/bin/dexel` when the bundle exists. **No Apple document was found
   stating that path-containment alone triggers bundle attribution — and
   §3.1.2 now records that on this machine it did NOT produce an icon.**
3. **The plist's `Label` is irrelevant to the displayed name — confirmed.** The
   standing counter-example is nix-darwin, whose labels are `org.nixos.*` but
   whose `ProgramArguments[0]` is `/bin/sh`: the pane shows entries literally
   named "sh". Attribution follows the process actually `exec`'d, which is a
   second reason to name a real Mach-O rather than a wrapper script. (`Label`
   *is* what MDM matches on — a different feature.) `BundleProgram` is no use
   either: the man page scopes it to `SMAppService` installs only.

**The "Item from unidentified developer" subtitle: NOT fixable without a paid
Apple Developer ID. This is not a workaround-shaped problem.**

That subtitle is the `Developer Name` field, and it is read out of the **code
signing certificate** of the responsible code. Every dexel binary today is
ad-hoc signed — specifically *linker-signed*, which is simply what the Go
toolchain emits on Apple Silicon, where an unsigned Mach-O cannot execute at
all:

```
$ codesign -dvv ~/.local/bin/dexel
Identifier=a.out
CodeDirectory ... flags=0x20002(adhoc,linker-signed)
Signature=adhoc
TeamIdentifier=not set
```

An ad-hoc signature has **no certificate chain and no Team Identifier**, so
there is no developer name for macOS to read. The proof is already on the
owner's machine and cost nothing to obtain: the binary above was *already*
ad-hoc signed while the pane said "unidentified developer". **`codesign -s -`
therefore changes nothing here** — it produces exactly the signature that is
already present. Do not ship a commit claiming otherwise.

Third-party (non-Apple) certificates buy nothing either: Apple DTS is explicit
that macOS treats code signed with a third-party identity "more-or-less like
unsigned code".

Worse — and this is the honest ceiling on §3.1's change — *responsible code*
tracking keys off the Team Identifier, and Apple DTS states that a job whose
executable has no Team Identifier causes `AssociatedBundleIdentifiers` to be
**ignored outright**. So findings 1 and 2 above may buy nothing at all until
there is a real signature. They are cheap, documented/observed-in-the-field, and
correct; they are not a guarantee. Nobody should record "the icon is fixed"
without looking at the pane.

**What the owner would have to buy and do for a real name in that subtitle:**

1. **Apple Developer Program membership — $99 USD/year.** A free Apple ID only
   yields "Apple Development" certificates, which are not distribution
   identities and give no Developer ID. Creating the certificate requires the
   Account Holder role. Code-signing and notarization cost nothing beyond
   membership.
2. Create a **Developer ID Application** certificate, then sign the app bundle
   *and* the sidecar with the **same** identity (Apple DTS: a helper must be
   signed the same way as its parent app for the responsible-code link to be
   tracked):

   ```sh
   security find-identity -v -p codesigning       # confirm the identity is present
   codesign --force --timestamp --options runtime \
            --sign "Developer ID Application: NAME (TEAMID)" \
            /Applications/Dexel.app/Contents/MacOS/Dexel
   codesign --force --timestamp --options runtime \
            --sign "Developer ID Application: NAME (TEAMID)" \
            /Applications/Dexel.app                          # never --deep
   codesign -d -vvv /Applications/Dexel.app                  # Authority + TeamIdentifier must match
   ```
3. **Notarize**, which needs a container (a bare Mach-O cannot be submitted),
   then **staple** — and stapling only works on a bundle/dmg/pkg, never on a
   bare Mach-O. That is an independent argument for shipping the runtime inside
   `Dexel.app` rather than only as `~/.local/bin/dexel`:

   ```sh
   ditto -c -k --keepParent /Applications/Dexel.app Dexel.zip
   xcrun notarytool submit Dexel.zip --apple-id … --team-id … --password … --wait
   xcrun stapler staple /Applications/Dexel.app
   ```

   Whether the Developer ID signature alone moves the subtitle, or notarization
   is also required, was **not** established — Apple's phrasing bundles "signed
   and notarised" together, while every description of the field says the name
   comes from the certificate. Practically moot: notarization is free once
   you're a member and needed for Gatekeeper anyway.
4. Do **not** codesign the plist itself (Apple DTS: "doesn't actually do
   anything useful"), and do **not** use `codesign --deep`.

**Two testing traps, both from Apple DTS.** The LaunchServices database gets
"thoroughly scramble[d]" by repeated development builds, so what a developer
sees in that pane is not what a fresh user sees — the real check is a machine or
VM that has never seen the product. And BTM only refreshes third-party items
during overnight Service Management maintenance; `sudo sfltool resetbtm` plus a
reboot forces a rescan, at the cost of clearing *all* third-party login items.

**One consequence of §3.1's change, stated plainly** (and see §3.1.2 for what
the pane then actually showed). With the plist pointing
into the bundle, the binary that runs at login is the one `Dexel.app` ships, not
the one `dexel update` replaces in `~/.local/bin`. The two can drift in version.
`dexel autostart status` prints the registered program path for exactly this
reason, and `dexel autostart enable --bare` opts back out (macOS-only; a no-op
on Linux and Windows, which have no bundle concept).

### 3.1.2 What the pane actually showed afterwards, and the one half that was fixable

> **Partly superseded by §3.1.3, and deliberately left standing.** Its
> OBSERVATIONS are the record and are still true: the pane read
> "dexel-server", the name is the exec'd file's filename, the icon and the
> subtitle are not fixable at this price point. Two of its CONCLUSIONS are
> not: the daemon is no longer called `Dexel Runtime`, and "Why not simply
> `Dexel`" was answered by moving the main binary out of the way instead of
> by accepting a second word. Read §3.1.3 for what is true now; read this
> section for how it was reasoned, including the rejected alternative that
> is still rejected.

§3.1.1 was written before anyone had looked at the pane again. The owner then
looked (2026-08-23, macOS 26.6, Apple Silicon, after `enable` had moved the
program to `/Applications/Dexel.app/Contents/MacOS/dexel-server` and set
`AssociatedBundleIdentifiers`). **General → Login Items & Extensions → App
Background Activity** read, verbatim:

> `[exec]` **dexel-server** — "Item from unidentified developer."

Three things follow, and they are worth more than the whole prediction above
because they were observed rather than reasoned:

1. **The displayed NAME is the `exec`'d file's filename.** It changed from
   "dexel" to "dexel-server" at exactly the moment `ProgramArguments[0]` moved
   from `~/.local/bin/dexel` to the bundle's `dexel-server` — nothing else
   changed. This is the same conclusion §3.1.1's finding 3 drew from
   nix-darwin's "sh" entries, now confirmed on our own item.
2. **The generic `exec` icon persisted, and the subtitle did not change.** So
   finding 2's field evidence (an executable inside a `.app` gets the app's
   icon) did NOT reproduce here, and finding 1's `AssociatedBundleIdentifiers`
   bought nothing visible. The reason was already written down in §3.1.1 and is
   the reason this is not a bug to chase: without a Developer ID there is no
   Team Identifier, and Apple DTS states a job whose executable has no Team ID
   has its `AssociatedBundleIdentifiers` ignored outright. **The icon and the
   subtitle are therefore closed as NOT FIXABLE at this price point.** Do not
   reopen them without the $99/yr membership and the steps in §3.1.1.
3. **The name, however, is entirely ours** — it is just a filename. That is the
   half that was fixed.

**The fix (as of §3.1.2): the bundled daemon is now named `Dexel Runtime`.**
**Superseded — it is now `Dexel`; see §3.1.3.**

It is Tauri's `bundle.externalBin` entry, so the name lives in
`desktop/src-tauri/tauri.conf.json` (plus the matching `shell:allow-execute`
scope entry in `capabilities/default.json`, `SIDECAR_NAME` in
`desktop/src-tauri/src/lib.rs`, and `BIN_BASE` in `scripts/build-sidecar.sh`,
which writes `binaries/<base>-<target triple>[.exe]`). Renaming it moves
nothing else: the user's CLI stays `~/.local/bin/dexel`, `DEXEL_HOME` and every
path are untouched, and `app/internal/paths` still derives the state dir from
`$DEXEL_HOME`/`$HOME` rather than from `argv[0]`.

This is a deliberate exception to "the product is *Dexel* in prose, `dexel` in
every artifact". It is the one artifact whose *filename* is display text,
because this pane prints it.

**Why not simply `Dexel` — the trap, which is silent.** *(The trap is real
and §3.1.3 confirms it in the vendored bundler source; the CONCLUSION drawn
here — that the main binary must keep the name `dexel` — is what §3.1.3
reverses.)* `Contents/MacOS/dexel`
is already taken by the Tauri shell's own main binary
(`mainBinaryName: "dexel"` — the GUI window, a completely different program
from the Go daemon), and macOS's default APFS volume is case-**in**sensitive.
Proof, on the shipped bundle, before the rename:

```
$ ls /Applications/Dexel.app/Contents/MacOS/Dexel      # note the capital D
Dexel                                                  # ...resolves to `dexel`
```

So `Contents/MacOS/Dexel` and `Contents/MacOS/dexel` are one file. And the
collision does not error: tauri-bundler copies external binaries first
(`macos/app.rs`'s `copy_binaries`) and the main binary second
(`copy_binaries_to_bundle`), so the Rust GUI shell would simply overwrite the
Go daemon, the bundle would ship with the daemon missing, and the LaunchAgent
would open a window at every login instead of starting the runtime. Renaming
the MAIN binary to free up the name was rejected: it inverts Apple's own
convention (`Foo.app/Contents/MacOS/Foo` is the app, not a helper) to relabel
a helper, and `mainBinaryName: "dexel"` is a contract stated deliberately in
`desktop/src-tauri/Cargo.toml`.

Shipping a *second*, differently-named copy via `bundle.macOS.files` was also
rejected: `externalBin` has to stay for the shell's bundled-fallback driver, so
it would mean ~12 MB of duplicate binary, a per-target hard-coded source path
in the config (the source name carries the target triple), and two names to
keep in sync — all to print "Dexel" instead of "Dexel Runtime".

"Runtime" is the product's own word for the thing that starts at login
(`dexel runtime`, `runtime.json`, `runtime.lock`), so the pane now names what
the item actually is. **The space is safe** at every layer that handles the
name, checked in source rather than assumed: `tauri_utils::resources`
composes the on-disk name with `format!("{curr_path}-{target_triple}{ext}")`;
`tauri-plugin-shell`'s `relative_command_path` is a `Path::join` handed to
`std::process::Command`, so there is no shell and no word splitting; NSIS's
generated directive is the quoted `File /a "/oname={{this}}"`; and launchd
reads `ProgramArguments` as an array of strings. Only hand-written shell is at
risk, which is why `scripts/build-sidecar.sh` quotes every expansion and the
CI assertion in `.github/workflows/desktop.yml` uses a quoted bash array
instead of the `for f in $expected` word-split loop it replaced.

**Old bundles keep working.** *(The list has since gained `"Dexel"` at the
front, and — more importantly — a case-exactness rule without which that new
first entry would resolve to the GUI shell on a pre-rename bundle. §3.1.3.)*
`app/internal/autostart`'s probe was
`bundleServerExecutables = ["Dexel Runtime", "dexel-server"]`, newest first,
with the bundle *location* as the outer loop (so `/Applications` still beats
`~/Applications` even when only the older name is present there). A bundle
installed before the rename therefore still resolves instead of silently
degrading to the bare-binary plist. `"dexel"` is deliberately NOT in that list,
for the reason above.

**What still needs a human's eyes, and why this document cannot claim it.**
Everything mechanical was verified on the owner's machine: the bundle contains
both `Contents/MacOS/dexel` and `Contents/MacOS/Dexel Runtime`, the plist names
the new program, `launchctl print` shows it running, and `dexel status` reports
a live runtime on the same state dir. **Whether the pane now reads "Dexel
Runtime" can only be confirmed by looking at it** — and §3.1.1's second testing
trap applies: BTM refreshes third-party items during overnight Service
Management maintenance, so the entry may keep showing the old name (or show
both, briefly) until then. `sudo sfltool resetbtm` plus a reboot forces a
rescan, at the cost of clearing *all* third-party login items. Nobody should
promote "the pane reads Dexel Runtime" to VERIFIED without having read it there.

### 3.1.3 The daemon is now named exactly `Dexel`, and the main binary moved out of its way

The owner asked for the pane to read `Dexel`, full stop — not `Dexel Runtime`.
§3.1.2 had rejected that name for a real reason. This section records how the
name was freed instead, what was rejected on the way, and the invariant that
is now enforced so none of this can regress.

**The hazard, re-verified in the vendored bundler rather than taken on trust**
(`~/.cargo/registry/src/*/tauri-bundler-2.9.4`, the version `tauri-cli 2.11.4`
on this machine builds with):

* `src/bundle/macos/app.rs`'s `bundle_project` calls
  `settings.copy_binaries(&bin_dir)` — the `externalBin` entries — at line 100,
  and `copy_binaries_to_bundle(..)` — the main binary — at line 107. External
  first, main second.
* `src/bundle/linux/debian.rs`'s `generate_data` does it in the **opposite**
  order: the main binary at line 119, `copy_binaries` at line 129. Both land in
  `usr/bin`.
* Both end in `src/utils/fs_utils.rs`'s `copy_file`, whose entire body after
  the existence checks is `fs::create_dir_all(dest_dir)?; fs::copy(from, to)?`.
  `fs::copy` truncates an existing destination and returns `Ok`.

So a case-folding collision does not error, does not warn, and exits 0 — and
*which* of the two programs survives is not even consistent across targets. On
macOS the survivor is the GUI shell. Measured on the shipped `Dexel Runtime`
bundle, before this change:

```
$ shasum -a 256 ./dexel "./Dexel Runtime" ./Dexel
13f888f4f092257d5e7906d61db3f4316837a61dc474fd703bf4997e6c05b002  ./dexel
4a01a90ca949fdb43fcb8612e8cdcf50572d516b3f52d9b93e03d2e0eabdb429  ./Dexel Runtime
13f888f4f092257d5e7906d61db3f4316837a61dc474fd703bf4997e6c05b002  ./Dexel
```

`./Dexel` and `./dexel` are one file — the 11.2 MB Rust shell — while the
12.1 MB Go daemon is a different file with a different hash. Naming the daemon
`Dexel` while the shell owned `dexel` would have shipped a bundle whose
LaunchAgent opened a window at every login.

**What was done: `mainBinaryName` moved from `dexel` to `dexel-desktop`,**
which leaves `Contents/MacOS/Dexel` free for the daemon. The bundle now holds:

| file | program | named by |
|---|---|---|
| `Contents/MacOS/Dexel` | the Go daemon — the login item | `bundle.externalBin` |
| `Contents/MacOS/dexel-desktop` | the Tauri GUI shell | `mainBinaryName` |

**Why that option, and what it costs.** Three were weighed.

1. **Rename the main binary (chosen).** It inverts Apple's
   `Foo.app/Contents/MacOS/Foo` convention, and that is the honest cost. It
   costs nothing user-visible: `create_info_plist` in `macos/app.rs` sets
   `CFBundleDisplayName` and `CFBundleName` from `productName` (still "Dexel")
   and only `CFBundleExecutable` from `mainBinaryName` — and
   `CFBundleExecutable` is what LaunchServices reads, so `open -a`, the Dock,
   the menu bar and Finder are all unaffected. Only a raw process listing
   (Activity Monitor, `ps`) shows `dexel-desktop`.

   Two things turn this from "acceptable" into "actively better than what it
   replaced". First, `dexel-desktop` is **not a new name**: it is already the
   product's word for this exact artifact — `app/cmd_lifecycle.go`'s
   `desktopAppName`, ARCHITECTURE.md Decision 17, §1's "desktop app" row,
   RUN-MODES.md — and `dexel open` looks for precisely that filename on
   `PATH`. Second, the old value was a latent bug on Linux: the `.deb` puts
   the main binary in `/usr/bin` too, so it installed a **`/usr/bin/dexel`
   that is the GUI shell**, shadowing the CLI of the same name — and
   `desktop/src-tauri/src/lib.rs`'s `dexel_on_path()` looks up `dexel` on
   `PATH` to find the CLI to drive, so on a deb-only install the shell could
   have resolved *itself* as its own driver. That job is dormant, so this was
   never shipped; it is fixed rather than merely avoided.

2. **A nested helper bundle, `Contents/Library/LoginItems/Dexel.app`
   (rejected).** This is genuinely Apple's pattern for login-item helpers, and
   it is what the third-party agents surveyed in §3.1.1 finding 2 do. It was
   rejected on cost against benefit, not on principle. Tauri does not build
   nested bundles, so it needs post-build machinery: a hand-written
   `Info.plist`, a staged `.app` skeleton, and a `bundle.macOS.files` entry —
   whose source path would have to carry the target triple, because that is
   how `externalBin` artifacts are named on disk. And `externalBin` has to
   stay regardless, for the shell's bundled-fallback driver
   (`Driver::Bundled`), so the helper would be a **second ~12 MB copy** of the
   same binary with two names to keep in sync. What it would buy is the
   helper's own `Info.plist` and icon — and §3.1.2 finding 2 observed on this
   very machine that without a Team Identifier the icon does not appear and
   `AssociatedBundleIdentifiers` is ignored outright. So the one thing a
   nested bundle adds is the thing already established as not working at this
   price point. Revisit it **together with** the $99/yr Developer ID, never
   before: with a real signature it becomes the right answer.

3. **A second, differently-named copy via `bundle.macOS.files` (rejected
   again, same reasons as §3.1.2).** All of the duplication costs of option 2
   with none of its Apple-pattern benefit.

**The invariant, and the guard.** The rule underneath all of this is:

> **No two executables destined for one flat install directory may collide
> case-insensitively.**

That is `mainBinaryName` plus every `bundle.externalBin` entry — all of which
share `Contents/MacOS/` on macOS, `/usr/bin` in the `.deb`, and the install
directory under NSIS. It was unenforced, it cost real debugging twice, and it
is now checked in two places, deliberately:

* **`mod bundle_layout` in `desktop/src-tauri/src/lib.rs`** — a unit test that
  `include_str!`s this crate's own `tauri.conf.json`, so it cannot pass against
  a stale copy. It asserts the invariant, asserts that `SIDECAR_NAME`,
  `externalBin` and the `shell:allow-execute` ACL entry are still one string,
  and — so the checker is proven rather than trusted — feeds its own comparison
  a known-bad list. Verified to FAIL on the colliding config:
  `no_two_installed_executables_collide_case_insensitively ... FAILED`, with
  `full list: ["dexel", "Dexel"]`.
* **A step in `.github/workflows/desktop.yml`'s `sidecar` job** — the same
  check in bash, because `sidecar` is the only job that runs on every push
  (both bundle jobs are dormant, and the crate's tests need a Rust toolchain
  and a built sidecar). It extracts the names with `sed` rather than jq/python,
  and cross-checks its own extraction against an occurrence count so a
  reformatted config fails loudly instead of silently enumerating nothing.
  Verified to fail on: a colliding `mainBinaryName`; a missing
  `mainBinaryName`; a second colliding `externalBin`; and two array entries
  crammed onto one line.

**The autostart probe needed real code, not just a shorter list.** This is the
subtlest consequence of the whole change and the one worth reading twice.
`app/internal/autostart`'s probe list is now
`bundleServerExecutables = ["Dexel", "Dexel Runtime", "dexel-server"]`, newest
first, so every generation of installed bundle still resolves. Neither
`dexel-desktop` nor the legacy `dexel` is in it. **That is not sufficient**:
`Dexel` and `dexel` fold to the same string, so on a **pre-rename** bundle —
one holding `dexel` (the GUI shell) and `Dexel Runtime` (the daemon) —
`os.Stat(".../MacOS/Dexel")` succeeds and hands back the GUI shell. The first
entry of the list would win and the login item would open a window: exactly the
bug this whole section exists to prevent, reintroduced by the fix for it.

What prevents it is `nameIsCaseExactOnDisk`, now part of the probe's
predicate: it reads the parent directory and requires the candidate's final
component to match a real entry's **stored spelling**, byte for byte. There is
no POSIX call that asks "what is this file actually called?" — `os.ReadDir`'s
entries are the only place the true spelling is visible. So on a pre-rename
bundle `.../MacOS/Dexel` is rejected and the probe falls through to
`Dexel Runtime`; on a current bundle it is spelled exactly that way and is
accepted. `TestLaunchdProgramOnAPreRenameBundle` builds both shapes as real
directories and logs which kind of volume it ran on, since the assertion is
only the real thing on a case-insensitive one. With the case-exactness check
temporarily removed, that test fails with
`launchdProgram = ".../MacOS/Dexel", want ".../MacOS/Dexel Runtime"`.

**Verified on the owner's Mac (2026-08-23, macOS 26.6, Apple Silicon).** The
freshly built bundle contains both executables as two distinct files, the
plist names `.../MacOS/Dexel`, `launchctl print` shows it running, `dexel
status` reports a live runtime on the real state dir, and `dexel open`
attaches to it. The commands and their output are in the landing commit.

**What still needs a human's eyes.** Whether the pane now reads "Dexel" can
only be confirmed by looking at it, and §3.1.1's second testing trap applies:
BTM refreshes third-party items during overnight Service Management
maintenance, so the entry may keep showing "Dexel Runtime" until then.
`sudo sfltool resetbtm` plus a reboot forces a rescan, at the cost of clearing
*all* third-party login items. Nobody should promote "the pane reads Dexel" to
VERIFIED without having read it there. The icon and the "unidentified
developer" subtitle are unchanged and remain closed as not fixable without the
$99/yr Developer ID (§3.1.1).

### 3.2 Linux — **systemd `--user`**, with XDG autostart as the fallback

Chosen over XDG autostart as the primary because systemd gives a real
enable/disable primitive, restart-on-failure, a log destination
(`journalctl --user -u dexel`), and works in a headless/tty session. XDG
autostart only fires inside a graphical session, has no supervision, and no way
to answer "is it enabled?" beyond "does the file exist?".

`~/.config/systemd/user/dexel.service`:

```ini
[Unit]
Description=dexel developer companion runtime
Documentation=https://dexel.jwdlab.com
After=default.target

[Service]
Type=simple
ExecStart=%h/.local/bin/dexel runtime
Restart=on-failure
RestartSec=5
# The runtime writes its own log file; journald gets it too, harmlessly.
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=default.target
```

`enable` = write the unit, `systemctl --user daemon-reload`,
`systemctl --user enable --now dexel.service`.
`disable` = `systemctl --user disable --now dexel.service` then remove the unit
and `daemon-reload`.
`status` = `systemctl --user is-enabled dexel.service`.

**Linger is NOT enabled by default.** `loginctl enable-linger` would keep the
runtime alive with nobody logged in — pointless for a companion that tracks *your
keyboard*, and surprising. A documented `--linger` flag exists for the
headless-workstation case.

**Detection, not assumption:** systemd is used only if `systemctl --user
is-system-running` answers at all (it is fine if it answers `degraded`). If it
does not — no systemd, or no user D-Bus session — fall back to XDG autostart and
record `xdg-autostart`:

`~/.config/autostart/dexel.desktop`:

```ini
[Desktop Entry]
Type=Application
Name=dexel
Comment=dexel developer companion runtime
Exec=/home/USER/.local/bin/dexel start
Terminal=false
X-GNOME-Autostart-enabled=true
```

Note `dexel start`, not `dexel runtime`: the XDG path has no supervisor, so
going through `start` gets the lock check, the log rotation and the readiness
report for free. `disable` must probe **both** mechanisms regardless of what
`config.json` claims — a user who switched distros should not be left with an
orphan.

Unrelated but user-visible: Linux capture needs `input` group membership
(README's platform table). Autostart does not change that, and a runtime started
at login without the group is `HonestyBlind` — `dexel status` must show the
provider honesty so this is diagnosable in one command.

### 3.3 Windows — **HKCU `Run` key**

Chosen over the two alternatives:

* *Startup-folder `.lnk`* — needs a shell link, i.e. COM/`IShellLink`, from Go.
  A whole dependency and a whole class of bug for the same effect.
* *Task Scheduler* — the most capable (delay-after-boot, restart-on-failure, no
  console at all), but it means composing a scheduler XML and shelling out to
  `schtasks`, and it is the mechanism users most often find has been disabled by
  policy.

`HKEY_CURRENT_USER\Software\Microsoft\Windows\CurrentVersion\Run`, value name
`dexel`, data `"%LOCALAPPDATA%\dexel\bin\dexel.exe" start`. One registry write
through `golang.org/x/sys/windows/registry` — no COM, no new module, trivially
reversible, and inspectable in Task Manager's Startup tab (which is where a
Windows user will look).

`start`, not `runtime`, for the same reason as the XDG fallback: the Run key has
no supervisor. The known cost is a brief console flash at login, because
`dexel.exe` is a console-subsystem binary. Deferred fix, named: build a second
`dexelw.exe` with `-ldflags "-H=windowsgui"` and point the Run key at that. Not
worth blocking a first release for a sub-second flash on a tier-2 platform whose
activity provider is blind anyway.

---

## 4. Logging

One file: `<StateDir>/logs/runtime.log`, opened `O_APPEND|O_CREATE` at `0600`.

### 4.1 Who owns stdout/stderr, per supervision mode

This is the table the first version of this section did not have, and its
absence hid a real bug (`docs/plan/BUGS-RESILIENCE.md` R6). "Who owns fd 1/2"
differs per mechanism, and everything about rotation follows from it.

| how the runtime was started | fd 1/2 point at | who writes `runtime.log` | rotated by |
|---|---|---|---|
| `dexel start` (also XDG autostart and the Windows Run key, whose entries run `start`) | the log file, opened by the CLI and inherited | the runtime, through the inherited descriptor | `start` once at spawn, **and the runtime itself while it runs** |
| launchd (§3.1) — `ProgramArguments = [<exe>, runtime]` | the log file, opened by launchd (`StandardOutPath`/`StandardErrorPath`) | launchd's redirection | **the runtime itself** (no `start` ever runs on this path) |
| systemd `--user` (§3.2) — `ExecStart=<exe> runtime` | a journald socket (`StandardOutput=journal`) | **the runtime, which tees** | **the runtime itself**; journald rotates the journal on its own |
| a terminal (`dexel runtime`, `dexel serve`, the legacy shape) | the tty | the runtime tees in `runtime` mode; `serve`/legacy write to the terminal only | n/a for `serve`/legacy |

Two consequences worth stating plainly:

* **The two supervised paths never run `dexel start`.** Anything `start` does on
  the way through — the lock pre-check, the rotation, the readiness report — is
  simply absent for the mechanism most real users' dexel runs under. That is why
  rotation had to move into the runtime and not into another CLI verb.
* **Under systemd the log file used to get nothing at all.** The unit's own
  comment claimed "the runtime writes its own log file; journald gets it too",
  and until the runtime started teeing, that was false: `journalctl --user -u
  dexel` had everything and `dexel logs` was empty forever. The comment is now
  true.

### 4.2 Rotation-lite: 8 MiB, two files, in the runtime

If a write would take the file past **8 MiB**, it is renamed to `runtime.log.1`
(replacing any existing `.1`) and reopened; exactly two files ever exist. No
rotating-writer dependency, no goroutine and no timer — the check is a byte
counter seeded from the file's size at open, tested on each write, so the cost
is an integer compare per line and one `stat` per 8 MiB.

It happens in **two** places, and the difference matters:

* `lifecycle.RotateLog`, once, in `dexel start`, before the child is spawned.
  Unchanged.
* `lifecycle.RotatingWriter`, continuously, in the runtime — attached by
  `app/main.go`'s `attachRuntimeLog` in `runtime` mode only. This is what makes
  the cap true for a runtime that is up for weeks, and true at all on the
  launchd and systemd paths.

`attachRuntimeLog` decides whether to **tee** by asking `os.SameFile` whether
its own stderr already IS the log file — the question the table above answers
per mechanism, asked of the descriptor rather than guessed from the mechanism.
Same file: take the `log` package's output over outright (teeing would write
every line twice). Different file: tee, so the journal/terminal keeps every line
and the file gets one too.

`DEXEL_MAX_LOG_BYTES` overrides the 8 MiB cap for one process (an absent,
unparseable or non-positive value leaves it at 8 MiB). It exists so a test can
watch a real runtime rotate a real log in milliseconds, and for a field
diagnosis on a box with a small partition. It is read only by the runtime;
`start`'s one-shot rotation always uses the 8 MiB constant.

**The limitation, stated rather than hidden.** Rotation moves the `log`
package's output to the new file; it cannot move file descriptors 1 and 2,
because a rename does not move an open descriptor. On the two paths where fd 1/2
ARE the log file (`dexel start`, launchd), anything written *straight* to them —
in practice a Go panic's traceback, and the `DEXEL_LISTENING` handshake line —
lands in whichever file that descriptor was opened on, i.e. in `runtime.log.1`
after a rotation. Mid-run `dup2` of fd 1/2 onto a fresh file is still rejected
for the reasons it always was (platform-specific, and it fights the
launchd/systemd redirections). A panic being one file over is a much smaller
problem than a log that grows without bound for months, which is what the
alternative measured out to: at the pathological ~2 lines/second a flapping
input device can produce, 8 MiB is under half a day.

Escape hatches, unchanged: `dexel logs --truncate` (which truncates rather than
unlinks precisely so a running runtime's descriptor keeps working — the writer
re-`stat`s before rotating so a truncate underneath it cannot destroy the real
`.1`), and `dexel restart`.

`dexel logs` is the interface: `-n N` tail, `-f` follow, `--path` print the
path, `--truncate` empty it. On Linux-with-systemd it also mentions
`journalctl --user -u dexel` in the command's help, since both exist — and
since the tee, both really do have the lines.

---

## 5. Single-instance enforcement

Three layers, in this order, because each catches what the previous cannot:

1. **`flock`/`LockFileEx` on `<StateDir>/runtime.lock`,** held for the process
   lifetime (`golang.org/x/sys`, already an indirect dependency). The OS releases
   it on process death *including SIGKILL*, so there is no stale-lock failure
   mode. Failure message names the pid from `runtime.json`.
2. **The explicit `net.Listen`** that `app/main.go` already does before anything
   else, with its existing `log.Fatalf("listen on %s: %v")`. Catches a *foreign*
   process on a fixed port, which the lock cannot.
3. **`runtime.json` + a live `GET /api/lifecycle/status` round-trip**, used by
   the CLI to decide "already running" *before* spawning. Never trust the pid
   alone: pids are recycled.

The lock is the authority for "am I the runtime"; the HTTP round-trip is the
authority for "is there a runtime to talk to". `dexel serve` (the dev foreground
path) skips layers 1 and 3 by default and logs that it did, so a scratch
instance next to a real one keeps working exactly as it does today.

**Two state directories = two independent instances**, by design: `DEXEL_HOME`
gives every instance its own lock, its own `runtime.json` and its own save. That
is how the test suite and CI avoid touching a developer's real state.

### 5.1 The sticky port

A `runtime` binds an **ephemeral** port by default (`127.0.0.1:0`) so a
background runtime nobody typed a port for never fights another process for
8080. Both supervisors restart it after a crash — systemd `Restart=on-failure`,
launchd `KeepAlive{SuccessfulExit:false}` — and a restart on a *different* port
left every already-open page and window permanently dead, retrying an address
nothing answers on, while a healthy runtime served another port
(`docs/plan/BUGS-RESILIENCE.md` R9).

So the port is **sticky**: `<StateDir>/lastport` records the port a runtime
actually bound, and the next runtime in that state directory tries to bind it
again before falling back to an OS-assigned one. The semantics, exactly:

* **`runtime` mode only, and only for a port of 0.** An `-addr` a human or a
  script typed a port into is never second-guessed.
* **Advisory, never binding.** If the recorded port is unavailable the runtime
  takes an ephemeral one, says so in the log, and overwrites the record with
  what it really got. A sticky port can never stop the runtime from starting,
  and can never be taken from whoever holds it — the bind either succeeds or it
  does not.
* **Written after the bind succeeds**, not on the way out: a crash has no way
  out. The record therefore always names a port a runtime here really answered
  on.
* **It survives a clean exit**, unlike `runtime.json`, which is deleted on every
  clean shutdown. That is the point of a separate file: `dexel restart` and a
  bookmarked browser tab benefit as much as a crash-restart does.
* **It cannot cause a second instance.** Layer 1 (`runtime.lock`) is taken
  before the listener is bound, so a runtime that gets as far as binding is
  already the only runtime in this state directory.
* `DEXEL_HOME` isolation is unaffected: the record lives in the state
  directory, so two state directories keep two independent ports.

Rebinding a port a just-SIGKILLed process held works because Go sets
`SO_REUSEADDR` on TCP listeners on Unix, so a `TIME_WAIT` left by that
runtime's accepted connections does not block the new listener. Where it ever
does not, the fallback covers it.

---

## 6. Replacing the binary in place

| | unix | windows |
|---|---|---|
| replace a running binary | `os.Rename` over it — the running process keeps the old inode | **impossible**; the file is locked |
| the dance | rename new → live path (atomic, same filesystem) | rename live → `dexel.exe.old`; rename new → live; delete `.old` best-effort, retry at next start, else `MoveFileEx(..., MOVEFILE_DELAY_UNTIL_REBOOT)` |
| after the swap | the *running* runtime is still the old code until restarted | same |

Both paths require the download to be staged on the **same filesystem** as
`BinDir` for the rename to be atomic — hence `CacheDir` under `StateDir` is
wrong for the final staging step, and `dexel update` extracts to
`<BinDir>/.dexel.new` specifically. Verify by executing the new file
(`.dexel.new version`) *before* the rename: an archive that unpacks but cannot
run must never become `dexel`.

`dexel update` therefore always finishes with `dexel restart` when a runtime was
running, or nothing at all when it was not.

---

## 7. Per-platform gotchas worth writing down once

* **macOS builds need a macOS host.** `app/internal/activity/provider_darwin.go`
  is cgo (Cocoa/CoreGraphics), and `scripts/build-release.sh` refuses
  `darwin/arm64` off-darwin with an explicit error. A `CGO_ENABLED=0` darwin
  build compiles and ships a **blind** provider — never publish that as
  `darwin-arm64`; omit the key instead (`ARCHITECTURE.md` Decision 19).
* **macOS Gatekeeper.** An unsigned, unnotarized `dexel` downloaded by `curl`
  gets no quarantine attribute (quarantine comes from browsers and Finder), so
  the install-script path runs clean. A `.dmg` a user double-clicks does not —
  that is `dexel-desktop`'s problem and it is deferred with signing.
* **Linux `input` group.** Required for real capture; a runtime without it is
  honestly blind rather than broken. `dexel status` surfaces `providerHonesty`
  so one command explains "why is nothing accruing".
* **Windows capture is new and unverified** (`provider_select_windows.go`, ADR
  0021). It is no longer blind by construction, but it *can* be: the low-level
  hook install is refusable (Group Policy, a secure desktop) and Windows evicts
  a slow hook silently. `dexel status`'s `providerHonesty` is the one command
  that explains "why is nothing accruing" here too, and the download page should
  say "field verification pending" rather than either "blind" or "works".
* **`~/.local/bin` may not be on `PATH`.** The installer detects and offers to
  fix it, per-shell, idempotently (RELEASE_PIPELINE.md §6 step 8). It never edits
  anything outside `$HOME`.
* **Time zones and the day boundary.** The daily rollover
  (`rolloverStatsIfNewDay`) uses local dates. A background runtime that survives
  a suspend/resume across midnight, or a laptop that changes zone mid-flight,
  hits that code far more often than a foreground one ever did. No design change
  proposed — `history_test.go` already covers cross-month/cross-year edges — but
  it is the first place to look if a long-running instance reports a weird day.
* **Suspend/resume.** After a laptop sleeps, `time.Ticker` does not fire for the
  missing hours; the 1 Hz loop simply resumes. Nothing accrues for the sleep
  (correct — nobody was typing) and no seconds are counted into any bucket
  (also correct). Worth a test, not a design change.
