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
| windows/amd64 | tier 2 | **blind** (`provider_select_other.go`) | HKCU Run key | dexel runs and the economy works, but earns nothing from real input |
| windows/arm64 | tier 3 | blind | same | cross-compiled, never run |
| darwin/amd64 | not built | — | — | omitted from the matrix; add if an Intel Mac ever matters |

The Windows honesty note matters for distribution: shipping a Windows binary
whose provider is blind means the store, the UI, and the save all work while
Dev Cash never accrues from real typing. That is a *documented* state, not a
bug — `activity.HonestyBlind` exists precisely so nothing lies about it — but
the download page and `dexel status` must both say so, or the first Windows
user will file "dexel is broken".

---

## 1. Filesystem locations

| | Linux | macOS | Windows |
|---|---|---|---|
| **StateDir** | `$XDG_CONFIG_HOME/dexel`, else `~/.config/dexel` | `~/Library/Application Support/dexel` | `%LOCALAPPDATA%\dexel` |
| state | `<StateDir>/state.db` | same | same |
| config | `<StateDir>/config.json` | same | same |
| discovery | `<StateDir>/runtime.json` (0600) | same | same |
| lock | `<StateDir>/runtime.lock` | same | same |
| logs | `<StateDir>/logs/runtime.log` | same | same |
| update cache | `<StateDir>/cache/` | same | same |
| **BinDir** | `~/.local/bin` | `~/.local/bin` | `%LOCALAPPDATA%\dexel\bin` |
| desktop app | `~/.local/bin/dexel-desktop` or an AppImage | `/Applications/dexel.app` | `%LOCALAPPDATA%\Programs\dexel` |
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

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>              <string>com.jawwadzafar.dexel</string>
  <key>ProgramArguments</key>
  <array>
    <string>/Users/USER/.local/bin/dexel</string>
    <string>runtime</string>
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

* Absolute paths only — launchd has almost no environment and no `PATH`.
* `KeepAlive → SuccessfulExit: false` means "restart if it crashed, do not
  restart after a clean `dexel stop`". A bare `KeepAlive: true` would make
  `dexel stop` un-stoppable, which is a bug, not supervision.
* `ProcessType: Background` opts into background CPU/IO scheduling — correct for
  a 1 Hz ticker.
* launchd owns the log redirection here, so the child's stdio is launchd's
  business and `dexel start` is not involved.

Commands: `launchctl bootstrap gui/$(id -u) <plist>` and
`launchctl bootout gui/$(id -u)/com.jawwadzafar.dexel`, falling back to
`launchctl load -w` / `unload -w` on older macOS. `status` = `launchctl print
gui/$(id -u)/com.jawwadzafar.dexel` exit code, plus the plist's existence.

Not designed here: the first launch will prompt nothing (the macOS provider is
permissionless by ADR 0010 decision 1 — that is why it was chosen), so autostart
needs no consent dialog of its own.

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
Everything the runtime writes through the `log` package (stderr) plus anything a
panic writes lands there, because `dexel start` points the child's stdout and
stderr at it. Under launchd, launchd points them at the same path.

**Rotation-lite, at start only.** Before opening, if the file exceeds **8 MiB**,
rename it to `runtime.log.1` (replacing any existing `.1`); keep exactly two
files. No goroutine, no timer, no mid-run reopen, no dependency.

The accepted limit, stated rather than hidden: a runtime that runs for months
without a restart can exceed the cap, because nothing rotates while it runs.
This is tolerable because the runtime is nearly silent at steady state — reading
`app/main.go`, a normal run logs ~8-10 lines at startup (provider, save load,
config, listen, the two static-source lines) and then **only on failure**
(`save failed`, `ws accept`, provider warnings). The 30s autosave logs nothing
when it succeeds. Escape hatches: `dexel logs --truncate`, and `dexel restart`
rotates on the way through.

Rejected: a rotating-writer library (a dependency for a problem we do not have),
and mid-run `dup2` of fd 2 onto a fresh file (platform-specific, and it fights
the launchd/systemd redirections).

`dexel logs` is the interface: `-n N` tail, `-f` follow, `--path` print the
path, `--truncate` empty it. On Linux-with-systemd, also mention
`journalctl --user -u dexel` in the command's help, since both exist.

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
  that is `dexel-desktop`'s problem and it is deferred with signing
  (`docs/plan/F3-design.md` §6).
* **Linux `input` group.** Required for real capture; a runtime without it is
  honestly blind rather than broken. `dexel status` surfaces `providerHonesty`
  so one command explains "why is nothing accruing".
* **Windows is blind today** (`provider_select_other.go`). Say it on the download
  page and in `dexel status`.
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
