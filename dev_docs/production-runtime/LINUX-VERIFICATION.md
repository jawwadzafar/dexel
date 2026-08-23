# dexel on Linux — a first, actually-executed verification

Nobody had ever run dexel on Linux before this document. `README.md`'s
platform table and `PLATFORM_NOTES.md`'s support-tier table both call Linux
**tier 1**, but every Linux claim in the repo was reasoned from the source
rather than observed: the code was written on a Mac, cross-compiled by CI, and
never started on a Linux kernel by anyone who then wrote down what happened.

This document is the record of doing that. Everything below was run for real
in Docker on 2026-08-23 against the working tree at commit
`18ef1db1cbd3857b13cda8cf6a590f3f450bb4dc` (tree dirty — three other agents
were editing concurrently; see [§9](#9-provenance-and-caveats-about-this-run)).
Nothing is inferred from reading code unless it says so explicitly, and where
a container fundamentally cannot answer a question, [§8](#8-what-a-container-cannot-prove)
says so instead of guessing.

**Headline:** dexel builds, runs, serves the real game, and drives its whole
economy from real `/dev/input` events on Linux — on both architectures. The
problems are not in the game. They are in the three places Linux differs from
macOS: what happens when input capture is *not* available, `autostart`'s
systemd detection, and one sticky-state bug in the provider that makes
recovery from blindness impossible.

---

## 1. Environment

| | |
|---|---|
| Host | macOS 26.6.2, Apple Silicon (`arm64`) |
| Docker | 29.4.1 client + server, server platform `linux/arm64` |
| Native container arch | `linux/arm64` — a real arm64 kernel (`6.12.76-linuxkit`) |
| Emulated container arch | `linux/amd64` — Docker Desktop's x86-64 emulation |
| Build images | `golang:1.27` (reports `go1.27.0 linux/arm64`) |
| Runtime images | `debian:trixie-slim` (Debian GNU/Linux 13, `arm64` and `amd64`) |
| Host Go | `go1.26.5 darwin/arm64` with `GOTOOLCHAIN=auto` (fetches 1.27 for `app/go.mod`'s `go 1.27.0`) |
| Screenshotter | `chrome-headless-shell` from `chromium_headless_shell-1228`, run on the **host** against a published container port |

The repo was mounted **read-only** (`-v <repo>:/src:ro`) into every build
container, with `GOCACHE`/`GOMODCACHE` pointed at scratch volumes, so no
container could write into the working tree. Every `DEXEL_HOME` used on the
host side was a scratch path; the owner's real state directory and the live
`launchctl` job were never touched.

---

## 2. Does it build for Linux?

### 2.1 The triple table is accurate

```
$ scripts/build-sidecar.sh --list
TARGET TRIPLE                GOOS     GOARCH   CGO
x86_64-unknown-linux-gnu     linux    amd64    0
aarch64-unknown-linux-gnu    linux    arm64    0
x86_64-pc-windows-msvc       windows  amd64    0
aarch64-pc-windows-msvc      windows  arm64    0
aarch64-apple-darwin         darwin   arm64    1
x86_64-apple-darwin          darwin   amd64    1

host triple: aarch64-apple-darwin
```

### 2.2 Cross-compiling from macOS (what CI does) — works

```
$ OUT_DIR=<scratch> scripts/build-sidecar.sh \
    aarch64-unknown-linux-gnu x86_64-unknown-linux-gnu
build-sidecar: build aarch64-unknown-linux-gnu  (GOOS=linux GOARCH=arm64 CGO_ENABLED=0, ...)
build-sidecar: build x86_64-unknown-linux-gnu   (GOOS=linux GOARCH=amd64 CGO_ENABLED=0, ...)
```

`file` on the results:

```
ELF 64-bit LSB executable, ARM aarch64, version 1 (SYSV), statically linked, ... stripped
ELF 64-bit LSB executable, x86-64,      version 1 (SYSV), statically linked, ... stripped
```

Statically linked, as `CGO_ENABLED=0` promises. `scripts/build-release.sh
linux/arm64 linux/amd64` also succeeded and produced both `.tar.gz` archives
plus `sha256sums.txt`; §5.6 records installing and running one of them.

### 2.3 Building inside a Linux container — also works, both cgo settings

Inside `golang:1.27` on `linux/arm64`, from the read-only mount:

* `CGO_ENABLED=1 go build .` → success (17,715,065 bytes)
* `CGO_ENABLED=0 go build .` → success (17,626,352 bytes)

### 2.4 **`CGO_ENABLED=0` on Linux is not a degraded build — it is the same build**

This was the question worth answering, because it is genuinely asymmetric with
darwin. The answer, established by reading the build tags and then confirmed by
running both binaries:

* `app/internal/activity/` contains exactly one Linux file,
  `provider_linux.go`, carrying `//go:build linux` and **no** cgo constraint.
  Its own header says "no cgo needed", and there is no second, non-cgo
  fallback definition of `NewLinuxProvider` anywhere — because none is needed.
* `app/provider_select_linux.go` is likewise plain `//go:build linux`.

So on Linux the cgo switch selects nothing. Both the `CGO_ENABLED=0` and
`CGO_ENABLED=1` builds log the same provider at startup —

```
activity provider: linux (/dev/input/event* raw reader)
```

— and the `CGO_ENABLED=0` binary is the one that read real evdev events in
§4.3. **A `CGO_ENABLED=0` Linux build is fully sighted, not blind.**

This *confirms* `scripts/build-sidecar.sh`'s header ("linux/windows use
`CGO_ENABLED=0` by design: their providers are already pure Go") and
`build-release.sh`'s "pure Go — cross-compiles cleanly from any host". It is
the opposite of the darwin situation, where `CGO_ENABLED=0` fails to link.
The pure-Go SQLite driver (`modernc.org/sqlite`) is what makes this possible
at all; a cgo SQLite would have made `CGO_ENABLED=0` impossible and the whole
cross-compile story would collapse.

### 2.5 The whole test suite passes on Linux

Run in `golang:1.27` on `linux/arm64` — the first time this suite has ever
executed on a Linux kernel:

```
go vet ./...      -> clean
ok  github.com/jawwadzafar/dexel/app                    19.201s
ok  github.com/jawwadzafar/dexel/app/internal/activity   1.250s
ok  github.com/jawwadzafar/dexel/app/internal/autostart  0.009s
ok  github.com/jawwadzafar/dexel/app/internal/engine     0.003s
ok  github.com/jawwadzafar/dexel/app/internal/game       0.188s
ok  github.com/jawwadzafar/dexel/app/internal/lifecycle  0.250s
ok  github.com/jawwadzafar/dexel/app/internal/paths      0.012s
ok  github.com/jawwadzafar/dexel/app/internal/store      1.614s
```

Note what this means for the build-tagged files: `paths_other.go`,
`spawn_unix.go`, `lock_unix.go`, `systemd_linux.go` and `xdg_linux.go` are
compiled *and* exercised here, where a macOS `go test` run compiles the darwin
siblings instead.

---

## 3. Does the server serve the real game on Linux?

Yes, and it looks right.

```
$ docker run -d --name dexel-linux-arm64 --platform linux/arm64 \
    -p 18080:18080 -v <scratch>:/host debian:trixie-slim sleep infinity
$ docker exec dexel-linux-arm64 /host/dexel start -addr 0.0.0.0:18080
dexel started (pid 47) — http://127.0.0.1:18080
```

From the **host**:

```
$ curl -s http://127.0.0.1:18080/api/health
{"assetsDir":null,"publicOk":true,"version":"dev","commit":"18ef1db...",
 "source":"embedded","publicSource":"embedded","assetsSource":"embedded"}
$ curl -sI http://127.0.0.1:18080/ | head -1
HTTP/1.1 200 OK
```

`assetsDir: null` + `"source":"embedded"` confirms EMBED-1 works on Linux: the
frontend and every sprite come out of the binary. The container held no
`public/` or `assets/` tree at all — only the single executable on a mount.

### 3.1 Screenshot verdict, in my own words

Both screenshots were taken from the host with
`chrome-headless-shell --headless --disable-gpu --hide-scrollbars
--window-size=660,460 --virtual-time-budget=20000` against the published port,
and I looked at both images myself.

**First launch (`linux-arm64.png`).** This is a finished pixel-art screen, not
a placeholder. The onboarding dialog "A DEXEL MOVED IN" sits centred over the
scene with the copy "It watches you work, keeps you company, and slowly gets
better dressed." Inside it, a real character sprite — a purple-hoodied figure
with both arms raised, rendered in the project's palette with visible dithered
shading — is captioned "Midnight Indigo", beside a "WHAT IS ITS NAME?" field
pre-filled `dexel`, five colour swatches under "STARTER COLOUR", and
`SAY HELLO` / `SKIP` buttons over the hint "ENTER CONFIRMS · ESC SKIPS". The
HUD is present above (coin icon with `0`, `LV 1`, the menu glyph top-right),
and both bottom panels render: `SPRINT: Fix Bug #404` with an empty bar and
`0 / 50 units`, and the terminal panel showing `Working...` with a green status
square over ticker lines `> Waiting on input... > Watching for changes...
> Reading the docs...`.

**Live, with real input flowing (`linux-arm64-coding.png`).** After naming the
dexel and feeding it real evdev keystrokes (§4.3), the full desk scene is
visible with no modal in the way: the character seen from behind in the purple
hoodie, both hands on a keyboard, at a wooden desk; a monitor above showing
scrolling code text (`Compiling companion v0.2`, `func handleRequest(ctx)
error…`, `test result: ok. 41 passed`); a coffee cup at left and a speaker at
right; a brick wall below the desk line; an office chair under the character.
The HUD reads coin `0`, `LV 1`. The sprint panel now reads
`SPRINT: Fix Bug #404` with a visibly filled first segment of the bar and
`2 / 50 units`. The terminal panel reads `Coding` with ticker lines
`> Compiling... > Rebuilding index... > Type-checking module 7...`, and the
dexel's name `Tux` sits bottom-right in gold.

That second image is the honest proof: the mood, the ticker pool, the sprint
bar and the name all changed *because* a Linux kernel delivered input events
to a Go process that parsed them. The frontend, the sprites, the HUD and the
character all render correctly from the embedded copy inside a Linux binary.

The `--virtual-time-budget=20000` caveat is real and I hit it once — see §7.2.

---

## 4. Activity capture: what a Linux user without `input` actually gets

This is the interesting half, because the container is a *harder* case than a
misconfigured desktop: it has no `/dev/input` directory at all.

```
$ docker exec dexel-linux-arm64 ls /dev/input
ls: cannot access '/dev/input': No such file or directory
```

### 4.1 The blind path — ADR 0010 holds, and holds for a long time

With no input devices, `dexel start` succeeds and the runtime logs:

```
activity provider: linux (/dev/input/event* raw reader)
...
activity provider start warning: no readable /dev/input devices (add your user
to the 'input' group, or run with access to input devices): %!w(<nil>)
```

Reading `ws://127.0.0.1:18080/ws` from the host with Node's global
`WebSocket`, over 25+ consecutive `state` messages:

| field | observed |
|---|---|
| `activeState` | `"idle"` on **every** tick — never `"onBreak"`, never `"coding"` |
| `activityLine` | `"Working..."` |
| `stats.today.keystrokes` | `0`, unchanging |
| `stats.today.activeSeconds` | `0`, unchanging |
| `stats.today.mouseActiveSeconds` | `0`, unchanging |
| `devCash` / `sprint.progress` | `0` / `0.0` |
| `paused` | `false` |
| `stats.today.idleSeconds` | increments 1/s |

**Verdict: the engine's honesty rule works exactly as ADR 0010 specifies.**
`Engine.mood` gates `MoodOnBreak` behind `honesty == HonestyGlobal`, and
`LinuxProvider.Honesty()` returns `HonestyBlind` when it opened nothing, so
`onBreak` is structurally unreachable. I watched for it and it never appeared.
The provider's own idle clock is frozen too: `Snapshot()` returns the zero
`Snapshot{}` while blind, so `IdleSeconds` is a hard `0` and there is nothing
for `mood` to over-interpret. No signal is fabricated anywhere.

`stats.today.idleSeconds` *does* keep counting, and that is deliberate, not a
leak: `StatCounters.IdleSeconds`' doc comment defines it as "one second for
every tick whose mood was NOT `MoodCoding`", and the invariant
`ActiveSeconds + IdleSeconds + PausedSeconds == uptime` depends on it. Worth
recording as a nuance, though: on a *blind* Linux box that bucket is
accumulating seconds under the label "observed, and doing nothing" when the
truthful label would be "not observed" — and the counter that exists for "not
observed", `PausedSeconds`, only fires for an explicit `dexel pause`. There is
no third bucket for "the runtime was up, unpaused, and structurally unable to
see". This is a labelling gap, not a fabricated number.

### 4.2 **Does it fail loudly? Only in the log. Everywhere a user looks, it is silent.**

This is the finding that matters most for a Linux release, and it contradicts
the docs.

What the user has available:

| Surface | Says the provider is blind? |
|---|---|
| `<StateDir>/logs/runtime.log` | **Yes** — one `activity provider start warning:` line, at startup only |
| `dexel status` | **No.** It prints `tracking  on` |
| `dexel status --json` | **No** field for it |
| `GET /api/health` | **No** field for it |
| `GET /api/lifecycle/status` | **No** field for it |
| The `state` WebSocket message | **No** field for it |
| The game UI (scene, HUD, `[A]` analytics, `[H]` history) | **No.** Nothing anywhere mentions honesty or blindness |

I checked the last two by grepping `app/frontend/src/` and `app/public/` for
`blind` and `honesty`: **zero matches.** `game.StateMessage` has no honesty
field. So a Linux user who is not in the `input` group sees a game that boots,
renders perfectly, says `tracking on`, and then simply never earns anything —
with the explanation sitting in a log file they have no reason to open.

`dexel status` printing **`tracking  on`** is the sharpest edge here. It is
technically accurate (it reflects `paused == false`) but on a blind box it
reads as a positive assertion that dexel is watching, which is the one thing
it cannot do.

### 4.3 The sighted path works — proven with synthetic evdev events

To test the read path itself rather than only the failure path, I made
`/dev/input/event0` a FIFO and fed it the exact 24-byte `input_event` records
`provider_linux.go` documents (16-byte time64 `timeval`, then `u16 type`,
`u16 code`, `s32 value`), one `EV_KEY` / `KEY_A` / value `1` every 120 ms:

```sh
mkfifo /dev/input/event0
exec 3>/dev/input/event0
printf "\000...\001\000\036\000\001\000\000\000" >&3   # 24 bytes, repeated
```

The result, over the same host-side WebSocket:

```
{"activeState":"coding","activityLine":"Coding","keystrokesToday":54,  "activeSecondsToday":9,  "idleSecondsToday":1}
{"activeState":"coding","activityLine":"Coding","keystrokesToday":135, "activeSecondsToday":22, "idleSecondsToday":1}
{"activeState":"coding","activityLine":"Coding","keystrokesToday":234, "activeSecondsToday":37, "idleSecondsToday":1}
{"sprint":{"index":0,"name":"Fix Bug #404","progress":2.424,"target":50,"unitLabel":"units"},
 "stats":{"keystrokes":303,"mouseActiveSeconds":0,"activeSeconds":48,"idleSeconds":1,
          "appSwitches":0,"pausedSeconds":0}}
```

Confirmed by this:

* the byte layout in `provider_linux.go` matches what the parser needs, and
  `type`/`code`/`value` are read from the right offsets;
* `MoodCoding` requires a real keystroke and gets one — `activeState` flips to
  `coding` and `activityLine` to `"Coding"`;
* `idleSeconds` **froze at 1** the moment input started, and `activeSeconds`
  climbed instead;
* the anti-mash clamp is doing its job: I emitted ~8.3 events/s and the
  counter rose at ~6–7/s, consistent with the 100 ms `MouseSampleInterval`
  coalescing window;
* real Dev Cash accrual happens — `sprint.progress` climbed to `2.424 / 50`
  from genuine input, at the deliberately slow shipped rate;
* `appSwitches` is `0`, exactly as `StatCounters.AppSwitches`' comment
  promises for Linux ("Always 0 on Linux, which never sets ActiveApp (ADR
  0009); shown honestly, no special-casing"). Confirmed on the wire.
* `SET_NAME` round-tripped and `onboarding` flipped `true` → `false`.

**Honest limit of this test:** a FIFO is not an evdev character device. This
proves dexel's *parsing, counting, coalescing and economy* path on Linux, and
that it opens and reads `/dev/input/event*` correctly. It does **not** prove
that a real kernel emits precisely this 24-byte time64 layout on every
supported arch, and it does not exercise the `input`-group permission model
against real device nodes. See §8.

### 4.4 The EACCES path — the message is correct and actionable

Separately, with root-owned mode-`000` stand-ins at `/dev/input/event{0,1,2}`
and the server run as a non-root user in no `input` group, the warning is
exactly what a real misconfigured desktop would get, and it is good:

```
activity provider start warning: no readable /dev/input devices (add your user
to the 'input' group, or run with access to input devices):
/dev/input/event0: open /dev/input/event0: permission denied
/dev/input/event1: open /dev/input/event1: permission denied
/dev/input/event2: open /dev/input/event2: permission denied
```

Per-device, names the remedy. Nothing to fix here except that nobody but a
log-reader will ever see it (§4.2).

---

## 5. The CLI and runtime lifecycle on Linux

Everything in this section was run in a `debian:trixie-slim` container as
`root`, using the container's own default state directory (a throwaway
`/root/.config/dexel`) so the *default* Linux path was the thing under test.

### 5.1 Paths — all confirmed

```
$ dexel status
  state     /root/.config/dexel
  log       /root/.config/dexel/logs/runtime.log

$ XDG_CONFIG_HOME=/tmp/xdgtest dexel status
  state     /tmp/xdgtest/dexel

$ DEXEL_HOME=/tmp/homeoverride XDG_CONFIG_HOME=/tmp/xdgtest dexel status
  state     /tmp/homeoverride
```

`PLATFORM_NOTES.md` §1's Linux column is exactly right: `~/.config/dexel` by
default, `$XDG_CONFIG_HOME/dexel` when set, `DEXEL_HOME` overriding both. The
directory came up `0700`, `runtime.json` `0600`, `runtime.lock` `0600`.

### 5.2 `start` / `status` / `logs` / `stop`

```
$ dexel start -addr 0.0.0.0:18080
dexel started (pid 47) — http://127.0.0.1:18080

$ dexel status
dexel is running
  tracking  on
  pid       47
  url       http://127.0.0.1:18080
  version   dev (commit 18ef1db...)
  uptime    4s (since 2026-08-23T15:41:00Z)
  state     /root/.config/dexel
  log       /root/.config/dexel/logs/runtime.log
```

`--json` returns the documented shape (`running`, `pid`, `port`, `url`,
`version`, `commit`, `startedAt`, `uptimeSeconds`, `stateDir`, `logPath`).
`dexel logs -n N` and `dexel logs --path` both work. The unix `Setsid` detach
in `spawn_unix.go` works: the parent exits immediately, the child survives,
and `runtime.json`/`runtime.lock` discovery finds it from a fresh process.

One correct detail worth noting: `-addr 0.0.0.0:18080` binds `[::]:18080` and
the log/handshake honestly say `http://[::]:18080`, while `runtime.json`'s
`url` is rewritten to `http://127.0.0.1:18080` — `reachableHost`'s documented
behaviour, observed working.

`dexel stop` works. In a container whose PID 1 reaps (`docker run --init`):

```
$ dexel stop
stopping dexel (pid 20) via the lifecycle endpoint...
dexel stopped                                            # exit 0, first try
```

In a container whose PID 1 is a bare `sleep infinity`, the *same* command
walks the entire escalation ladder and then reports a failure that did not
happen — see §7.4. Both behaviours are container artefacts of PID 1; §7.4
explains why and why a real Linux desktop is not affected.

### 5.3 `pause` / `resume` — the honest-signal work landed well

```
$ dexel pause
dexel PAUSED (pid 4185) — tracking is off; nothing is observed or counted
while paused. `dexel resume` resumes.

$ dexel status | head -2
dexel is running
  tracking  PAUSED — nothing is being observed or counted (`dexel resume` to resume)
```

and in the log:

```
PAUSED: tracking is off — the activity provider is stopped and nothing is
being observed or counted (paused time is recorded as its own stat, never as idle)
RESUMED: tracking is on — the activity provider is running again and the engine
started from a clean slate (no pre-pause keystroke or focus run carried over)
```

Against a not-running runtime, both correctly refuse with exit 1 ("not
running — there is nothing to pause"). `dexel restart` against a stopped
runtime prints `dexel is not running` and then starts one, exit 0.

### 5.4 `open` fails cleanly

```
$ dexel open
dexel is already running (pid 4185) — http://127.0.0.1:45293
opening http://127.0.0.1:45293
dexel: xdg-open: exec: "xdg-open": executable file not found in $PATH
dexel: open this URL yourself: http://127.0.0.1:45293
```

Exit 1, names the missing tool, and hands the user the URL. That is the right
shape for a headless or minimal Linux box.

### 5.5 `autostart` — **this is where Linux is broken**

There are two cases and they behave very differently.

**Case A — no `systemctl` on PATH at all.** Correct, and matches
`PLATFORM_NOTES.md` §3.2 exactly:

```
$ dexel autostart status
dexel: autostart is OFF
  no autostart mechanism found (checked systemd --user and xdg-autostart)

$ dexel autostart enable
dexel: autostart ENABLED via xdg-autostart (/host/dexel)

$ dexel autostart status
dexel: autostart is ON via xdg-autostart (active=true)
  xdg-autostart: ~/.config/autostart/dexel.desktop present

$ cat ~/.config/autostart/dexel.desktop
[Desktop Entry]
Type=Application
Name=dexel
Comment=dexel developer companion runtime
Exec=/host/dexel start
Terminal=false
X-GNOME-Autostart-enabled=true

$ cat ~/.config/dexel/config.json
{ "name": "", "sessionNames": null, "autostart": "xdg-autostart" }
```

`disable` removes the file, resets `config.json`'s `autostart` to `""`, and is
idempotent (second call still exit 0). Byte-for-byte the unit content
`PLATFORM_NOTES.md` §3.2 specifies, `Exec=… start` rather than `… runtime` as
documented, mechanism recorded in `config.json` as designed.

**Case B — `systemctl` exists but there is no user D-Bus session.** This is
the broken one, and it is the common case on a headless box, in a container,
in a chroot, over `ssh` without a login session, and in WSL without systemd's
user manager.

The detection contract in `systemd_linux.go` is "non-empty stdout means
systemd answered", justified in its own doc comment by a claim about what
was measured on the developer's box:

> This is deliberately NOT "exit code == 0" … `is-system-running` … ALSO
> exits non-zero — with EMPTY stdout — when there is no user D-Bus session at
> all (verified on this box: unset `DBUS_SESSION_BUS_ADDRESS`/`XDG_RUNTIME_DIR`
> yields exit 1, empty stdout, an error on stderr). Empty stdout is therefore
> the one reliable signal for "systemd is not usable here"

On **systemd 257** (257.13-1~deb13u1, Debian 13) that is not what happens:

```
$ systemctl --user is-system-running   # no DBUS_SESSION_BUS_ADDRESS, no XDG_RUNTIME_DIR
offline
$ echo $?
1
$ systemctl --user is-system-running 2>/dev/null | od -c
0000000   o   f   f   l   i   n   e  \n
```

Stdout is **`offline\n`** — seven bytes, not empty. `systemdUsableFromOutput`
therefore returns `true`, dexel commits to the systemd branch, and the
documented XDG fallback is never reached. Observed consequences, in order:

```
$ dexel autostart enable
dexel: autostart enable: systemctl [--user daemon-reload]: exit status 1
(Failed to connect to user scope bus via local transport:
$DBUS_SESSION_BUS_ADDRESS and $XDG_RUNTIME_DIR not defined ...)
$ echo $?
1

$ ls ~/.config/systemd/user/
dexel.service                       # <-- orphan left behind by the failed enable

$ dexel autostart status
dexel: autostart is ON via systemd-user (active=false)
  systemd --user: disabled (/root/.config/systemd/user/dexel.service)
  (config.json records "", which disagrees with what the OS reports -- the OS
   is authoritative)
```

Four distinct problems in that transcript:

1. **`enable` fails outright** where the documented design says it should fall
   back to XDG autostart. On this box autostart is simply unavailable via the
   CLI, even though the mechanism that *would* work is implemented and sitting
   right there in `xdg_linux.go`.
2. **`enable` leaves an orphan unit file.** `enableSystemd` writes the unit
   *before* `daemon-reload`, and nothing unwinds the write when the reload
   fails.
3. **`autostart status` then reports `ON`** for that orphan, because
   `statusSystemd` treats "the file exists" as ON. That is precisely the weak
   inference `PLATFORM_NOTES.md` §3.2 rejected XDG autostart for ("no way to
   answer 'is it enabled?' beyond 'does the file exist?'"). Its parenthetical
   is doubly wrong here: it calls the file's presence "what the OS reports"
   and "authoritative", when the OS in fact reported `disabled`.
4. **`disable` also exits 1** — though it does at least remove the file first,
   so it self-heals the orphan while still reporting failure.

The `is-system-running` interpretation is the root cause and the fix belongs
there: `offline` (and `unknown`) are answers that mean "not usable", not
"usable". Treating the *set of known-good answers* (`running`, `degraded`,
`initializing`, `starting`, `maintenance`, `stopping`) as the allow-list —
rather than "any non-empty string" — restores the documented fallback. The
existing unit test for `systemdUsableFromOutput` cannot catch this, because
the value it was written against is not the value modern systemd prints.

### 5.6 The release tarball path works end to end

The most realistic "what a Linux user does" test:

```
$ tar xzf dexel-v0.0.0-linuxverify-linux-arm64.tar.gz
$ ls dexel-v0.0.0-linuxverify-linux-arm64/
LICENSE  NOTICE  README.md  THIRD-PARTY-LICENSES.md  dexel
$ cp .../dexel ~/.local/bin/dexel && export PATH=$HOME/.local/bin:$PATH
$ dexel version
dexel v0.0.0-linuxverify (commit 18ef1db...-dirty)
$ dexel start -addr 0.0.0.0:18080 && dexel status
dexel started (pid 73) — http://127.0.0.1:18080
dexel is running ...
$ dexel stop
dexel stopped
```

The archive layout matches `RELEASE_PIPELINE.md`, the version stamp reaches
`dexel version` / `dexel status` / `/api/health`, and `~/.local/bin` (the
documented `BinDir`) works.

---

## 6. Architecture: arm64 and amd64

**`linux/arm64`, native.** Everything above. Real arm64 kernel, no emulation.
`PLATFORM_NOTES.md`'s "linux/arm64 … cross-compiled, untested on hardware" can
now be softened: it has been run on an arm64 kernel, built both natively and
cross, with the full test suite green and the game rendering. It has still
never run on arm64 *bare metal with a real keyboard* — see §8.

**`linux/amd64`, emulated.** The macOS-cross-built amd64 binary runs:

```
$ docker run --rm --platform linux/amd64 ... debian:trixie-slim
$ uname -m
x86_64
$ /host/dexel-amd64 version
dexel 18ef1db (commit 18ef1db...)
$ /host/dexel-amd64 start -addr 0.0.0.0:18090
dexel started (pid 19) — http://127.0.0.1:18090
$ /host/dexel-amd64 status
dexel is running / tracking on / pid 19 / url http://127.0.0.1:18090 ...
```

`/api/health` returns `publicOk: true`, `"source":"embedded"`; the WebSocket
serves `state` messages; `SET_NAME` round-trips; and the screenshot
(`linux-amd64-2.png`) shows the full desk scene — character in the purple
hoodie at the keyboard, a dark idle monitor above, coffee cup, speaker, brick
wall, HUD, `SPRINT: Fix Bug #404 — 0 / 50 units`, terminal panel reading
`Working...` with idle ticker lines, and the name `Penguin` bottom-right. Idle,
correctly, because that container also has no `/dev/input`.

**Emulation caveat, stated plainly:** this says the amd64 binary is a valid
x86-64 ELF that starts, serves, and renders. It says nothing about timing,
and nothing about performance — see §7.2 for the one place emulation latency
visibly changed a result. Anything timing-sensitive on amd64 must be measured
on real x86-64 hardware.

---

## 7. Code problems found (reported, not fixed)

Per the constraints of this pass, nothing here was changed. Four items, worst
first.

### 7.1 `LinuxProvider` never un-blinds — a blind provider stays blind for the life of the process

`app/internal/activity/provider_linux.go`, `Start()`. On total failure it sets
`p.blind = true`. On the success path it sets `p.devices`, `p.stopCh` and
spawns readers — **but never sets `p.blind = false`.** `blind` is only ever
written `true`.

Consequence: once a `LinuxProvider` has gone blind, a later successful
`Start()` cannot restore it. `Honesty()` keeps returning `HonestyBlind` and
`Snapshot()` keeps returning the zero `Snapshot{}` — while the reader
goroutines are running and dutifully incrementing `p.keystrokeCount` into a
value nothing will ever read.

Observed, not inferred. A runtime started with no `/dev/input` at all, then
given a readable device, then `dexel pause` + `dexel resume` (the documented
resume seam, which calls `provider.Start()` again):

```
15:55:09  activity provider start warning: no readable /dev/input devices ... # blind
15:55:36  PAUSED: tracking is off ...
15:55:37  RESUMED: tracking is on — the activity provider is running again ...
          # note: NO second warning -> the second Start() SUCCEEDED
```

and for the following 12 seconds, with events actively flowing into the
device:

```
{"activeState":"idle","keystrokesToday":0,"activeSecondsToday":0}
```

Zero. `grep -c "provider start warning"` over the whole log returns **1** — so
`Start()` genuinely returned `nil` the second time and opened the device, and
the provider still reported nothing at all.

Why this matters on a real Linux desktop: it is exactly the recovery a user
would attempt. They start dexel, notice nothing is accruing, run
`sudo usermod -aG input $USER`, get a fresh session, `dexel resume` (or plug
their keyboard back in and resume) — and dexel silently stays dead, with
`RESUMED: tracking is on` on stdout claiming otherwise. It also means the
honest-failure story inverts: the same `blind` flag that correctly prevents a
lie about `onBreak` now causes a lie in the other direction.

Two related, smaller facts from the same function:

* **Devices are globbed exactly once, in `Start()`.** A device attached later
  is never picked up. `pause`/`resume` is the only re-glob, and per the above
  it does not help once blind.
* **`readLoop` exiting does not set `blind`.** If every device disappears
  (unplugged), the readers all return, `Honesty()` stays `HonestyGlobal`, and
  `lastAnyInput` stops advancing — so `IdleSeconds` grows without bound and
  `mood` becomes eligible to claim `onBreak` from a provider that can no
  longer see anything. I did not construct this case; it follows from the code
  and is worth a test.

`provider_linux_test.go` contains one test
(`TestLinuxProviderCoalescesFloodedInputToSampleInterval`) and no coverage of
`blind` at all.

### 7.2 A blocking `open()` on a `/dev/input/event*` match hangs startup after the port is already bound

`Start()` opens each glob match with `os.OpenFile(path, os.O_RDONLY, 0)` — no
`O_NONBLOCK`. If any match blocks on open, `Start()` blocks, and because
`startProvider()` runs *after* `net.Listen` and the `DEXEL_LISTENING`
handshake but *before* `httpSrv.Serve` and `signal.Notify`, the process ends
up in a genuinely bad state.

Reproduced with a FIFO at `/dev/input/event0` and no writer:

```
DEXEL_LISTENING http://127.0.0.1:18110        # port bound, handshake printed
frontend: serving / from the copy embedded in this binary
assets: serving /assets/ from the copy embedded in this binary
dexel dev (commit ...) starting
activity provider: linux (/dev/input/event* raw reader)
wrote default config to /tmp/t6/config.json
no save found: starting fresh
first launch: no save and no name — onboarding
                                              # <-- and nothing more, ever
```

No `dexel listening on …` line, so `Serve` never started; the process sits in
`S` state forever. `SIGTERM` then killed it with **exit 143** and no
`shutting down: saving state...` — because `signal.Notify` is registered after
the blocking call, so nothing saved.

Control run, identical except the FIFO removed:

```
... activity provider start warning: no readable /dev/input devices ...
... dexel listening on http://127.0.0.1:18111 (frontend: embedded, assets: embedded)
... shutting down: saving state...
$ ls /tmp/t7 ; echo exit=$?
config.json  state.db
exit=0
```

**Honest scoping:** I induced this with a FIFO, and I have no evidence that a
real evdev character device ever blocks on open — normally it does not. So
this is a robustness gap rather than a bug a typical user will hit. But the
failure mode is severe and silent (the port is open and the readiness
handshake has already been printed, so a parent process — the Tauri shell
reads exactly that line — believes the server is up), and the two fixes are
both cheap and independently correct: open input devices with `O_NONBLOCK`
(the conventional way to open evdev anyway), and start the provider *after*
the HTTP server and signal handler are live.

### 7.3 `%!w(<nil>)` in the one message a Linux user most needs

The warning printed on *every* run in a container, and on any system with no
`/dev/input/event*` nodes at all:

```
activity provider start warning: no readable /dev/input devices (add your user
to the 'input' group, or run with access to input devices): %!w(<nil>)
```

`Start()` builds that with `fmt.Errorf("...: %w", errors.Join(openErrs...))`.
When `filepath.Glob` matches nothing, `openErrs` is empty, `errors.Join()`
returns `nil`, and `%w` against a nil error formats as `%!w(<nil>)`. Cosmetic,
but it lands in the single most diagnostic line dexel emits on Linux, and it
reads like a crash. The EACCES path (§4.4) is unaffected and formats
correctly. Fix is a branch on `len(openErrs) == 0`.

### 7.4 `processAlive` counts a zombie as alive (container-scoped)

`app/spawn_unix.go`'s `processAlive` uses `p.Signal(syscall.Signal(0))`, and
on Linux `kill -0` succeeds against a **zombie**. Because `dexel start`
`exec`s a detached child that is reparented to PID 1, any PID 1 that does not
reap leaves a zombie behind, and `dexel stop` then walks its whole escalation
ladder against a process that is already dead:

```
$ dexel stop
stopping dexel (pid 47) via the lifecycle endpoint...
stopping dexel (pid 47) via signal...
dexel: pid 47 did not exit within 5s of the lifecycle endpoint accepting the
stop request — falling back to a signal
dexel: pid 47 did not exit within 5s — escalating to a HARD KILL.
dexel: up to the last 30 seconds of progress since the previous autosave may be lost.
dexel: pid 47 is STILL alive after a hard kill — something is very wrong;
investigate by hand
$ echo $?
1
```

That message is alarming and false. The runtime had in fact shut down
gracefully — the log's last line is `shutting down: saving state...` and
`state.db` was written — and `dexel status` immediately afterwards correctly
says `dexel is not running`.

Proven to be the reaper, not dexel:

```
$ cat /proc/47/stat | awk '{print $3}'
Z                                    # zombie; parent is PID 1 = `sleep infinity`
```

and with a reaping PID 1 (`docker run --init`, PID 1 = `docker-init`) the same
sequence is clean on the first attempt: `dexel stopped`, exit 0, no zombies.

**On a real Linux desktop this does not happen**, because PID 1 is systemd and
systemd reaps. It is worth writing down anyway: it will bite anyone running
dexel in a container without `--init`, it costs ~15 s and a false alarm, and a
`/proc/<pid>/stat` state check (or `WIFEXITED`-style handling) would make the
probe correct everywhere for very little code.

### 7.5 Not a bug: sidecar base name changed mid-session

`scripts/build-sidecar.sh`'s `BIN_BASE` was `"Dexel Runtime"` when I ran it at
19:39 and `"Dexel"` when I re-ran it at 19:51 — another agent's edit landing
between the two. Both runs produced correct, working Linux binaries; only the
filenames differ, and the tree is self-consistent (`tauri.conf.json`
`externalBin: ["binaries/Dexel"]`, `desktop.yml` `BASE="Dexel"`) as of the
later run. Recorded only so the two filenames in this document are not
mistaken for a mismatch.

---

## 8. What a container cannot prove

Everything in this section is **still unverified on Linux** and no claim here
should be promoted without a real machine.

1. **Real `/dev/input` capture.** §4.3 proved the parse/count/economy path
   against a FIFO carrying hand-written `input_event` records. It did not read
   one byte from a kernel input driver. Unproven: that a real keyboard's
   `EV_KEY` codes fall under `keyCodeCeiling` as assumed, that a real mouse's
   `EV_REL` stream produces sensible `MouseActive`, that the 24-byte time64
   layout holds on both arches' real kernels, that reading N device nodes
   concurrently behaves, and that unplugging a device mid-run does what §7.1's
   last bullet predicts.
2. **The `input` group.** §4.4 simulated EACCES with mode-`000` regular files.
   Nobody has run `sudo usermod -aG input "$USER"`, logged out, logged back
   in, and watched dexel go from blind to sighted. That is the single most
   important user-facing instruction in `README.md`'s Linux row and it remains
   untested end to end.
3. **A real `systemd --user` session.** §5.5 proved the *failure* branch of
   `systemdUsable()` and found it misdetects. The happy path — write the unit,
   `daemon-reload`, `enable --now`, survive a logout/login, appear in
   `journalctl --user -u dexel`, and be removed cleanly by `disable` — needs a
   real login session with `DBUS_SESSION_BUS_ADDRESS` and `XDG_RUNTIME_DIR`.
   `loginctl enable-linger` and the documented `--linger` flag are likewise
   untested.
4. **XDG autostart actually firing.** §5.5 Case A proved the `.desktop` file
   is written correctly. Whether a GNOME/KDE session actually launches
   `dexel start` from it at login is unproven — there is no graphical session
   here.
5. **A desktop at all.** No X11, no Wayland, no compositor, no browser. The
   screenshots came from a headless Chromium on the macOS host talking to a
   published port. `dexel open` → `xdg-open` → a real browser window is
   untested (§5.4 only proved the clean failure when `xdg-open` is absent).
6. **The Tauri desktop app.** See §10 — not attempted.
7. **Suspend/resume and the midnight rollover** on a real Linux box.
   `PLATFORM_NOTES.md` §7 flags both as "first place to look"; a container
   that never sleeps cannot exercise either.
8. **amd64 timing/performance.** Emulation only (§6).

---

## 9. Provenance and caveats about this run

* Commit `18ef1db1cbd3857b13cda8cf6a590f3f450bb4dc`, working tree **dirty and
  moving** — three other agents were editing throughout. Files seen modified
  at one point or another during this pass:
  `.github/workflows/desktop.yml`, `app/internal/activity/sanitize*.go`,
  `app/internal/autostart/{autostart,launchd_darwin}*.go`,
  `app/internal/game/activity_line*.go`, `app/internal/game/game_test.go`,
  `desktop/README.md`, `desktop/src-tauri/*`, `scripts/build-sidecar.sh`,
  `dev_docs/production-runtime/PLATFORM_NOTES.md` (some of those were
  committed by their owners mid-run). **None** of them touch
  `provider_linux.go`, `provider_select_linux.go`, `internal/paths`,
  `internal/lifecycle`, `spawn_unix.go`, `systemd_linux.go` or `xdg_linux.go`
  — the files every finding above is about. Every quotation in §11 was
  re-checked against the on-disk text after the last of those edits landed.
  Version strings in the transcripts vary (`dev`, `18ef1db`,
  `18ef1db-dirty`, `v0.0.0-linuxverify`) because different binaries were built
  at different moments by different scripts; none of the differences are
  behavioural.
* Ports used: `18080` (arm64, published), `18090` (amd64, published), and
  `18099`/`18101`/`18102`/`18110`/`18111`/ephemeral (in-container only).
* Containers: `dexel-linux-arm64` and `dexel-amd64` (`debian:trixie-slim`,
  bare `sleep infinity` as PID 1), `dexel-init` (same image, `--init`), plus
  throwaway `golang:1.27` build containers.
* Screenshots (not committed; regenerate by repeating §3):
  `linux-arm64.png`, `linux-arm64-coding.png`, `linux-amd64.png` (the
  `CONNECTING…` artefact of §7.2's tool caveat), `linux-amd64-2.png`.
* No git state was changed and no file in the tree was modified except this
  one.

---

## 10. The Tauri desktop app on Linux — deliberately not attempted

`desktop/README.md` already states it: "Linux / Windows bundles — **never
built** — still blocked on runners". That is still true, and a full
`cargo tauri build` in a container was not cheap enough to justify against the
"do not attempt unless genuinely cheap" instruction. What was worth doing was
the cheap half: establishing that the *prerequisites* are real and current.

**Verified.** Tauri's exact documented Linux dependency list — the same list
`.github/workflows/desktop.yml`'s `Install Tauri Linux deps` step installs —
resolves and installs cleanly on Debian 13 (trixie) `arm64`:

```
apt-get install -y libwebkit2gtk-4.1-dev build-essential curl wget file \
  libxdo-dev libssl-dev libayatana-appindicator3-dev librsvg2-dev pkg-config
# -> exit 0
pkg-config --modversion webkit2gtk-4.1
# -> 2.52.5
```

So the package names in that workflow step are not stale, and the step's own
`pkg-config --exists webkit2gtk-4.1` guard will behave.

**Still unverified, and what it would take.** The `desktop-linux` job in
`.github/workflows/desktop.yml` (AppImage + `.deb`) is **dormant by design**
and needs both halves of the documented activation:

1. a self-hosted runner registered with the label `desktop-linux` — the
   existing `darkmirror` runner does not have Tauri's GTK/WebKit deps and
   installing them there needs root, which is exactly why the label is
   separate; and
2. the repository variable that ungates it:
   `gh variable set DESKTOP_LINUX_RUNNER --body true`.

The workflow header explains why both are required: a `runs-on` naming a label
no runner has does not fail, it queues forever, so the `if:` gate on the
variable is what makes "dormant" actually mean dormant. Once ungated the job
already does the right things — installs Rust, installs the deps above,
rebuilds `app/frontend` and asserts the committed `app/public/js/dexel.js` did
not drift, builds the host sidecar, then `cargo tauri build --bundles
appimage,deb` (explicitly excluding `rpm`, which would need `rpmbuild`).

Wholly unproven beyond that, and only a real Linux desktop can settle it:
whether the WebKitGTK webview renders the game correctly (a different engine
from macOS's WKWebView), whether the sidecar-launch handshake works from the
Linux shell, whether the window-is-a-view lifecycle holds, and whether the
AppImage and `.deb` install and run on a distro other than the one that built
them. Signed installers remain out of scope (`docs/plan/F3-design.md` §6).

---

## 11. Existing claims: confirmed, contradicted, unresolved

### Confirmed

| Claim | Source | Evidence |
|---|---|---|
| The server runs on every platform Go supports | `README.md` platform table | §2, §3 |
| Linux reads raw `/dev/input/event*` | `README.md`; `PLATFORM_NOTES.md` tier table | §4.3, §4.4 |
| Linux/Windows build `CGO_ENABLED=0` because their providers are pure Go | `build-sidecar.sh` header; `build-release.sh` header | §2.3, §2.4 |
| `GOOS=linux GOARCH=… go build` cross-compiles from macOS | `README.md` Cross-compiling | §2.2 |
| Linux `StateDir` is `$XDG_CONFIG_HOME/dexel`, else `~/.config/dexel` | `PLATFORM_NOTES.md` §1 | §5.1 |
| `DEXEL_HOME` overrides `StateDir` on every platform | `PLATFORM_NOTES.md` §1 | §5.1 |
| `runtime.json` is `0600`; lock + discovery under `StateDir` | `PLATFORM_NOTES.md` §1, §5 | §5.1, §5.2 |
| `Setsid` detach: parent exits, child survives | `PLATFORM_NOTES.md` §2 | §5.2 |
| `stop` = lifecycle endpoint → SIGTERM → SIGKILL after a grace | `PLATFORM_NOTES.md` §2 | §5.2, §7.4 |
| A blind provider can never produce `OnBreak` | ADR 0010; `engine.go`'s `mood` | §4.1 |
| A blind provider's `Snapshot()` is all-zero — the idle clock freezes | `provider_linux.go` | §4.1 |
| `AppSwitches` is always 0 on Linux, shown honestly | `game.go` `StatCounters` | §4.3 |
| The runtime still starts without input access | `README.md`; `PLATFORM_NOTES.md` §7 | §4.1 |
| systemd primary, XDG autostart fallback, mechanism recorded in `config.json` | `PLATFORM_NOTES.md` §3.2 | §5.5 Case A |
| XDG entry uses `Exec=… start`, not `… runtime` | `PLATFORM_NOTES.md` §3.2 | §5.5 Case A |
| `disable` probes both mechanisms and is idempotent | `PLATFORM_NOTES.md` §3.2 | §5.5 |
| Embedded frontend + assets; `/api/health` reports the resolution | `README.md` Troubleshooting; EMBED-1 | §3 |
| The Linux sidecar prints `DEXEL_LISTENING http://…` first from an empty dir | `desktop/README.md` | §7.2 control run |
| SIGTERM logs `shutting down: saving state...` and exits 0 | `desktop/README.md` | §7.2 control run |
| `~/.local/bin` is the Linux `BinDir` | `PLATFORM_NOTES.md` §1 | §5.6 |
| Tauri's Linux dep list is installable and current | `desktop.yml` | §10 |

### Contradicted

| Claim | Source | What actually happens |
|---|---|---|
| "activity only counts while the browser tab itself has focus" (without the `input` group) | `README.md`, Linux paragraph | **There is no in-tab fallback of any kind.** `app/frontend/src/` sends only `BUY_ITEM`, `BUY_TINT`, `EQUIP_ITEM`, `STORE_OPEN`/`CLOSE`, `SET_NAME`, `SESSION_START`/`STOP`, `PAUSE`, `RESUME` — no keystroke or mouse reporting — and a blind `LinuxProvider` returns the zero `Snapshot`. Nothing counts, ever, focused or not. Observed: `keystrokes` and `activeSeconds` pinned at 0 across 25+ ticks (§4.1). |
| "`dexel status` surfaces `providerHonesty`" / "must show the provider honesty so this is diagnosable in one command" | `PLATFORM_NOTES.md` §3.2 and §7 | **`providerHonesty` does not exist anywhere in the codebase.** Not in `dexel status`, not in `--json`, not in `RuntimeStatus`, not in `/api/health`, not in `/api/lifecycle/status`, not on the WebSocket. It was designed in `ARCHITECTURE.md` Decision 5 and ADR 0018; `paused` shipped from that list and `providerHonesty` did not. `dexel status` prints `tracking on` on a blind box (§4.2). |
| "the honesty rules … freeze rather than guess at idle time" | `README.md`, Linux paragraph | Half true and worth restating precisely. The **provider's** idle clock is frozen (hard `0`) and never reaches `OnBreak`. The **analytics** `stats.today.idleSeconds` keeps incrementing 1/s, by design (§4.1). The sentence as written implies nothing accumulates. |
| `systemctl --user is-system-running` yields "exit 1, **empty stdout**" when there is no user D-Bus session, so non-empty stdout reliably means "usable" | `systemd_linux.go`'s `systemdUsableFromOutput` doc comment (the measured claim) | On systemd 257 with no user bus it prints **`offline\n`** to stdout, exit 1. Detection therefore returns "usable", the XDG fallback never runs, `enable` fails, and an orphan unit is left behind that `status` then reports as `ON` (§5.5 Case B). |
| "systemd is used only if `systemctl --user is-system-running` **answers at all** (it is fine if it answers `degraded`)" | `PLATFORM_NOTES.md` §3.2 | Correct as written but load-bearing in the wrong direction: `offline` *is* an answer, and it means the opposite of usable. The rule needs to name the usable answers rather than accept any answer. |
| The Linux sidecar "writes `state.json`" on SIGTERM | `desktop/README.md`, "What *is* verified" | It writes **`state.db`** (post-DB-1). Confirmed: `ls` after a graceful SIGTERM shows `config.json` and `state.db`, no `state.json` (§7.2 control run). Stale doc, not a bug. |

### Softened rather than contradicted

* `PLATFORM_NOTES.md`: "linux/arm64 … cross-compiled, untested on hardware."
  Now built and run on an arm64 Linux kernel with the full suite green. Still
  untested on arm64 bare metal with real input hardware.
* `PLATFORM_NOTES.md` §7: "a runtime without [the `input` group] is honestly
  blind rather than broken." True of the *engine*. Not true of the *user
  experience*, which is silent (§4.2), and not true after an attempted
  recovery (§7.1).

---

## 12. What a Linux user must actually do today

Assuming the fixes in §7 have not landed yet, this is the honest set of
instructions:

1. **Build or install.** `cd app && go build -o dexel .` with Go 1.27, or
   unpack a `dexel-<version>-linux-<arch>.tar.gz` and put `dexel` on `PATH`
   (`~/.local/bin` is the documented home). Node/npm are not needed. Both
   `arm64` and `amd64` work.
2. **Get input access *before* the first run.**
   `sudo usermod -aG input "$USER"`, then **log out and back in** — a new
   shell is not enough; the group must be in the session's credentials.
   Because of §7.1, doing this *after* dexel is already running and then
   using `dexel resume` will not work: the provider is stuck blind. **Stop the
   process and start a new one** (`dexel stop && dexel start`).
3. **Verify capture rather than assuming it.** `dexel status` will say
   `tracking on` whether or not dexel can see anything, so check the log:

   ```sh
   dexel logs -n 50 | grep 'activity provider'
   ```

   One line reading `activity provider: linux (/dev/input/event* raw reader)`
   and **no** following `activity provider start warning:` means capture is
   live. A warning means blind, and nothing will ever accrue. (A
   `%!w(<nil>)` tail on that warning is §7.3's formatting bug, not a crash.)
   The behavioural check: type for ~15 s with the UI open and watch the
   sprint bar and the `[A]` analytics keystroke count move.
4. **Autostart:** on a normal desktop with a live user session, `dexel
   autostart enable` should take the systemd `--user` path — but that path is
   unverified (§8.3). On anything headless, in a container, or over plain
   `ssh`, expect it to **fail** with a `daemon-reload` error and leave an
   orphan `~/.config/systemd/user/dexel.service`; `dexel autostart disable`
   removes it (while also exiting 1). Until §5.5 is fixed, the working
   workaround on such a box is to write
   `~/.config/autostart/dexel.desktop` by hand with the content in §5.5, or
   to write and enable the systemd unit yourself.
5. **Know what Linux does not give you.** No app identity and no app-switch
   signal — `activityLine` never names an application and `appSwitches` stays
   `0`, because focus detection is compositor-specific (ADR 0009). That is
   correct, deliberate, and shown honestly.
6. **In a container, use `--init`** (or any reaping PID 1), or `dexel stop`
   will spend ~15 s escalating and then falsely claim the hard kill failed
   (§7.4).

---

## 13. The two things a real Linux box would settle that this run could not

1. **One real desktop session, one real keyboard, one real `input` group.**
   Boot a Linux machine, run `sudo usermod -aG input "$USER"`, log out and
   back in, start dexel, and type. That single sitting would confirm the
   README's central Linux instruction end to end, prove the evdev key/mouse
   code assumptions against real drivers rather than against bytes I wrote by
   hand, and — by starting blind first and then fixing the group — either
   reproduce or refute §7.1's sticky-blind bug in the exact shape a user will
   meet it.
2. **`dexel autostart enable` inside a real `systemd --user` session,
   across a logout.** That is the only way to test the branch §5.5 could not
   reach: unit written, `daemon-reload`, `enable --now`, the runtime actually
   coming back after a re-login, `journalctl --user -u dexel` carrying its
   output, and `disable` leaving nothing behind. It would also pin down what
   `is-system-running` prints on a *working* session, which is the value the
   detection fix in §5.5 needs to be written against.
