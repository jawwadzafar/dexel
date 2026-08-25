//! dexel desktop shell — a native window around dexel's own background runtime.
//!
//! Implements ADR 0015 / docs/plan/F3-design.md task T1. dexel is NOT a Rust
//! app: the product is a Go backend serving an HTML/NES.css frontend over
//! loopback (ADR 0011). This crate is packaging only — it finds the runtime
//! that Go binary manages and points a webview at the URL it reports.
//!
//! ## The window is a VIEW. It does not own the game.
//!
//! This is the one load-bearing rule in this file. dexel's whole product is
//! that it watches your real activity all day; a user closes a window and
//! forgets about it, so **capture must never depend on a window being open**.
//! `docs/plan/RUN-MODES.md` mode P states the contract: "Closing the browser
//! tab or app window does not stop the runtime — only `dexel stop` does."
//!
//! The first version of this shell broke that contract: it spawned its own
//! private copy of the bundled Go binary (the `SIDECAR_NAME` artifact, then
//! named `dexel-server`) with `-addr 127.0.0.1:0` and SIGTERM'd it on window
//! close. That server took no `runtime.lock` and wrote no `runtime.json`, so
//! `dexel status` reported "dexel is not running" while it was very much
//! running — and closing the window silently stopped all activity capture. It
//! could also run *alongside* a real runtime, giving two processes two
//! in-memory economies saving over one `state.db`.
//!
//! So this shell now attaches instead of spawning:
//!
//! 1. `setup` resolves a `dexel` binary to drive — `dexel` on `PATH`, then
//!    the `BinDir` convention (`~/.local/bin/dexel`, `%LOCALAPPDATA%\dexel\bin`
//!    on Windows), then the bundled copy — and runs `status --json` to read
//!    the runtime's URL. Preferring an INSTALLED dexel is deliberate
//!    (ARCHITECTURE.md Decision 17): it is the binary `dexel update` keeps
//!    current, so the window never drives a stale server the user has
//!    already upgraded past. The bundled `bundle.externalBin` copy is the
//!    fallback for someone who installed only the app.
//! 2. If nothing is running, it runs `start` — the SAME detached,
//!    lock-taking, `runtime.json`-writing runtime the CLI starts, not a
//!    private child — and asks again for the URL.
//! 3. It builds the single window on that URL via [`tauri::WebviewUrl::External`].
//!    The same `status --json` call also carries the user's window
//!    PREFERENCES (`prefs.alwaysOnTop`, SET-1 / docs/ui-spec.md §11), which
//!    is why this shell needs no IPC into the page to honour a setting the
//!    page owns — see [`Prefs`] and [`apply_prefs`].
//!    Because the page's origin IS the server's origin, the frontend's
//!    `location.host`-derived WebSocket URL is already correct and the
//!    server's same-origin check accepts it with no `-insecure-origin` and no
//!    wildcard (ADR 0015 §Security).
//! 4. On window close this shell terminates NOTHING. The runtime keeps
//!    ticking, keeps counting keystrokes and keeps autosaving. `dexel stop`
//!    is the only thing that stops it — that is the whole point.
//!
//! One thing this shell does own outright: the window's SIZE. The game is a
//! fixed 640x400 surface, so the window is constrained to whole multiples of
//! it (1x..3x) and a settled resize snaps onto that ladder — see the
//! "WINDOW-FIT" section, and `docs/ui-spec.md` §0.4.
//!
//! Since EMBED-1 (docs/plan/ROADMAP.md) the sidecar is a SELF-CONTAINED
//! binary: it carries the frontend (`app/public`) and the sprites
//! (`app/assets`) inside itself via `go:embed`, so there is nothing for this
//! shell to locate, bundle as a Tauri resource, or point it at.
//!
//! Why the shell — not the JS — learns the port: the window cannot be created
//! until the URL is known, so the URL has to arrive before any page loads.
//!
//! ## Verified against
//!
//! * <https://v2.tauri.app/develop/sidecar/> — `bundle.externalBin`, the
//!   `<name>-<target-triple>` naming rule (see `SIDECAR_NAME` below for what
//!   `<name>` is and why it is the one capitalised artifact in the tree),
//!   `app.shell().sidecar(..)`,
//!   `(Receiver<CommandEvent>, CommandChild)`, `CommandEvent::Stdout(Vec<u8>)`.
//! * <https://docs.rs/tauri/latest/tauri/webview/struct.WebviewWindowBuilder.html>
//!   and `tauri::{WebviewUrl, RunEvent, Url}` (tauri 2.11.x).
//! * The `dexel status --json` / `dexel start` contract in
//!   `app/cmd_lifecycle.go` (ADR 0018), including the exit-code note on
//!   [`discover_runtime`].

use std::path::PathBuf;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::mpsc::{self, RecvTimeoutError};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use tauri::{Manager, RunEvent, WebviewUrl, WebviewWindowBuilder};
use tauri_plugin_shell::process::CommandEvent;
use tauri_plugin_shell::ShellExt;

/// The `bundle.externalBin` base name. Tauri appends the target triple on
/// disk (`Dexel-x86_64-unknown-linux-gnu`) and strips it again inside the
/// bundle, so the name used here is the bare one — and it must stay in
/// lockstep with `tauri.conf.json`'s `externalBin` entry and the
/// `shell:allow-execute` scope entry in `capabilities/default.json`.
/// `bundle_layout` asserts that lockstep so a half-finished rename fails
/// `cargo test` instead of failing at bundle time.
///
/// ## Why this ONE artifact is capitalised, when every other artifact is not
///
/// It is the only artifact whose filename a human reads. macOS's System
/// Settings -> General -> Login Items & Extensions -> App Background Activity
/// pane names each background item after the executable that is actually
/// `exec`'d — not after the launchd `Label`, and not after the enclosing
/// bundle (PLATFORM_NOTES.md §3.1.1). `dexel autostart enable` points its
/// LaunchAgent at this file inside the installed `Dexel.app`, so whatever
/// this is called is the string the owner sees in that pane. It has been
/// `dexel-server` (the pane read "dexel-server") and then `Dexel Runtime`;
/// PLATFORM_NOTES.md §3.1.2 and §3.1.3 record each observation and its date.
///
/// ## How `Dexel` became available, and the trap that made it unavailable before
///
/// `Contents/MacOS/` is ONE FLAT DIRECTORY, and macOS's default APFS volume
/// is case-INsensitive, so `Dexel` and `dexel` name the same file there.
/// While the Tauri shell's own main binary was called `dexel`
/// (`mainBinaryName`), this name was therefore unusable — and the collision
/// would not even have errored: tauri-bundler copies external binaries first
/// (`Settings::copy_binaries`) and the main binary second
/// (`copy_binaries_to_bundle`), both with a plain `fs::copy`, so the Rust GUI
/// shell would have silently overwritten the Go daemon and the LaunchAgent
/// would have opened a window at every login instead of starting the runtime.
///
/// The main binary was therefore renamed to `dexel-desktop` — which is
/// already the product's own word for it (`app/cmd_lifecycle.go`'s
/// `desktopAppName`, ARCHITECTURE.md Decision 17) — which frees `Dexel` for
/// the daemon. See `../Cargo.toml`'s `[package]` header for the full
/// three-names table and what the rename costs.
///
/// The invariant underneath all of that — no two executables destined for
/// one flat install directory may collide case-insensitively — is now
/// enforced by `bundle_layout`, because it was unenforced twice and cost
/// real debugging both times.
const SIDECAR_NAME: &str = "Dexel";

/// How long any one short-lived CLI call (`status`, `start`) gets before we
/// give up and fail loudly. `start` is the slow one and it only has to fork a
/// child and wait for its readiness probe, so this is a "something is badly
/// wrong" bound, not a tuning knob.
const CLI_TIMEOUT: Duration = Duration::from_secs(20);

const WINDOW_LABEL: &str = "main";
/// The window's title bar — a DISPLAY string, so it is the capitalised product
/// name that `tauri.conf.json`'s `productName` also carries. The rest of this
/// file stays lowercase `dexel` on purpose: those are artifacts, not display
/// text (`~/.local/bin/dexel`, the `dexel stop` command a log line tells the
/// user to run, and `dexel-desktop`, which is what this shell's own binary is
/// called inside the bundle). `SIDECAR_NAME` is the one deliberate exception,
/// and its doc comment says why: that filename IS display text, because
/// System Settings names the login item after it.
const WINDOW_TITLE: &str = "Dexel";
/// The game surface, in logical (CSS) pixels: the fixed 640x400 layout
/// `#root` declares in `app/public/css/game.css` and `docs/ui-spec.md` §0
/// pins. Every size in this file is 640x400 times something.
///
/// It used to be 660x460 here — "the 640x400 canvas plus its sprint/ticker
/// chrome". That was already wrong when it was written (the chrome moved
/// INSIDE the 640x400 root in BUG-2) and the window has been 20x60 too big
/// ever since: the page letterboxed the surface into it with a 10px pillarbox
/// and a 30px letterbox, so the game did not touch a single window edge.
/// WINDOW-FIT is the fix, and it fixes it from THIS side — the page's
/// letterbox is left alone as the fallback it always was.
const SURFACE_W: f64 = 640.0;
const SURFACE_H: f64 = 400.0;

/// The scale range the WINDOW may be sized to: 1x (640x400) to 3x
/// (1920x1200), enforced by `min_inner_size`/`max_inner_size` so the OS —
/// not this code — is what stops a drag at the ends.
///
/// The floor is the surface itself: the layout is fixed-pixel and does not
/// reflow, so below 1x there is nothing to give. (The page would scale down
/// to fit rather than clip, but a window smaller than the game is not a shape
/// this product wants, and a min size is the honest way to say so.)
///
/// The ceiling is `render/viewport.ts`'s `MAX_SCALE`, and it is a PRODUCT
/// decision, not a technical one — dexel's 8px type stops reading as cozy
/// somewhere past 3x. The two numbers must agree: a window the page refuses
/// to fill is exactly the letterbox this item exists to remove.
const MIN_SCALE: f64 = 1.0;
const MAX_SCALE: f64 = 3.0;

/// The largest scale a FRESH window opens at, as opposed to the largest it
/// can be dragged to. 2x (1280x800) is a companion-sized window on a 1080p
/// screen; 3x (1920x1200) is most of one, and a first launch that fills the
/// display reads as an app that thinks it is the main event. The user can
/// still drag to 3x, and this shell does not remember that they did — see
/// `opening_size`.
const DEFAULT_MAX_SCALE: f64 = 2.0;

/// How long after the LAST resize event the snap fires. tao exposes no
/// resize-END event on any platform (`tauri::WindowEvent` has only
/// `Resized`), so "the user let go" has to be inferred from the events going
/// quiet. Long enough not to fight a slow drag, short enough that the snap
/// feels like part of releasing the mouse rather than a later correction.
const RESIZE_SETTLE: Duration = Duration::from_millis(200);

/// WINDOW-POLISH (docs/plan/ROADMAP.md) — the one bit of information this
/// shell tells the page about itself, appended to the loopback URL it loads.
///
/// The page is dexel's ordinary frontend, served by the Go daemon to browsers
/// and to this window alike, and it must look byte-identical in a browser tab.
/// Two things are true only here: there is no native frame (see
/// [`build_window`]'s `decorations(false)`), so the page has to supply
/// close/minimize itself; and `#titlebar` is the window's drag handle. Neither
/// is something the page can *detect* — `window.__TAURI__` is injected into
/// every document the webview loads and says nothing about whether a frame
/// exists — so the shell DECLARES it instead. `app/frontend/src/env.ts`'s
/// `SHELL_MODE` is the other end of this contract, and `features/
/// shell-window.ts` is the only module that reads it.
///
/// A query string and not a fragment, a header or an injected script: the Go
/// server ignores unknown query parameters on `/` (it serves index.html
/// regardless), a fragment is not readable before the first paint in every
/// engine, a header would need the daemon to cooperate, and an injected
/// initialization script would put shell-only behaviour in a place the browser
/// build cannot be tested against. The origin is unchanged by a query, so the
/// frontend's `location.host`-derived WebSocket URL and the server's
/// same-origin check are untouched.
const SHELL_QUERY: &str = "shell=1";

/// Which `dexel` binary this shell drives (ARCHITECTURE.md Decision 17).
///
/// `bundle.externalBin` is no longer "a sidecar the shell owns"; it is the
/// FALLBACK dexel for someone who installed only the app bundle and never ran
/// the install script. An installed dexel wins because it is the one
/// `dexel update` replaces.
#[derive(Debug, Clone)]
enum Driver {
    /// A dexel found on `PATH` or at the `BinDir` convention.
    Installed(PathBuf),
    /// The copy shipped inside this bundle.
    Bundled,
}

impl Driver {
    fn describe(&self) -> String {
        match self {
            Driver::Installed(p) => p.display().to_string(),
            Driver::Bundled => format!("{SIDECAR_NAME} (bundled)"),
        }
    }
}

/// The BinDir convention, mirroring `app/internal/paths`' `binDirFor`:
/// `~/.local/bin` on Linux AND macOS (never `/usr/local/bin` — the installer
/// must work without sudo), `%LOCALAPPDATA%\dexel\bin` on Windows.
fn bin_dir_candidate() -> Option<PathBuf> {
    if cfg!(windows) {
        let base = std::env::var_os("LOCALAPPDATA")
            .map(PathBuf::from)
            .or_else(|| {
                std::env::var_os("USERPROFILE")
                    .map(|h| PathBuf::from(h).join("AppData").join("Local"))
            })?;
        return Some(base.join("dexel").join("bin").join("dexel.exe"));
    }
    let home = std::env::var_os("HOME")?;
    Some(PathBuf::from(home).join(".local").join("bin").join("dexel"))
}

/// The first `dexel` on `PATH`, if any. A hand-rolled `which`: one env var
/// and a stat per entry is not worth a dependency.
fn dexel_on_path() -> Option<PathBuf> {
    let exe = if cfg!(windows) { "dexel.exe" } else { "dexel" };
    let path = std::env::var_os("PATH")?;
    std::env::split_paths(&path)
        .map(|dir| dir.join(exe))
        .find(|cand| cand.is_file())
}

/// Decision 17's lookup order: `PATH` -> `BinDir` -> the bundled copy.
fn resolve_driver() -> Driver {
    if let Some(p) = dexel_on_path() {
        return Driver::Installed(p);
    }
    if let Some(p) = bin_dir_candidate() {
        if p.is_file() {
            return Driver::Installed(p);
        }
    }
    Driver::Bundled
}

/// The user preferences this shell acts on, as published by
/// `dexel status --json`'s `prefs` block (`app/cmd_lifecycle.go`).
///
/// # Why the CLI, and not the page
///
/// The window's page is loaded from the runtime's own origin, so this shell
/// has no IPC channel into it (the same constraint
/// [`watch_for_a_moved_runtime`] documents). It does already run
/// `status --json` — at launch, and again on every window focus — so a
/// preference the SHELL has to act on rides that call. `status` reads it
/// straight out of `config.json`, the file every `SET_PREF` writes through
/// to immediately, so this is fresh without polling anything.
///
/// # `Default` is the honest default, and it is FALSE
///
/// This shell hardcoded `always_on_top(true)` for its whole life, on ADR
/// 0007's reasoning ("a companion the editor buries never gets seen").
/// The reasoning stands; forcing it on everyone did not — on macOS a window
/// that will not go behind anything is an obstruction, not a companion. So
/// the behaviour is kept and the DEFAULT is inverted: off unless the user
/// turns it on in Settings. `Default::default()` is therefore also the right
/// answer for an older `dexel` whose `status --json` has no `prefs` block at
/// all — an unconfigured window is an ordinary window.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
struct Prefs {
    always_on_top: bool,
}

/// Read [`Prefs`] out of a parsed `status --json` object.
///
/// Every field degrades to `false` independently: a missing `prefs` block, a
/// missing key inside it, or a value of the wrong JSON type all mean "not
/// configured", which is exactly what an absent preference means. This
/// never fails, because a status answer that is useful for finding the
/// runtime must not be thrown away over a preference.
fn prefs_from_status(parsed: &serde_json::Value) -> Prefs {
    let flag = |name: &str| {
        parsed
            .get("prefs")
            .and_then(|p| p.get(name))
            .and_then(|v| v.as_bool())
            .unwrap_or(false)
    };
    Prefs {
        always_on_top: flag("alwaysOnTop"),
    }
}

/// What `status --json` said: where the runtime is (`None` = nothing
/// running, a normal answer) and what the user's preferences are.
///
/// The two are deliberately carried together, and `prefs` is populated on
/// BOTH branches: a preference lives in `config.json` and is just as true
/// with nothing running, so this shell never has to spawn a second process
/// to learn it, and never has to wait for a runtime to exist before honouring
/// it.
struct Discovery {
    url: Option<String>,
    prefs: Prefs,
}

/// What one short-lived `dexel <args>` run said.
///
/// `code` is deliberately kept and reported rather than turned into an error
/// here: for `status --json`, "no runtime is running" is a perfectly normal
/// answer and not a failure, so the exit code is not the authority on it
/// (see [`discover_runtime`]). `start`'s code IS checked.
struct CliOutput {
    stdout: String,
    code: Option<i32>,
}

/// Run the resolved dexel binary as a one-shot CLI command and collect its
/// stdout.
///
/// Nothing long-lived comes out of this: every call is a command that runs,
/// prints and exits. The one process that outlives it — the runtime `start`
/// detaches — is deliberately not our child (see the module docs).
fn run_cli(app: &tauri::AppHandle, driver: &Driver, args: &[&str]) -> Result<CliOutput, String> {
    match driver {
        Driver::Installed(path) => run_installed(path, args),
        Driver::Bundled => run_bundled(app, args),
    }
}

/// Run an installed `dexel` directly. A plain `std::process::Command` in a
/// thread, so the [`CLI_TIMEOUT`] bound applies here exactly as it does to the
/// bundled path — `output()` on its own would wait forever on a wedged child.
fn run_installed(path: &std::path::Path, args: &[&str]) -> Result<CliOutput, String> {
    let (tx, rx) = mpsc::channel::<Result<CliOutput, String>>();
    let owned_path = path.to_path_buf();
    let owned_args: Vec<String> = args.iter().map(|a| (*a).to_string()).collect();
    let label = args.join(" ");
    std::thread::Builder::new()
        .name("dexel-cli".into())
        .spawn(move || {
            let result = std::process::Command::new(&owned_path)
                .args(&owned_args)
                .output()
                .map_err(|e| {
                    format!(
                        "run `{} {}`: {e}",
                        owned_path.display(),
                        owned_args.join(" ")
                    )
                })
                .map(|out| {
                    let stderr = String::from_utf8_lossy(&out.stderr);
                    for line in stderr.lines() {
                        log::info!("[{label}] {line}");
                    }
                    CliOutput {
                        stdout: String::from_utf8_lossy(&out.stdout).into_owned(),
                        code: out.status.code(),
                    }
                });
            let _ = tx.send(result);
        })
        .map_err(|e| format!("spawn thread for `dexel {}`: {e}", args.join(" ")))?;

    match rx.recv_timeout(CLI_TIMEOUT) {
        Ok(result) => result,
        Err(RecvTimeoutError::Timeout) => Err(format!(
            "`dexel {}` did not finish within {CLI_TIMEOUT:?}",
            args.join(" ")
        )),
        Err(RecvTimeoutError::Disconnected) => Err(format!(
            "`dexel {}` thread died before reporting",
            args.join(" ")
        )),
    }
}

/// Run the bundled copy through the sidecar mechanism, which is what it is
/// good at: locating the binary built for this exact target triple.
///
/// The reader thread drains the event stream to completion rather than
/// stopping at the first interesting line — it is the only reader of the
/// child's stdout/stderr pipes, and abandoning them could block the Go
/// process on a full pipe buffer.
fn run_bundled(app: &tauri::AppHandle, args: &[&str]) -> Result<CliOutput, String> {
    let mut command = app
        .shell()
        .sidecar(SIDECAR_NAME)
        .map_err(|e| format!("locate sidecar {SIDECAR_NAME:?}: {e}"))?;
    for arg in args {
        command = command.arg(arg);
    }

    // The child handle is held (not dropped) until the stream ends, so the
    // process is never disturbed mid-run.
    let (mut rx, _child) = command
        .spawn()
        .map_err(|e| format!("spawn `{SIDECAR_NAME} {}`: {e}", args.join(" ")))?;

    let (tx, out_rx) = mpsc::channel::<CliOutput>();
    let label = args.join(" ");
    std::thread::Builder::new()
        .name("dexel-cli-reader".into())
        .spawn(move || {
            let mut stdout = String::new();
            let mut code = None;
            // blocking_recv() is correct here and cannot panic: this is a
            // plain OS thread with no tokio runtime entered (blocking_recv
            // only panics when called from inside an async context).
            while let Some(event) = rx.blocking_recv() {
                match event {
                    CommandEvent::Stdout(bytes) => {
                        stdout.push_str(&String::from_utf8_lossy(&bytes));
                    }
                    // The Go CLI's human-readable output goes to stderr;
                    // forward it so a packaged failure is diagnosable from
                    // the log file alone.
                    CommandEvent::Stderr(bytes) => {
                        log::info!("[{label}] {}", String::from_utf8_lossy(&bytes).trim_end());
                    }
                    CommandEvent::Error(err) => {
                        log::error!("[{label}] pipe error: {err}");
                    }
                    CommandEvent::Terminated(payload) => {
                        code = payload.code;
                    }
                    // CommandEvent is #[non_exhaustive]; ignore anything a
                    // future plugin release adds rather than failing to build.
                    _ => {}
                }
            }
            // A send error just means setup gave up waiting; nothing to do.
            let _ = tx.send(CliOutput { stdout, code });
        })
        .map_err(|e| {
            format!(
                "spawn reader thread for `{SIDECAR_NAME} {}`: {e}",
                args.join(" ")
            )
        })?;

    match out_rx.recv_timeout(CLI_TIMEOUT) {
        Ok(out) => Ok(out),
        Err(RecvTimeoutError::Timeout) => Err(format!(
            "`{SIDECAR_NAME} {}` did not finish within {CLI_TIMEOUT:?}",
            args.join(" ")
        )),
        Err(RecvTimeoutError::Disconnected) => Err(format!(
            "`{SIDECAR_NAME} {}` reader thread died before reporting",
            args.join(" ")
        )),
    }
}

/// Ask the Go CLI whether a runtime is already running, and where.
///
/// `Ok(None)` means "nothing running" — a normal answer, not a failure. The
/// authority for that is the `running` field in the JSON, never the exit
/// code: `cmdStatus` in app/cmd_lifecycle.go exits **0 whether or not a
/// runtime is running**, precisely so callers like this one are not forced to
/// treat a normal answer as an error. (An earlier version of this comment
/// claimed it exits 1; measured on Linux 2026-08-24, `status --json` with no
/// runtime exits 0, and the Go source says so explicitly. Reading `running`
/// was correct either way, which is why the wrong comment cost nothing.)
/// `status` also verifies liveness by
/// round-tripping an HTTP call to the runtime it found, so a `true` here means
/// a server that actually answers, not just a pid file.
fn discover_runtime(app: &tauri::AppHandle, driver: &Driver) -> Result<Discovery, String> {
    let out = run_cli(app, driver, &["status", "--json"])?;
    let parsed: serde_json::Value = serde_json::from_str(out.stdout.trim()).map_err(|e| {
        format!(
            "`status --json` printed unparseable JSON ({e}): {}",
            out.stdout
        )
    })?;
    // Read before the running check: a preference lives in config.json, so
    // it is answered identically either way (see `Discovery`).
    let prefs = prefs_from_status(&parsed);

    if parsed.get("running").and_then(|v| v.as_bool()) != Some(true) {
        if let Some(reason) = parsed.get("reason").and_then(|v| v.as_str()) {
            log::info!("no runtime yet: {reason}");
        }
        return Ok(Discovery { url: None, prefs });
    }
    let url = parsed.get("url").and_then(|v| v.as_str()).ok_or_else(|| {
        format!(
            "`status --json` reported running with no url: {}",
            out.stdout
        )
    })?;
    Ok(Discovery {
        url: Some(url.to_string()),
        prefs,
    })
}

/// The URL to open the window on — an already-running runtime if there is
/// one, otherwise a freshly started one — plus the user's window
/// preferences, which `status --json` answers in the same breath.
///
/// `start` is the CLI's own detached-runtime path — it takes the lock, writes
/// `runtime.json` and waits for readiness — so a runtime started here is
/// indistinguishable from one started by `dexel start` in a terminal, and
/// outlives this window exactly the same way.
fn runtime_url(app: &tauri::AppHandle) -> Result<(String, Prefs), String> {
    let driver = resolve_driver();
    log::info!("driving {}", driver.describe());
    let found = discover_runtime(app, &driver)?;
    if let Some(url) = found.url {
        log::info!("attaching to the running dexel runtime at {url} (this window owns nothing)");
        return Ok((url, found.prefs));
    }

    log::info!("no runtime running; starting the detached runtime via `start`");
    let out = run_cli(app, &driver, &["start"])?;
    if out.code != Some(0) {
        return Err(format!(
            "`{} start` failed (exit {:?}); see the [start] lines above for why",
            driver.describe(),
            out.code
        ));
    }

    let started = discover_runtime(app, &driver)?;
    match started.url {
        Some(url) => {
            log::info!("started the dexel runtime at {url}");
            log::info!("it keeps running after this window closes — `dexel stop` is what stops it");
            Ok((url, started.prefs))
        }
        None => {
            Err("`start` reported success but no runtime was discoverable afterwards".to_string())
        }
    }
}

/// Apply [`Prefs`] to the window, calling into the platform ONLY for what
/// actually changed.
///
/// `applied` is what this shell last told the window, not what it wishes were
/// true, and the comparison against it is the whole point: this runs on every
/// window focus, and re-asserting `set_always_on_top` on a window that is
/// already there is a pointless trip into the window manager several times a
/// minute. Same discipline as [`re_point_if_moved`]'s "an unchanged URL means
/// no navigation at all".
///
/// A failure is logged and NOT propagated: this shell's job is to show the
/// game, and a window manager that refuses a hint is not a reason to take the
/// window away. `applied` is only advanced on success, so the next focus
/// retries rather than believing a call that failed.
fn apply_prefs(window: &tauri::WebviewWindow, wanted: Prefs, applied: &Mutex<Prefs>) {
    let mut applied = applied.lock().unwrap_or_else(|p| p.into_inner());
    if applied.always_on_top == wanted.always_on_top {
        return;
    }
    match window.set_always_on_top(wanted.always_on_top) {
        Ok(()) => {
            log::info!(
                "always-on-top is now {} (from `status --json`'s prefs.alwaysOnTop)",
                if wanted.always_on_top { "on" } else { "off" }
            );
            applied.always_on_top = wanted.always_on_top;
        }
        Err(e) => log::warn!(
            "could not set always-on-top to {}: {e}",
            wanted.always_on_top
        ),
    }
}

/// How long the focus re-resolve waits before it will ask again. Focus fires
/// on every alt-tab; one `status --json` per window activation is cheap (a
/// short-lived process and one loopback round-trip) but not free, and nothing
/// is gained by asking twice in the same second.
const RERESOLVE_MIN_INTERVAL: Duration = Duration::from_secs(2);

/// Re-point the window if the runtime moved (docs/plan/BUGS-RESILIENCE.md R9).
///
/// # What this is for
///
/// This shell resolves the runtime's URL exactly once, in `setup`, because the
/// window cannot be created until the URL is known. That was also the whole
/// bug: a runtime binds an ephemeral port, both supervisors restart it after a
/// crash (systemd `Restart=on-failure`, launchd `KeepAlive`), and the page in
/// this window would then retry a dead port forever while a perfectly healthy
/// runtime answered on another one. The window was a grey box and the only
/// recovery was for the user to close and reopen it.
///
/// The RUNTIME's own fix — a sticky port (`app/main.go`'s `listenRuntime`) —
/// handles the common case, and handles it better than this can, because it
/// also fixes a bookmarked browser tab that this shell knows nothing about.
/// This is the backstop for when the old port was genuinely unavailable and
/// the restarted runtime had to take a new one.
///
/// # Why focus, and not a timer or a WS hook
///
/// The page is loaded from the runtime's own origin, so this shell has no IPC
/// channel into it and cannot be told "my WebSocket is down" — the frontend's
/// reconnect loop is entirely inside the page. Window focus is the honest
/// substitute: it is exactly the moment a human looks at the window, i.e. the
/// only moment a stale page matters, and it costs nothing when nothing moved
/// (an unchanged URL means no navigation at all, so a focused window is never
/// reloaded out from under someone).
///
/// It deliberately does NOT start a runtime. `dexel stop` means stopped, and a
/// window that resurrected the daemon every time it was clicked would be its
/// own bug.
fn watch_for_a_moved_runtime(app: &tauri::App, loaded: tauri::Url, loaded_prefs: Prefs) {
    let Some(window) = app.get_webview_window(WINDOW_LABEL) else {
        log::warn!(
            "no window labelled {WINDOW_LABEL} to watch; it will not notice a moved runtime"
        );
        return;
    };
    let handle = app.handle().clone();
    // The URL the webview is actually on, so a re-resolve can tell "moved"
    // from "same place".
    let current = Arc::new(Mutex::new(loaded));
    // One check at a time, and not more often than RERESOLVE_MIN_INTERVAL:
    // focus events arrive in bursts and each check spawns a process.
    let checking = Arc::new(AtomicBool::new(false));
    let last = Arc::new(Mutex::new(None::<Instant>));
    let target = window.clone();
    // SET-1: what this shell has last told the window about its preferences,
    // seeded with what `build_window` already applied at creation time — so
    // the first focus does not re-assert a setting the builder just made.
    let applied_prefs = Arc::new(Mutex::new(loaded_prefs));

    window.on_window_event(move |event| {
        if !matches!(event, tauri::WindowEvent::Focused(true)) {
            return;
        }
        {
            let mut last = last.lock().unwrap_or_else(|p| p.into_inner());
            if let Some(when) = *last {
                if when.elapsed() < RERESOLVE_MIN_INTERVAL {
                    return;
                }
            }
            *last = Some(Instant::now());
        }
        if checking.swap(true, Ordering::SeqCst) {
            return; // a previous check is still running
        }
        // Off the event thread: `status --json` spawns a process and waits on
        // a loopback round-trip, and blocking here would freeze the UI.
        let handle = handle.clone();
        let target = target.clone();
        let current = current.clone();
        let checking = checking.clone();
        let applied_prefs = applied_prefs.clone();
        std::thread::spawn(move || {
            re_resolve(&handle, &target, &current, &applied_prefs);
            checking.store(false, Ordering::SeqCst);
        });
    });
}

/// One re-resolve: ask the CLI where the runtime is and what the user's
/// preferences are, then navigate only if the runtime moved and touch the
/// window's flags only if a preference changed.
///
/// The two halves are deliberately independent. The preference half runs on
/// EVERY answer, including "nothing is running" — a preference lives in
/// `config.json`, so a toggle flipped in the page (or by hand in that file)
/// must reach the window on the next focus whether or not the daemon
/// happens to be up. The navigation half is unchanged: it fires only for a
/// genuinely moved runtime, and never starts one.
fn re_resolve(
    app: &tauri::AppHandle,
    window: &tauri::WebviewWindow,
    current: &Mutex<tauri::Url>,
    applied_prefs: &Mutex<Prefs>,
) {
    let driver = resolve_driver();
    let found = match discover_runtime(app, &driver) {
        Ok(found) => found,
        Err(e) => {
            log::warn!("could not ask where the runtime is: {e}");
            return;
        }
    };

    apply_prefs(window, found.prefs, applied_prefs);

    let Some(url) = found.url else {
        // Not an error and not something to fix from here: `dexel stop` means
        // stopped, and this shell does not resurrect the daemon.
        log::info!("no runtime is running; leaving this window where it is");
        return;
    };
    let parsed = match tauri::Url::parse(&url) {
        Ok(u) => u,
        Err(e) => {
            log::warn!("`status --json` reported an unparseable URL {url:?}: {e}");
            return;
        }
    };
    let mut cur = current.lock().unwrap_or_else(|p| p.into_inner());
    if *cur == parsed {
        return; // the overwhelmingly common case: nothing moved
    }
    log::info!(
        "the runtime moved from {} to {parsed} — re-pointing this window",
        *cur
    );
    // Navigate to the SHELL url (with ?shell=1) but remember the bare one, so
    // the comparison above stays bare-vs-bare — see shell_url's doc comment.
    if let Err(e) = window.navigate(shell_url(&parsed)) {
        log::error!("could not navigate the window to {parsed}: {e}");
        return;
    }
    *cur = parsed;
}

/// The runtime URL with [`SHELL_QUERY`] on it — what this window actually
/// loads, as opposed to what `status --json` reported.
///
/// Kept as a pure function of the bare URL, and used at BOTH of the two places
/// that point the webview somewhere ([`build_window`] and [`re_resolve`]'s
/// navigate), because the two must never disagree: a window that reloaded
/// without `?shell=1` after the runtime moved would silently lose its own
/// close and minimize buttons and become unclosable from inside.
///
/// The bare URL stays the one this shell REMEMBERS (see `re_resolve`'s
/// `current`), so the "has the runtime moved?" comparison is bare-vs-bare. It
/// used to be the same value as the loaded URL; conflating the two now would
/// make every focus event see a difference and re-navigate the window
/// endlessly.
///
/// `set_query` replaces any existing query rather than appending, which is
/// what we want: the runtime's URL never carries one, and if it ever did,
/// dropping it is safer than producing `?a=b?shell=1`.
fn shell_url(url: &tauri::Url) -> tauri::Url {
    let mut shell = url.clone();
    shell.set_query(Some(SHELL_QUERY));
    shell
}

// ---------------------------------------------------------------------------
// WINDOW-FIT — the window is constrained so the GAME fills it, edge to edge.
// ---------------------------------------------------------------------------
//
// ## What was wrong
//
// The window opened at 660x460 and could be dragged to anything. The page's
// job (`app/frontend/src/render/viewport.ts`) is to scale the fixed 640x400
// layout to fit whatever window it is given, snapping to a CRISP factor —
// one where an art pixel covers a whole number of device pixels — and
// letterboxing the remainder in the layout's own `--shadow`. So at 660x460 it
// chose 1x and painted a 10px pillarbox and a 30px letterbox, and at every
// other hand-dragged size it painted whatever was left over. The screenshot
// the owner sent is that: a game that never touches the edges of its own
// window, framed by dead space that reads as a rendering fault rather than as
// a bezel.
//
// Both halves are correct in isolation. The letterbox is the right answer to
// "you have been given a window that is not a crisp multiple of the surface",
// and it MUST stay: it is the whole of the browser-tab story (a tab is any
// size at all), and it is what covers the moments during a drag before the
// snap lands. What was missing is that in a window this shell OWNS, being
// given a non-crisp size is avoidable.
//
// ## What this does instead
//
// Constrain the window, and let the page's existing rule find offsets of
// (0, 0) on its own:
//
//   * `min_inner_size` = 640x400 (1x), `max_inner_size` = 1920x1200 (3x).
//     The OS/window manager enforces the range during a drag, so there is no
//     size to "correct" outside it and no fight with the compositor.
//   * a fresh window opens at the largest crisp size that fits the monitor's
//     work area, capped at 2x (`opening_size`).
//   * after a user resize SETTLES, the window's inner size is set to the
//     nearest crisp size (`nearest_crisp_size`), so the surface fills it
//     exactly. The page then computes `exact = 1/1.5/2/...`, snaps to itself,
//     and the letterbox it draws is 0px wide on both axes.
//
// The result is that the letterbox logic is untouched and *unreachable* in
// this window at rest — the game touches all four edges — while staying the
// fallback for a browser tab, for a maximized window (see `snap_once`) and
// for the ~200ms of a live drag.
//
// ## The ladder is the PAGE's ladder, derived from the display
//
// `viewport.ts`'s `crispStep()` is `dpr >= 1 && dpr is whole ? 1/dpr : 1`, so
// the crisp scales are `1, 1+step, 1+2*step, ... <= 3`:
//
//   dpr 1            step 1     1x, 2x, 3x           640x400, 1280x800, 1920x1200
//   dpr 2            step 0.5   1x, 1.5x, 2x, ...    640x400, 960x600, 1280x800,
//                                                    1600x1000, 1920x1200
//   dpr 3            step 1/3   1x, 1.33x, ...       see the integer filter below
//   dpr 1.25 (125%)  step 1     1x, 2x, 3x           no crisp increment exists
//
// Those are the same numbers the page will choose, which is the point: this
// shell hands the page a size that is already on its ladder.
//
// **The integer filter.** A window's inner size is whole pixels, and every
// length in this design is an integer (ui-spec.md §0). At dpr 3 a step of 1/3
// makes 640 * 4/3 = 853.33 logical px — not a size a window can have, and
// rounding it to 853 would hand the page a viewport a third of a logical pixel
// (one whole device pixel) short of 1.333x, which its `Math.floor` then reads
// as **1x**: the perfect fit would be the case that looks worst. So
// `crisp_sizes` keeps only the
// steps whose logical size is whole on both axes, which on a dpr-3 display
// leaves exactly the integer scales (1x, 2x, 3x) and on dpr 1 and 2 leaves
// the whole ladder. Stated as a cost: on a dpr-3 phone-class display the
// window snaps in bigger jumps than the page could technically render.
//
// ## Why logical pixels everywhere
//
// `min_inner_size`, `max_inner_size` and `LogicalSize` are all logical, and
// logical px == CSS px is what the page measures with
// `documentElement.clientWidth`. `WindowEvent::Resized` is the one PHYSICAL
// value in the loop, and `snap_once` converts it once, immediately, using the
// window's own scale factor.

/// A window inner size in LOGICAL (CSS) pixels — the unit both ends of this
/// contract speak. Deliberately a plain pair: every function below is pure
/// arithmetic over it, so the whole of the snap decision is testable without
/// a window (which matters more than usual here — see ../README.md §5, this
/// project's Linux box cannot display one at all).
type Logical = (u32, u32);

/// Is `v` a whole number of pixels? The tolerance is far below any real step
/// (the smallest is 1/3 of a pixel-scale, i.e. 213.33 px) and exists only to
/// absorb the float division in `crisp_step`.
fn is_whole(v: f64) -> bool {
    (v - v.round()).abs() < 1e-6
}

/// The smallest scale increment that still lands art pixels on whole DEVICE
/// pixels. Mirrors `crispStep()` in `app/frontend/src/render/viewport.ts`
/// exactly, and must keep mirroring it: this shell's whole job here is to
/// hand the page a size the page will not have to letterbox.
fn crisp_step(scale_factor: f64) -> f64 {
    if scale_factor >= 1.0 && is_whole(scale_factor) {
        1.0 / scale_factor
    } else {
        // A fractional device-pixel-ratio (a Windows 125% display) has no
        // crisp increment at all, so whole CSS pixels are the best available.
        1.0
    }
}

/// Every crisp inner size from 1x up to `cap`, ascending, in logical px.
///
/// Never empty: 1x is 640x400, which is whole at any scale factor.
fn crisp_sizes(scale_factor: f64, cap: f64) -> Vec<Logical> {
    let step = crisp_step(scale_factor);
    let mut sizes = Vec::new();
    let mut i = 0u32;
    loop {
        let scale = MIN_SCALE + step * f64::from(i);
        // The epsilon is on the CAP comparison, not on the scale: 1 + 2 * 0.5
        // can land a hair above 2.0 and a bare `>` would drop 2x from a dpr-2
        // ladder capped at 2x.
        if scale > cap + 1e-9 {
            break;
        }
        let (w, h) = (SURFACE_W * scale, SURFACE_H * scale);
        if is_whole(w) && is_whole(h) {
            sizes.push((w.round() as u32, h.round() as u32));
        }
        i += 1;
    }
    sizes
}

/// The ladder restricted to the sizes that fit inside `bound` — the monitor's
/// work area, when this shell could read one.
///
/// The 1x floor is kept even when it does not fit, because it is the window's
/// `min_inner_size`: on a display too small for 640x400 the honest answer is
/// "the smallest window this game has", not "no size at all".
fn crisp_sizes_within(scale_factor: f64, cap: f64, bound: Option<Logical>) -> Vec<Logical> {
    let all = crisp_sizes(scale_factor, cap);
    let Some((bw, bh)) = bound else {
        return all;
    };
    let fitting: Vec<Logical> = all
        .iter()
        .copied()
        .filter(|(w, h)| *w <= bw && *h <= bh)
        .collect();
    if fitting.is_empty() {
        all.into_iter().take(1).collect()
    } else {
        fitting
    }
}

/// Squared distance between two sizes, in logical pixels.
fn distance2(a: Logical, b: Logical) -> f64 {
    let dw = f64::from(a.0) - f64::from(b.0);
    let dh = f64::from(a.1) - f64::from(b.1);
    dw * dw + dh * dh
}

/// The crisp size NEAREST to what the user dragged the window to.
///
/// # Why nearest, and nearest by what
///
/// Nearest, not "the largest that fits": the game's aspect ratio is fixed and
/// the user's drag is not, so a drag that grows one axis a long way and the
/// other not at all has to resolve to one of them. Snapping DOWN to what fits
/// makes a big diagonal drag collapse the window (drag to 1200x750 on a dpr-1
/// display and the largest crisp size that fits is 1x — a window that jumps
/// backwards, which reads as the drag having failed). Snapping to the nearest
/// size means a drag more than halfway to the next step lands on it.
///
/// Nearest in PIXEL space — plain Euclidean distance between the two sizes —
/// rather than nearest in scale space, so the axis the user actually dragged
/// dominates the choice by how far they dragged it. Ties go to the SMALLER
/// size (the ladder is ascending and the comparison is strict), which is the
/// safe direction: it can never push the window past the edge of the screen.
///
/// `bound` keeps the answer on the monitor: without it a drag to the bottom
/// of a 1920x1080 screen is nearest to 1920x1200 and the snap would make the
/// window taller than the display it is on.
fn nearest_crisp_size(reported: Logical, scale_factor: f64, bound: Option<Logical>) -> Logical {
    let ladder = crisp_sizes_within(scale_factor, MAX_SCALE, bound);
    let mut best = ladder[0]; // `crisp_sizes_within` never returns empty
    let mut best_distance = distance2(best, reported);
    for candidate in &ladder[1..] {
        let distance = distance2(*candidate, reported);
        if distance < best_distance {
            best = *candidate;
            best_distance = distance;
        }
    }
    best
}

/// The size a fresh window opens at: the largest crisp size that fits the
/// monitor, capped at [`DEFAULT_MAX_SCALE`].
///
/// This shell deliberately does NOT remember the last size. A remembered size
/// is a file to write, a file to migrate and a file to be wrong (a size
/// remembered from a 4K monitor is off the screen of the laptop the user
/// opens the lid on), and the value it buys is small when every available
/// size is one of three or five. What it does instead is open at a size that
/// is right for the display it is actually on, every time.
fn opening_size(scale_factor: f64, bound: Option<Logical>) -> Logical {
    let ladder = crisp_sizes_within(scale_factor, DEFAULT_MAX_SCALE, bound);
    *ladder
        .last()
        .unwrap_or(&(SURFACE_W as u32, SURFACE_H as u32))
}

/// How far a reported size may be from the size this shell asked for and
/// still be the same size. One logical pixel, because the round trip through
/// physical pixels (`Resized` is physical, `set_size` is logical) can lose a
/// fraction at a non-integer scale factor.
const ECHO_TOLERANCE: u32 = 1;

/// Is this reported size just this shell's own `set_size` coming back?
fn is_own_echo(reported: Logical, target: Option<Logical>) -> bool {
    match target {
        Some((w, h)) => {
            reported.0.abs_diff(w) <= ECHO_TOLERANCE && reported.1.abs_diff(h) <= ECHO_TOLERANCE
        }
        None => false,
    }
}

/// How many times in a row this shell will ask for a size the window never
/// reports before it gives up on that size.
const MAX_UNANSWERED: u32 = 3;

/// The loop guard: the pure decision "given what the window reports and what
/// I last asked for, do I call `set_size`?".
///
/// # The two loops it exists to prevent
///
/// 1. **Our own echo.** `set_size` produces a `Resized` event, which re-arms
///    the settle timer, which snaps again. That one is benign (the second
///    pass computes the same target) but it would run forever, so a reported
///    size that already IS the target ends the chain — and, because reaching
///    the target is the definition of success, it also clears `unanswered`.
/// 2. **A window manager that says no.** If the compositor answers a
///    `set_size` with a different size (a tiling WM, a snapped/edge-tiled
///    window, a size hint it declines), the chain above never terminates:
///    every pass computes the same target and asks again, forever, ~200ms
///    apart. So asking for the SAME target without ever seeing it is counted,
///    and after [`MAX_UNANSWERED`] tries this shell stops asking and leaves
///    the window alone. The page's letterbox is then what the user sees,
///    which is exactly the fallback it is there to be.
#[derive(Debug, Default)]
struct SnapGuard {
    /// The size this shell last asked the window manager for.
    target: Option<Logical>,
    /// How many times in a row it has asked for `target` without the window
    /// ever reporting it.
    unanswered: u32,
}

impl SnapGuard {
    /// The settle timer fired on a window reporting `reported`, and
    /// `nearest_crisp_size` chose `target`. `true` if `set_size(target)` is
    /// worth calling; records the ask when it is.
    fn should_ask_for(&mut self, target: Logical, reported: Logical) -> bool {
        if is_own_echo(reported, Some(target)) {
            // Already exactly there — including the case where the user
            // dragged to a crisp size by hand, and the echo of our own
            // successful `set_size`. Loop 1 ends here.
            self.unanswered = 0;
            return false;
        }
        if self.target == Some(target) {
            if self.unanswered >= MAX_UNANSWERED {
                return false; // loop 2: asked, unanswered, stop asking
            }
            self.unanswered += 1;
        } else {
            self.target = Some(target);
            self.unanswered = 1;
        }
        true
    }
}

/// The shared state behind the settle timer. Not part of the pure math: the
/// arithmetic above is what the tests exercise, and this is only the plumbing
/// that decides *when* to run it.
struct ResizeWatch {
    guard: Mutex<SnapGuard>,
    /// When the newest resize event arrived.
    last_event: Mutex<Instant>,
    /// Whether a settle thread is already waiting, so a burst of resize
    /// events (one per frame of a drag) starts exactly one.
    armed: AtomicBool,
}

/// The monitor's usable area as a logical size, or `None` if it is not a
/// usable bound.
///
/// The WORK area and not the full resolution: a window as tall as the display
/// is a window with its bottom edge behind the taskbar/dock. `None` rather
/// than a guess when the compositor reports something smaller than the game
/// itself (some Wayland compositors report a zero-sized work area before the
/// first surface is mapped) — a bogus bound would pin the window at 1x
/// forever, which is worse than not bounding it at all.
fn work_area_bound(monitor: &tauri::window::Monitor) -> Option<Logical> {
    let scale_factor = monitor.scale_factor();
    if scale_factor <= 0.0 || !scale_factor.is_finite() {
        return None;
    }
    let area = monitor.work_area().size;
    let w = (f64::from(area.width) / scale_factor).floor();
    let h = (f64::from(area.height) / scale_factor).floor();
    if w >= SURFACE_W && h >= SURFACE_H {
        Some((w as u32, h as u32))
    } else {
        None
    }
}

/// Keep the game filling the window: after every resize settles, set the
/// window's inner size to the nearest crisp size.
///
/// # Why a debounce and not a per-event snap
///
/// `WindowEvent::Resized` arrives once per frame while an edge is being
/// dragged, and tao/tauri expose no resize-END event on any platform. Calling
/// `set_size` mid-drag fights the pointer: the window is snapped out from
/// under the edge the user is still holding, and on X11 that produces a
/// visible tug-of-war. So the events only push a timestamp, and the snap
/// happens once, [`RESIZE_SETTLE`] after the last one.
///
/// # Why the work happens on another thread
///
/// This callback runs ON the event loop. `inner_size()`, `scale_factor()` and
/// `is_maximized()` are not local reads — tauri's `window_getter!` posts a
/// message to the event loop and blocks on a channel — so calling one from
/// here would deadlock the process. The callback therefore reads nothing but
/// the event it was handed, and the settle thread (which is not the event
/// loop) does all of it.
fn keep_the_game_filling_the_window(app: &tauri::App) {
    let Some(window) = app.get_webview_window(WINDOW_LABEL) else {
        log::warn!("no window labelled {WINDOW_LABEL} to keep crisp; a resize will letterbox");
        return;
    };
    let watch = Arc::new(ResizeWatch {
        guard: Mutex::new(SnapGuard::default()),
        last_event: Mutex::new(Instant::now()),
        armed: AtomicBool::new(false),
    });
    let target = window.clone();
    window.on_window_event(move |event| {
        let size = match event {
            tauri::WindowEvent::Resized(size) => size,
            // A move to a display with a different device-pixel-ratio changes
            // the LADDER (see this section's table), so the size that was
            // crisp on the old monitor usually is not on the new one.
            tauri::WindowEvent::ScaleFactorChanged { new_inner_size, .. } => new_inner_size,
            _ => return,
        };
        // Minimizing reports 0x0 (and, on some compositors, 1x1) — not a size
        // to snap to, and not a resize the user performed.
        if size.width <= 1 || size.height <= 1 {
            return;
        }
        {
            let mut last = watch.last_event.lock().unwrap_or_else(|p| p.into_inner());
            *last = Instant::now();
        }
        if watch.armed.swap(true, Ordering::SeqCst) {
            return; // a settle thread is already waiting for the drag to end
        }
        let watch = watch.clone();
        let target = target.clone();
        std::thread::spawn(move || {
            loop {
                let waited = {
                    let last = watch.last_event.lock().unwrap_or_else(|p| p.into_inner());
                    last.elapsed()
                };
                match RESIZE_SETTLE.checked_sub(waited) {
                    Some(remaining) if !remaining.is_zero() => std::thread::sleep(remaining),
                    _ => break,
                }
            }
            // Disarmed BEFORE the snap, so an event that arrives while we are
            // in it arms a fresh thread rather than being dropped. The worst
            // case is two threads computing the same target, and `SnapGuard`
            // is behind a mutex, so the second one sees the first one's ask.
            watch.armed.store(false, Ordering::SeqCst);
            snap_once(&target, &watch.guard);
        });
    });
}

/// One settled resize: read the window, choose the nearest crisp size, and
/// ask for it unless [`SnapGuard`] says the ask is a loop.
fn snap_once(window: &tauri::WebviewWindow, guard: &Mutex<SnapGuard>) {
    // MAXIMIZE / FULLSCREEN — the honest decision. A maximized window is the
    // window manager's size, not the user's drag, and `set_size` on one
    // either un-maximizes it (macOS, Windows) or is refused (most Linux
    // WMs) — so snapping here would either undo a deliberate maximize or
    // start the loop `SnapGuard` exists to break. `max_inner_size` already
    // caps how big a maximize can get, `maximizable(false)` removes the
    // gesture entirely where the platform supports it, and there is no
    // maximize button in the page's own title bar. In the case that is left
    // (a keyboard/WM maximize on Linux) the page letterboxes, which is
    // precisely what its letterbox is for. See ../README.md §5.
    if window.is_maximized().unwrap_or(false) || window.is_fullscreen().unwrap_or(false) {
        log::debug!("the window is maximized or fullscreen; leaving its size to the compositor");
        return;
    }
    let physical = match window.inner_size() {
        Ok(size) => size,
        Err(e) => {
            log::warn!("could not read the window's inner size, so not snapping it: {e}");
            return;
        }
    };
    let scale_factor = match window.scale_factor() {
        Ok(scale_factor) if scale_factor > 0.0 => scale_factor,
        Ok(scale_factor) => {
            log::warn!("the window reported a scale factor of {scale_factor}; not snapping");
            return;
        }
        Err(e) => {
            log::warn!("could not read the window's scale factor, so not snapping it: {e}");
            return;
        }
    };
    if physical.width == 0 || physical.height == 0 {
        return; // minimized between the event and here
    }
    let reported = (
        (f64::from(physical.width) / scale_factor).round() as u32,
        (f64::from(physical.height) / scale_factor).round() as u32,
    );
    let bound = window
        .current_monitor()
        .ok()
        .flatten()
        .as_ref()
        .and_then(work_area_bound);
    let target = nearest_crisp_size(reported, scale_factor, bound);
    {
        let mut guard = guard.lock().unwrap_or_else(|p| p.into_inner());
        if !guard.should_ask_for(target, reported) {
            return;
        }
        // The lock is released here on purpose: `set_size` is an event-loop
        // round trip, and holding it across one would serialise a second
        // settle thread behind a blocking call for no reason.
    }
    log::info!(
        "resize settled at {}x{} logical; snapping to {}x{} so the game fills the window",
        reported.0,
        reported.1,
        target.0,
        target.1
    );
    if let Err(e) = window.set_size(tauri::LogicalSize::new(target.0, target.1)) {
        log::warn!(
            "could not resize the window to {}x{}: {e}",
            target.0,
            target.1
        );
    }
}

/// Build dexel's one window, pointed at the runtime's loopback URL and
/// carrying the user's preferences from the first frame.
fn build_window(app: &tauri::App, url: tauri::Url, prefs: Prefs) -> tauri::Result<()> {
    // WINDOW-FIT. The window this builds can only ever be a whole number of
    // art pixels wide: it OPENS on a crisp size, its min/max pin the range to
    // 1x..3x, and `keep_the_game_filling_the_window` puts every hand-dragged
    // size back on the ladder. See that section for the whole argument.
    //
    // The monitor here is the PRIMARY one, because the window does not exist
    // yet and so has no `current_monitor()` to ask. On a multi-head setup
    // whose displays differ, the first size can therefore be one step off for
    // the display the window is actually centred on — which the first resize
    // (or the `ScaleFactorChanged` that a cross-display move fires) corrects.
    // A first frame at a crisp size beats a correct size applied one frame
    // late: the alternative is a visible resize at every launch.
    let monitor = app.primary_monitor().ok().flatten();
    let scale_factor = monitor.as_ref().map_or(1.0, |m| m.scale_factor());
    let bound = monitor.as_ref().and_then(work_area_bound);
    let (open_w, open_h) = opening_size(scale_factor, bound);
    log::info!(
        "opening the window at {open_w}x{open_h} logical px \
         (scale factor {scale_factor}, work area {bound:?})"
    );

    WebviewWindowBuilder::new(app, WINDOW_LABEL, WebviewUrl::External(shell_url(&url)))
        .title(WINDOW_TITLE)
        .inner_size(f64::from(open_w), f64::from(open_h))
        // 1x — the surface itself. The layout is fixed-pixel and does not
        // reflow, so there is nothing below this to give.
        .min_inner_size(SURFACE_W * MIN_SCALE, SURFACE_H * MIN_SCALE)
        // 3x. The OS enforces the top of the range during a drag, which also
        // makes it the cap on how big a window-manager maximize can get.
        .max_inner_size(SURFACE_W * MAX_SCALE, SURFACE_H * MAX_SCALE)
        .resizable(true)
        // No maximize. A maximized window is by definition the display's
        // shape, not 8:5, so it is the one shape this game cannot fill —
        // and there is no maximize button in the page's own title bar to
        // offer it. Unsupported on Linux (tauri 2.11.5 documents
        // `maximizable` as macOS/Windows only), where `max_inner_size` and
        // `snap_once`'s hands-off rule are what remain.
        .maximizable(false)
        // SET-1 (docs/ui-spec.md §11): the USER's choice, applied at creation
        // so the very first frame is already right — never a flash of a
        // pinned window followed by a correction.
        //
        // This used to be a hardcoded `true`, on ADR 0007's reasoning ("a
        // companion the editor buries never gets seen"). That reasoning is
        // still why the capability exists; what changed is who decides. A
        // window that cannot be put behind anything is an obstruction on
        // macOS, and forcing it on every user was the wrong way to act on a
        // good idea — so the behaviour is kept, the DEFAULT is off, and
        // Settings owns the switch.
        .always_on_top(prefs.always_on_top)
        // WINDOW-POLISH (docs/plan/ROADMAP.md): FRAMELESS. The game already
        // draws its own 640x24 titlebar, and a native frame stacked on top of
        // it was two title bars in a 460px-tall window — the thing the
        // original floating-companion vision never had.
        //
        // Going frameless is not free, and the cost is paid in the page:
        //
        //   * There is no system close or minimize button any more, so the
        //     page MUST provide both, or the window cannot be dismissed from
        //     inside. app/frontend/src/features/shell-window.ts does, gated on
        //     the `?shell=1` this shell appends (see SHELL_QUERY) so a browser
        //     tab — which has its own — never grows a pair.
        //   * There is nothing to drag the window by, so `#titlebar` carries
        //     `data-tauri-drag-region="deep"` (app/public/index.html).
        //
        // Both of those need the page to reach Tauri's IPC, and the page is a
        // REMOTE origin (`WebviewUrl::External` on http://127.0.0.1:<port>),
        // not bundled `tauri://` content. What makes it work — verified
        // against the tauri 2.11.5 source, because the docs do not say it
        // plainly — is that `manager/webview.rs::prepare_pending_webview`
        // pushes `__TAURI_INTERNALS__`, the invoke script, every plugin init
        // script (including the core window plugin's drag.js, which is what
        // implements the drag-region attribute) and — when
        // `app.withGlobalTauri` is true — the global API bundle, ALL with no
        // local-vs-remote condition. The gate is purely the ACL, and a remote
        // origin matches only a capability that declares `remote.urls`; hence
        // capabilities/loopback-window-controls.json. `core:window:default`
        // grants read-only getters and would not have been enough even for a
        // local page.
        //
        // Resizing is the remaining honest caveat, and it is
        // platform-dependent: macOS and Windows still give an undecorated
        // window draggable resize edges, while on Linux it is up to the
        // compositor and an undecorated GTK window may offer none. See
        // ../README.md "The frameless shell" for what still needs a human's
        // eyes on each platform.
        .decorations(false)
        .center()
        .build()
        .map(|_| ())
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        // Default targets are exactly [Stdout, LogDir { file_name: None }],
        // so CLI output reaches both a terminal (dev) and a log file
        // (packaged) with no further configuration.
        .plugin(
            tauri_plugin_log::Builder::new()
                .level(log::LevelFilter::Info)
                .build(),
        )
        .setup(|app| {
            let handle = app.handle().clone();
            // `?` converts String into Box<dyn Error> for us. A failure here
            // aborts startup loudly instead of showing an empty window.
            let (url, prefs) = runtime_url(&handle)?;
            let url = tauri::Url::parse(&url)
                .map_err(|e| format!("runtime reported an unparseable URL {url:?}: {e}"))?;

            // No managed child, no guard: there is nothing for this shell to
            // own or terminate. If the window fails to build we simply fail —
            // the runtime is not ours to clean up, and killing it would be
            // exactly the bug this shell was rewritten to remove.
            build_window(app, url.clone(), prefs)?;
            keep_the_game_filling_the_window(app);
            watch_for_a_moved_runtime(app, url, prefs);
            Ok(())
        })
        .build(tauri::generate_context!())
        .expect("error while building the dexel desktop shell")
        .run(|_handle, event| {
            // Deliberately empty of termination logic. Closing the window
            // quits this shell and NOTHING else: the runtime keeps observing
            // activity, keeps its sprint ticking and keeps autosaving. See
            // the module docs — this is the contract, not an oversight.
            if matches!(event, RunEvent::Exit) {
                log::info!(
                    "window closed; the dexel runtime keeps running (use `dexel stop` to stop it)"
                );
            }
        });
}

// ---------------------------------------------------------------------------
// SET-1 — the `prefs` block this shell reads out of `dexel status --json`.
// ---------------------------------------------------------------------------

/// `prefs_from_status` is a pure function over the JSON `app/cmd_lifecycle.go`
/// prints, so it is tested against literal status payloads rather than only
/// exercised by launching a window. The Go side pins the same strings from its
/// end (`app/prefs_wire_test.go`); these are the two halves of one contract
/// that crosses a process boundary, and neither language can check the other.
#[cfg(test)]
mod prefs_parsing {
    use super::{prefs_from_status, Prefs};

    fn parse(raw: &str) -> Prefs {
        prefs_from_status(&serde_json::from_str(raw).expect("test payload is not valid JSON"))
    }

    #[test]
    fn reads_always_on_top_from_a_real_status_payload() {
        let got = parse(
            r#"{"running":true,"pid":42,"url":"http://127.0.0.1:8080",
                "version":"v","prefs":{"alwaysOnTop":true,"showAwayTime":false}}"#,
        );
        assert!(got.always_on_top);
    }

    #[test]
    fn a_false_pref_is_off() {
        let got = parse(r#"{"running":true,"prefs":{"alwaysOnTop":false,"showAwayTime":true}}"#);
        assert!(!got.always_on_top);
    }

    /// The default matters more than it looks: it is what a user who never
    /// opened Settings gets, and SET-1 exists precisely because that used to
    /// be a forced-on-top window.
    #[test]
    fn every_way_of_saying_nothing_means_off() {
        for raw in [
            r#"{"running":true}"#,                               // pre-SET-1 dexel
            r#"{"running":true,"prefs":{}}"#,                    // block, no keys
            r#"{"running":true,"prefs":{"alwaysOnTop":null}}"#,  // explicit null
            r#"{"running":true,"prefs":{"alwaysOnTop":"yes"}}"#, // wrong type
            r#"{"running":true,"prefs":"nonsense"}"#,            // block is not an object
            r#"{"running":false,"reason":"no runtime.json"}"#,   // nothing running
        ] {
            assert_eq!(parse(raw), Prefs::default(), "payload: {raw}");
        }
    }

    /// A preference is config, not a property of a live process — so it is
    /// read on the not-running branch too, which is what lets the window
    /// honour a setting made while the daemon was stopped.
    #[test]
    fn prefs_are_read_even_when_nothing_is_running() {
        let got =
            parse(r#"{"running":false,"reason":"no runtime.json","prefs":{"alwaysOnTop":true}}"#);
        assert!(got.always_on_top);
    }
}

// ---------------------------------------------------------------------------
// The bundle-layout guard.
// ---------------------------------------------------------------------------

/// Guards the ONE invariant that governs how this bundle's executables are
/// named, and that nothing else in the toolchain checks.
///
/// # The invariant
///
/// **No two executables destined for one flat install directory may collide
/// case-insensitively.**
///
/// The names in play are `tauri.conf.json`'s `mainBinaryName` (this crate's
/// GUI shell) and every entry of `bundle.externalBin` (today just the Go
/// daemon). On macOS all of them land in `Dexel.app/Contents/MacOS/`; in the
/// `.deb` all of them land in `/usr/bin`; in the NSIS installer all of them
/// land in the install directory. Two of those three filesystems are
/// case-insensitive in their default configuration, and Windows always is.
///
/// # Why a test, rather than trusting a build error
///
/// **There is no build error.** Verified in the vendored bundler
/// (`tauri-bundler-2.9.4`):
///
/// * `src/bundle/macos/app.rs`'s `bundle_project` calls
///   `settings.copy_binaries(&bin_dir)` — the `externalBin` entries — and
///   THEN `copy_binaries_to_bundle(..)` — the main binary.
/// * `src/bundle/linux/debian.rs`'s `generate_data` does it in the OPPOSITE
///   order: the main binary first, `copy_binaries` second.
/// * Both ultimately call `utils::fs_utils::copy_file`, whose body is
///   `fs::create_dir_all(dest_dir)?; fs::copy(from, to)?` — an overwrite.
///   `fs::copy` truncates an existing destination and returns `Ok`.
///
/// So a colliding pair produces a bundle that is missing one of its two
/// programs, with a zero exit code and no warning, and *which* program
/// survives depends on the target. On macOS the survivor would be the GUI
/// shell, which means the login item would open a WINDOW at every login
/// instead of starting the runtime — the exact bug this guard exists to make
/// impossible. PLATFORM_NOTES.md §3.1.3 has the field account.
///
/// The check is split in two on purpose: `case_insensitive_collision` is a
/// pure function with its own test that feeds it a KNOWN-BAD list, so the
/// checker itself is proven to catch a collision rather than being trusted
/// to. The other test then points it at this crate's real config.
#[cfg(test)]
mod bundle_layout {
    use super::SIDECAR_NAME;

    /// The config is read at COMPILE time, so this test cannot pass against
    /// a stale copy of a file that was edited after the build.
    const TAURI_CONF: &str = include_str!("../tauri.conf.json");
    const CAPABILITY: &str = include_str!("../capabilities/default.json");

    fn conf() -> serde_json::Value {
        serde_json::from_str(TAURI_CONF).expect("tauri.conf.json is not valid JSON")
    }

    /// Every filename this config will place in one flat install directory:
    /// the main binary plus each `externalBin`, with Tauri's `binaries/`
    /// source-directory prefix stripped the way the bundler strips it (only
    /// the final component becomes a filename).
    fn flat_install_names(conf: &serde_json::Value) -> Vec<String> {
        // Absent `mainBinaryName` would silently fall back to the Cargo
        // package name, which would make the whole arrangement implicit
        // again. It is required to be stated.
        let main = conf
            .get("mainBinaryName")
            .and_then(|v| v.as_str())
            .expect("tauri.conf.json must state mainBinaryName explicitly");
        let mut names = vec![main.to_string()];

        let external = conf
            .get("bundle")
            .and_then(|b| b.get("externalBin"))
            .and_then(|e| e.as_array())
            .expect("tauri.conf.json must have bundle.externalBin");
        for entry in external {
            let entry = entry.as_str().expect("externalBin entries are strings");
            let base = entry.rsplit('/').next().unwrap_or(entry);
            names.push(base.to_string());
        }
        names
    }

    /// The first case-insensitively colliding pair, in the order given, or
    /// `None`. Deliberately returns the offending pair rather than a bool so
    /// a failure message can name both halves.
    fn case_insensitive_collision(names: &[String]) -> Option<(String, String)> {
        for (i, a) in names.iter().enumerate() {
            for b in &names[i + 1..] {
                if a.to_lowercase() == b.to_lowercase() {
                    return Some((a.clone(), b.clone()));
                }
            }
        }
        None
    }

    /// The checker itself, against a list that is known to collide. Without
    /// this, a `case_insensitive_collision` that always returned `None`
    /// would make the real test below pass while asserting nothing.
    #[test]
    fn the_collision_check_catches_a_known_collision() {
        let colliding = vec!["dexel".to_string(), "Dexel".to_string()];
        let (a, b) = case_insensitive_collision(&colliding)
            .expect("case_insensitive_collision missed `dexel` vs `Dexel` — the exact pair that broke this bundle before, so this checker is not checking anything");
        assert_eq!((a.as_str(), b.as_str()), ("dexel", "Dexel"));

        // A space and a hyphen are ordinary characters here: only case is
        // folded. `Dexel` and `Dexel Runtime` must NOT read as a collision,
        // or the guard would reject a perfectly valid pair of names.
        assert!(
            case_insensitive_collision(&[
                "dexel-desktop".to_string(),
                "Dexel".to_string(),
                "Dexel Runtime".to_string(),
            ])
            .is_none(),
            "distinct names were reported as colliding"
        );
    }

    /// The real config. This is the assertion that would have failed on the
    /// `mainBinaryName: "dexel"` + `externalBin: "binaries/Dexel"` pair that
    /// tauri-bundler would otherwise have collapsed into one file.
    #[test]
    fn no_two_installed_executables_collide_case_insensitively() {
        let names = flat_install_names(&conf());
        assert!(
            names.len() >= 2,
            "expected at least the main binary and one externalBin, got {names:?}"
        );
        if let Some((a, b)) = case_insensitive_collision(&names) {
            panic!(
                "tauri.conf.json names two executables that collide case-insensitively: {a:?} and {b:?}.\n\
                 Both land in Dexel.app/Contents/MacOS/ (and in /usr/bin for the .deb), and macOS's \
                 default APFS volume is case-INsensitive, so they are ONE FILE. tauri-bundler copies \
                 them with a plain fs::copy and does NOT error — one program silently overwrites the \
                 other, and which one survives differs between the macOS and .deb code paths. Rename \
                 one of them; do not delete this test. See ../Cargo.toml's [package] header and \
                 PLATFORM_NOTES.md §3.1.3.\n\
                 full list: {names:?}"
            );
        }
    }

    /// `SIDECAR_NAME`, `bundle.externalBin` and the `shell:allow-execute`
    /// scope entry are three copies of one string. They have drifted before
    /// — the ACL is the quiet one, because a stale entry only shows up as a
    /// permission denial in a packaged build — so the lockstep is asserted
    /// rather than maintained by hand.
    #[test]
    fn sidecar_name_matches_the_config_and_the_acl() {
        let names = flat_install_names(&conf());
        assert!(
            names[1..].iter().any(|n| n == SIDECAR_NAME),
            "SIDECAR_NAME is {SIDECAR_NAME:?} but bundle.externalBin holds {:?}; \
             app.shell().sidecar(SIDECAR_NAME) resolves the on-disk name, so a mismatch \
             is a runtime 'sidecar not found', not a build error",
            &names[1..]
        );

        let acl: serde_json::Value =
            serde_json::from_str(CAPABILITY).expect("capabilities/default.json is not valid JSON");
        let wanted = format!("binaries/{SIDECAR_NAME}");
        let found = acl["permissions"]
            .as_array()
            .expect("permissions is an array")
            .iter()
            .filter_map(|p| p.get("allow"))
            .filter_map(|a| a.as_array())
            .flatten()
            .filter_map(|e| e.get("name"))
            .filter_map(|n| n.as_str())
            .any(|n| n == wanted);
        assert!(
            found,
            "capabilities/default.json's shell:allow-execute scope does not allow {wanted:?}; \
             the shell's bundled-driver fallback would be denied at runtime in a packaged build"
        );
    }
}

// ---------------------------------------------------------------------------
// WINDOW-POLISH — the frameless shell's four-file contract.
// ---------------------------------------------------------------------------

/// Going frameless made four files depend on each other, three of them not
/// Rust, and every one of them fails SILENTLY when it drifts:
///
///   1. `build_window`'s `decorations(false)` — no native close/minimize/drag.
///   2. `SHELL_QUERY` on the loaded URL — how the page learns (1) happened.
///   3. `tauri.conf.json`'s `app.withGlobalTauri` — whether `window.__TAURI__`
///      exists for `features/shell-window.ts` to call.
///   4. `capabilities/loopback-window-controls.json` — whether the ACL lets a
///      REMOTE origin call those commands at all.
///
/// Break 3 or 4 and you get a frameless window whose close button does
/// nothing: no build error, no test failure, and the only symptom is a window
/// the user cannot dismiss. The page complains loudly when it happens
/// (shell-window.ts's honest-failure path), but "loud at runtime" is a poor
/// substitute for "caught at build time", so the config side is pinned here.
///
/// What this canNOT check is whether the URL PATTERN actually matches the
/// running port. That is decided by the `urlpattern` crate at runtime against
/// a port nobody knows until the daemon binds it — but a malformed pattern
/// panics during `generate_context!`, so a build that links has already proven
/// the patterns parse. The port-wildcard behaviour itself is asserted
/// separately in `shell_url_and_patterns`.
#[cfg(test)]
mod shell_mode {
    use super::{shell_url, SHELL_QUERY};

    const TAURI_CONF: &str = include_str!("../tauri.conf.json");
    const DEFAULT_CAP: &str = include_str!("../capabilities/default.json");
    const LOOPBACK_CAP: &str = include_str!("../capabilities/loopback-window-controls.json");

    fn json(raw: &str, what: &str) -> serde_json::Value {
        serde_json::from_str(raw).unwrap_or_else(|e| panic!("{what} is not valid JSON: {e}"))
    }

    /// `window.__TAURI__` is injected only when this is true (tauri 2.11.5:
    /// `tauri-codegen`'s `plugin_global_api_scripts` is `None` otherwise), and
    /// `features/shell-window.ts` calls
    /// `window.__TAURI__.window.getCurrentWindow()`. Flipping this back to
    /// false leaves a frameless window with two dead buttons.
    #[test]
    fn global_tauri_is_enabled_for_the_pages_window_api() {
        let conf = json(TAURI_CONF, "tauri.conf.json");
        assert_eq!(
            conf["app"]["withGlobalTauri"].as_bool(),
            Some(true),
            "app.withGlobalTauri must be true: the frameless window's close and minimize \
             buttons live in the PAGE (app/frontend/src/features/shell-window.ts) and reach \
             the window through window.__TAURI__, which is not injected without it"
        );
    }

    /// The loopback capability, field by field. Each assertion here is a
    /// distinct way to end up with a silently unclosable window.
    #[test]
    fn the_loopback_capability_grants_exactly_the_three_window_verbs() {
        let cap = json(LOOPBACK_CAP, "capabilities/loopback-window-controls.json");

        // A remote origin matches ONLY a capability with `remote.urls`
        // (tauri's `Origin::matches`: Remote never matches ExecutionContext::
        // Local). Without this block the page is granted nothing at all.
        let urls: Vec<&str> = cap["remote"]["urls"]
            .as_array()
            .expect("the capability must declare remote.urls — the page is a REMOTE http:// origin, and core:default only ever applies to local app URLs")
            .iter()
            .map(|u| u.as_str().expect("remote.urls entries are strings"))
            .collect();
        // The daemon binds an EPHEMERAL port in runtime mode (`-addr
        // 127.0.0.1:0`), so a pattern without a port wildcard would match only
        // port 80 and nothing real. urlpattern does NOT widen the port the way
        // tauri widens path/query/hash.
        assert!(
            urls.contains(&"http://127.0.0.1:*"),
            "remote.urls must include \"http://127.0.0.1:*\" — the runtime's port is ephemeral, \
             and a pattern with a fixed or absent port matches nothing it will ever bind; got {urls:?}"
        );
        // `localhost` and `127.0.0.1` are different hostnames to urlpattern
        // with no cross-matching, and which spelling `status --json` reports
        // is the daemon's business, not this shell's.
        assert!(
            urls.contains(&"http://localhost:*"),
            "remote.urls must also include \"http://localhost:*\": urlpattern treats it as a \
             different host from 127.0.0.1, and the URL the daemon reports may use either \
             spelling; got {urls:?}"
        );

        // `windows` defaults to an EMPTY vec, and tauri's resolve_access
        // requires `windows.iter().any(...)` — an empty list matches nothing,
        // so omitting this silently grants nothing.
        let windows: Vec<&str> = cap["windows"]
            .as_array()
            .expect("the capability must list `windows` — it defaults to empty, and an empty list matches NO window")
            .iter()
            .map(|w| w.as_str().expect("windows entries are strings"))
            .collect();
        assert!(
            windows.contains(&super::WINDOW_LABEL),
            "the capability's `windows` must include this shell's only window label {:?}; got {windows:?}",
            super::WINDOW_LABEL
        );

        // `local` defaults to TRUE, which would also grant these three verbs
        // to any future bundled/tauri:// page. Nothing needs that, and a
        // capability should grant the smallest thing that works.
        assert_eq!(
            cap["local"].as_bool(),
            Some(false),
            "the capability should set \"local\": false — it exists for the loopback page only, \
             and `local` defaults to true, which would silently widen it"
        );

        let perms: Vec<&str> = cap["permissions"]
            .as_array()
            .expect("permissions is an array")
            .iter()
            .map(|p| {
                p.as_str()
                    .expect("these permissions are plain identifier strings")
            })
            .collect();
        // start-dragging is what index.html's data-tauri-drag-region needs
        // (tauri's drag.js invokes `plugin:window|start_dragging`); close and
        // minimize are what shell-window.ts's two buttons need. None of the
        // three is in core:window:default, which is read-only getters plus
        // internal-toggle-maximize.
        for wanted in [
            "core:window:allow-start-dragging",
            "core:window:allow-close",
            "core:window:allow-minimize",
        ] {
            assert!(
                perms.contains(&wanted),
                "the capability must grant {wanted:?} — it is NOT in core:window:default, so \
                 without it the page's call is denied with \"not allowed on origin [remote: ...]\"; \
                 got {perms:?}"
            );
        }
    }

    /// The OTHER half of scoping this correctly: the pre-existing capability
    /// must stay local-only. It carries `shell:allow-execute` for the bundled
    /// Go daemon, and adding a `remote` block to it — the obvious shortcut
    /// instead of writing a second file — would hand the loopback page the
    /// ability to spawn that binary with arbitrary arguments.
    #[test]
    fn the_sidecar_capability_stays_local_only() {
        let cap = json(DEFAULT_CAP, "capabilities/default.json");
        assert!(
            cap.get("remote").is_none(),
            "capabilities/default.json must NOT declare `remote`: it grants shell:allow-execute \
             on the bundled Go daemon, and a remote block would expose that to the loopback page. \
             Window verbs belong in capabilities/loopback-window-controls.json."
        );
    }

    /// `shell_url` is the whole of the shell-mode signal, and it has to be
    /// exact: the page reads `?shell=1` and nothing else, and the port/host/
    /// path must survive untouched or the window loads the wrong server.
    #[test]
    fn shell_url_and_patterns() {
        let bare = tauri::Url::parse("http://127.0.0.1:53421/").expect("test URL");
        let shell = shell_url(&bare);

        assert_eq!(shell.query(), Some(SHELL_QUERY));
        assert_eq!(shell.as_str(), "http://127.0.0.1:53421/?shell=1");
        // Everything the window depends on for reaching the right daemon is
        // preserved. Same-origin matters too: the frontend derives its
        // WebSocket URL from location.host and the server checks the Origin
        // header, and a query string changes neither.
        assert_eq!(shell.host_str(), bare.host_str());
        assert_eq!(shell.port(), bare.port());
        assert_eq!(shell.path(), bare.path());
        assert_eq!(shell.origin(), bare.origin());

        // Idempotent: `re_resolve` navigates to shell_url(bare) but remembers
        // `bare`, so this should never be reached twice in production — but if
        // the remembered value ever became the decorated one, doubling the
        // query would be the bug, and it does not happen.
        assert_eq!(shell_url(&shell).as_str(), shell.as_str());

        // The bare URL is NOT mutated: re_resolve's "has the runtime moved?"
        // comparison is bare-vs-bare, and a shell_url that mutated its input
        // would make every window focus look like a moved runtime and
        // re-navigate the page endlessly.
        assert_eq!(bare.as_str(), "http://127.0.0.1:53421/");
    }
}

// ---------------------------------------------------------------------------
// WINDOW-FIT — the snap arithmetic.
// ---------------------------------------------------------------------------

/// The whole of the sizing decision is pure arithmetic over logical pixels,
/// which is deliberate: this project's Linux box cannot display a Tauri window
/// at all (../README.md §5), so "does a resize land on a crisp size" has to be
/// answerable without one. These tests are that answer. What they cannot check
/// — how a drag FEELS, and whether a compositor honours the size hints — is
/// listed in ../README.md §5 for an owner with a real screen.
#[cfg(test)]
mod window_sizing {
    use super::{
        crisp_sizes, crisp_sizes_within, crisp_step, is_own_echo, nearest_crisp_size, opening_size,
        Logical, SnapGuard, DEFAULT_MAX_SCALE, MAX_SCALE, MAX_UNANSWERED, MIN_SCALE, SURFACE_H,
        SURFACE_W,
    };

    /// The page's own scale rule, transcribed from `chooseScale` in
    /// `app/frontend/src/render/viewport.ts`. Every size this shell hands the
    /// window is fed through it below, because "the game fills the window" is
    /// a claim about what the PAGE does with the size, not about the size.
    fn page_scale(vw: f64, vh: f64, step: f64) -> f64 {
        let exact = (vw / 640.0).min(vh / 400.0);
        // The TS reads `if (!(exact > 0))` — a NaN/zero guard for a viewport
        // of zero size. Spelled out here because clippy (rightly) objects to
        // negating a partial-order comparison.
        if exact.is_nan() || exact <= 0.0 {
            return 1.0;
        }
        if exact < 1.0 {
            return exact;
        }
        let capped = exact.min(3.0);
        let snapped = (capped / step + 1e-9).floor() * step;
        snapped.max(1.0)
    }

    /// Every device-pixel-ratio worth reasoning about: an ordinary monitor, a
    /// retina display, a 3x display, and Windows at 125% (where no crisp
    /// increment exists at all).
    const RATIOS: [f64; 4] = [1.0, 2.0, 3.0, 1.25];

    // -- the ladder ---------------------------------------------------------

    #[test]
    fn the_crisp_step_mirrors_the_pages_rule() {
        assert_eq!(crisp_step(1.0), 1.0);
        assert_eq!(crisp_step(2.0), 0.5);
        assert_eq!(crisp_step(3.0), 1.0 / 3.0);
        // Fractional: there is no increment that lands art pixels on whole
        // device pixels, so whole CSS pixels are the best available.
        assert_eq!(crisp_step(1.25), 1.0);
        assert_eq!(crisp_step(1.5), 1.0);
        // Below 1 is not a ratio any display reports, but a zero or negative
        // one would make 1/dpr nonsense, so it falls back too.
        assert_eq!(crisp_step(0.5), 1.0);
        assert_eq!(crisp_step(0.0), 1.0);
    }

    #[test]
    fn a_dpr_1_display_gets_the_integer_ladder() {
        assert_eq!(
            crisp_sizes(1.0, MAX_SCALE),
            vec![(640, 400), (1280, 800), (1920, 1200)]
        );
    }

    #[test]
    fn a_retina_display_gets_the_half_steps_too() {
        assert_eq!(
            crisp_sizes(2.0, MAX_SCALE),
            vec![
                (640, 400),
                (960, 600),
                (1280, 800),
                (1600, 1000),
                (1920, 1200)
            ]
        );
    }

    /// The integer filter. A 1/3 step would put 1.333x at 853.33 logical px —
    /// not a size a window can have, and a window rounded to 853 is a third of
    /// a logical pixel (one device pixel) short of 1.333x, which the page's
    /// `Math.floor` reads as **1x**. So those steps are dropped and a dpr-3
    /// display gets whole scales.
    #[test]
    fn a_dpr_3_display_drops_the_steps_that_are_not_whole_pixels() {
        assert_eq!(
            crisp_sizes(3.0, MAX_SCALE),
            vec![(640, 400), (1280, 800), (1920, 1200)]
        );
    }

    #[test]
    fn a_fractional_dpr_gets_the_integer_ladder() {
        assert_eq!(crisp_sizes(1.25, MAX_SCALE), crisp_sizes(1.0, MAX_SCALE));
    }

    /// The cap is inclusive at both ends of a floating-point accumulation:
    /// 1 + 2 * 0.5 can land a hair above 2.0, and a bare `>` would drop 2x
    /// from a dpr-2 ladder capped at 2x.
    #[test]
    fn the_cap_keeps_the_step_that_lands_exactly_on_it() {
        assert_eq!(
            crisp_sizes(2.0, DEFAULT_MAX_SCALE),
            vec![(640, 400), (960, 600), (1280, 800)]
        );
        assert_eq!(*crisp_sizes(1.0, MAX_SCALE).last().unwrap(), (1920, 1200));
        assert_eq!(
            *crisp_sizes(3.0, DEFAULT_MAX_SCALE).last().unwrap(),
            (1280, 800)
        );
    }

    /// The property that makes a size "crisp" at all: one art pixel covers a
    /// whole number of DEVICE pixels, and the same whole number on both axes.
    ///
    /// A fractional device-pixel-ratio is excluded, and that exclusion is the
    /// honest statement of what a 125% display costs: at dpr 1.25 an art pixel
    /// is 1.25 device px wide at 1x and no window size can change that, so the
    /// ladder falls back to whole CSS pixels (asserted separately) and this
    /// property is simply unavailable.
    #[test]
    fn every_ladder_size_is_a_whole_number_of_device_pixels_per_art_pixel() {
        for dpr in RATIOS {
            for (w, h) in crisp_sizes(dpr, MAX_SCALE) {
                // 8:5, exactly, on every rung of every ladder.
                assert_eq!(u64::from(w) * 5, u64::from(h) * 8, "{w}x{h} is not 8:5");
                if !super::is_whole(dpr) {
                    continue;
                }
                let across = f64::from(w) * dpr / SURFACE_W;
                let down = f64::from(h) * dpr / SURFACE_H;
                assert!(
                    (across - across.round()).abs() < 1e-9,
                    "{w}x{h} at dpr {dpr}: {across} device px per art px across"
                );
                assert_eq!(
                    across.round(),
                    down.round(),
                    "{w}x{h} at dpr {dpr}: art pixels are not square"
                );
            }
        }
    }

    /// The claim this whole section exists to make, checked against the page's
    /// own arithmetic: at every size this shell can produce, the page fills
    /// the window exactly and its letterbox is 0px on both axes.
    #[test]
    fn at_every_ladder_size_the_page_fills_the_window_exactly() {
        for dpr in RATIOS {
            let step = crisp_step(dpr);
            for (w, h) in crisp_sizes(dpr, MAX_SCALE) {
                let scale = page_scale(f64::from(w), f64::from(h), step);
                let ox = (f64::from(w) - SURFACE_W * scale) / 2.0;
                let oy = (f64::from(h) - SURFACE_H * scale) / 2.0;
                assert_eq!(
                    (ox.round(), oy.round()),
                    (0.0, 0.0),
                    "at dpr {dpr} a {w}x{h} window renders at {scale}x, leaving a \
                     ({ox}, {oy}) letterbox — the game would not touch its own window edges"
                );
            }
        }
    }

    /// The bug, stated as a test: the size this window used to open at is the
    /// one the owner screenshotted, and it letterboxes.
    #[test]
    fn the_old_660x460_default_is_exactly_the_reported_letterbox() {
        let scale = page_scale(660.0, 460.0, crisp_step(1.0));
        assert_eq!(scale, 1.0);
        assert_eq!((660.0 - SURFACE_W * scale) / 2.0, 10.0); // 10px pillarbox
        assert_eq!((460.0 - SURFACE_H * scale) / 2.0, 30.0); // 30px letterbox
                                                             // ...and it is not a size this shell can produce any more.
        assert!(!crisp_sizes(1.0, MAX_SCALE).contains(&(660, 460)));
    }

    // -- nearest-step selection --------------------------------------------

    #[test]
    fn a_size_already_on_the_ladder_snaps_to_itself() {
        for dpr in RATIOS {
            for size in crisp_sizes(dpr, MAX_SCALE) {
                assert_eq!(nearest_crisp_size(size, dpr, None), size, "dpr {dpr}");
            }
        }
    }

    #[test]
    fn the_old_default_snaps_down_to_1x() {
        assert_eq!(nearest_crisp_size((660, 460), 1.0, None), (640, 400));
    }

    /// Nearest, not "largest that fits": a drag most of the way to 2x lands on
    /// 2x rather than collapsing back to 1x.
    #[test]
    fn a_drag_past_the_halfway_point_snaps_up() {
        assert_eq!(nearest_crisp_size((970, 610), 1.0, None), (1280, 800));
        assert_eq!(nearest_crisp_size((1200, 740), 1.0, None), (1280, 800));
    }

    #[test]
    fn a_drag_short_of_the_halfway_point_snaps_down() {
        assert_eq!(nearest_crisp_size((700, 470), 1.0, None), (640, 400));
        assert_eq!(nearest_crisp_size((900, 560), 1.0, None), (640, 400));
    }

    /// Dead centre between two rungs. The ladder is ascending and the
    /// comparison is strict, so the SMALLER size wins — the direction that can
    /// never push the window off the screen.
    #[test]
    fn an_exact_tie_goes_to_the_smaller_size() {
        assert_eq!(nearest_crisp_size((960, 600), 1.0, None), (640, 400));
        // On a retina display 960x600 is not a tie at all: it is a rung.
        assert_eq!(nearest_crisp_size((960, 600), 2.0, None), (960, 600));
    }

    /// The same drag resolves differently per display, because the ladder is
    /// the display's. Not a wart — it is the point of deriving it.
    #[test]
    fn the_same_size_resolves_per_display() {
        assert_eq!(nearest_crisp_size((1000, 620), 1.0, None), (1280, 800));
        assert_eq!(nearest_crisp_size((1000, 620), 2.0, None), (960, 600));
    }

    /// Nearest in PIXEL space, so how far each axis was dragged is what
    /// decides — and 8:5 is not negotiable, so a one-axis drag resolves to a
    /// rung that moves BOTH axes. Both halves of that are deliberate:
    ///
    ///   * a modest pull on one edge holds the current rung rather than
    ///     doubling the other axis behind the user's back;
    ///   * a big one, though, is a request for a bigger window, and the only
    ///     bigger windows this game has are 8:5. Pulling the right edge out to
    ///     1900 asks for a scale the height has to follow to.
    ///
    /// `bound` is what keeps that second case on the screen — see
    /// `the_work_area_bound_stops_the_snap_growing_off_the_screen`.
    #[test]
    fn a_one_axis_drag_resolves_by_how_far_it_went() {
        assert_eq!(nearest_crisp_size((640, 790), 1.0, None), (640, 400));
        assert_eq!(nearest_crisp_size((1900, 800), 1.0, None), (1920, 1200));
        assert_eq!(
            nearest_crisp_size((1900, 800), 1.0, Some((1920, 1040))),
            (1280, 800)
        );
    }

    // -- min / max clamping -------------------------------------------------

    /// The min/max are enforced by the OS on the window itself, so these sizes
    /// should never reach the snap — but a compositor that ignores size hints
    /// is exactly the case the snap is the backstop for.
    #[test]
    fn a_size_below_1x_clamps_up_and_a_size_above_3x_clamps_down() {
        assert_eq!(nearest_crisp_size((320, 200), 1.0, None), (640, 400));
        assert_eq!(nearest_crisp_size((1, 1), 2.0, None), (640, 400));
        assert_eq!(nearest_crisp_size((3840, 2400), 1.0, None), (1920, 1200));
        assert_eq!(nearest_crisp_size((2000, 1300), 2.0, None), (1920, 1200));
    }

    #[test]
    fn the_range_is_the_pages_range() {
        assert_eq!(
            (SURFACE_W * MIN_SCALE, SURFACE_H * MIN_SCALE),
            (640.0, 400.0)
        );
        assert_eq!(
            (SURFACE_W * MAX_SCALE, SURFACE_H * MAX_SCALE),
            (1920.0, 1200.0)
        );
    }

    // -- the monitor bound --------------------------------------------------

    /// A bound keeps the snap on the screen: on a 1080p display 1920x1200 is
    /// nearest to a full-height drag, and snapping there would make the window
    /// taller than the monitor it is on.
    #[test]
    fn the_work_area_bound_stops_the_snap_growing_off_the_screen() {
        let full_height_drag: Logical = (1900, 1040);
        assert_eq!(
            nearest_crisp_size(full_height_drag, 1.0, None),
            (1920, 1200)
        );
        assert_eq!(
            nearest_crisp_size(full_height_drag, 1.0, Some((1920, 1040))),
            (1280, 800)
        );
    }

    /// A display too small for even 1x keeps 1x anyway: it is the window's
    /// `min_inner_size`, and "the smallest window this game has" is a better
    /// answer than no size at all.
    #[test]
    fn a_bound_smaller_than_the_surface_still_leaves_1x() {
        assert_eq!(
            crisp_sizes_within(1.0, MAX_SCALE, Some((500, 300))),
            vec![(640, 400)]
        );
        assert_eq!(
            nearest_crisp_size((500, 300), 1.0, Some((500, 300))),
            (640, 400)
        );
    }

    // -- the opening size ---------------------------------------------------

    #[test]
    fn a_fresh_window_opens_at_2x_on_a_display_that_fits_it() {
        // 1080p, minus a taskbar.
        assert_eq!(opening_size(1.0, Some((1920, 1040))), (1280, 800));
        // A 4K display could fit 3x, but a first launch that fills the screen
        // is not what this product is: the default is capped at 2x.
        assert_eq!(opening_size(1.0, Some((3840, 2100))), (1280, 800));
        // The same 4K panel at dpr 2 is a 1920x1080 logical desktop.
        assert_eq!(opening_size(2.0, Some((1920, 1040))), (1280, 800));
    }

    #[test]
    fn a_small_display_opens_smaller() {
        // A 1366x768 laptop cannot fit 1280x800 (768 < 800), so 1x it is.
        assert_eq!(opening_size(1.0, Some((1366, 728))), (640, 400));
        // A retina display of the same logical size has a rung in between.
        assert_eq!(opening_size(2.0, Some((1366, 728))), (960, 600));
    }

    /// With no readable work area the cap is the only bound — never larger
    /// than the default cap, so an unknown display cannot produce a 3x window.
    #[test]
    fn an_unknown_display_opens_at_the_default_cap() {
        assert_eq!(opening_size(1.0, None), (1280, 800));
        assert_eq!(opening_size(2.0, None), (1280, 800));
        assert_eq!(opening_size(1.25, None), (1280, 800));
    }

    #[test]
    fn every_opening_size_is_on_the_ladder() {
        for dpr in RATIOS {
            for bound in [
                None,
                Some((1920, 1040)),
                Some((1366, 728)),
                Some((3840, 2100)),
                Some((500, 300)),
            ] {
                let size = opening_size(dpr, bound);
                assert!(
                    crisp_sizes(dpr, MAX_SCALE).contains(&size),
                    "dpr {dpr}, bound {bound:?} opened at {size:?}, which is not crisp"
                );
            }
        }
    }

    // -- the loop guard -----------------------------------------------------

    #[test]
    fn an_echo_is_recognised_within_a_pixel_and_not_beyond_it() {
        assert!(is_own_echo((1280, 800), Some((1280, 800))));
        assert!(is_own_echo((1281, 799), Some((1280, 800))));
        assert!(!is_own_echo((1282, 800), Some((1280, 800))));
        assert!(!is_own_echo((1280, 802), Some((1280, 800))));
        // Nothing has been asked for yet, so nothing can be an echo.
        assert!(!is_own_echo((1280, 800), None));
    }

    /// Loop 1: our own `set_size` comes back as a resize event. The snap must
    /// not ask again — and a size that already IS the target needs no ask
    /// however it got there (the user can drag onto a rung by hand).
    #[test]
    fn a_window_already_at_the_target_is_not_resized() {
        let mut guard = SnapGuard::default();
        assert!(!guard.should_ask_for((1280, 800), (1280, 800)));
        assert!(!guard.should_ask_for((1280, 800), (1281, 800)));
    }

    #[test]
    fn a_settled_drag_asks_once_and_the_echo_ends_it() {
        let mut guard = SnapGuard::default();
        // The drag settled at 1200x740; the snap asks for 1280x800.
        assert!(guard.should_ask_for((1280, 800), (1200, 740)));
        // set_size worked, and its own Resized event re-armed the timer.
        assert!(!guard.should_ask_for((1280, 800), (1280, 800)));
    }

    /// Loop 2: a window manager that answers `set_size` with something else.
    /// Three tries, then this shell leaves the window alone rather than
    /// fighting the compositor forever at one round trip per settle.
    #[test]
    fn a_window_manager_that_refuses_is_asked_a_bounded_number_of_times() {
        let mut guard = SnapGuard::default();
        for attempt in 1..=MAX_UNANSWERED {
            assert!(
                guard.should_ask_for((1280, 800), (1300, 810)),
                "attempt {attempt} should still be tried"
            );
        }
        assert!(
            !guard.should_ask_for((1280, 800), (1300, 810)),
            "the {}th ask for a size the window never reports is a loop",
            MAX_UNANSWERED + 1
        );
    }

    /// The counter must not leak into the next drag: a user who resizes again
    /// gets a snap, even if an earlier target was abandoned.
    #[test]
    fn a_new_target_resets_the_counter() {
        let mut guard = SnapGuard::default();
        for _ in 0..=MAX_UNANSWERED {
            guard.should_ask_for((1280, 800), (1300, 810));
        }
        assert!(!guard.should_ask_for((1280, 800), (1300, 810))); // given up
        assert!(guard.should_ask_for((1920, 1200), (1900, 1180))); // a real drag
    }

    /// And a target that DID land is askable again later — the same size, a
    /// second time, after the window has actually been there.
    #[test]
    fn a_target_that_landed_can_be_asked_for_again() {
        let mut guard = SnapGuard::default();
        assert!(guard.should_ask_for((1280, 800), (1200, 740)));
        assert!(!guard.should_ask_for((1280, 800), (1280, 800))); // it landed
        for attempt in 1..=MAX_UNANSWERED {
            assert!(
                guard.should_ask_for((1280, 800), (1200, 740)),
                "attempt {attempt} after a successful snap should be tried"
            );
        }
    }

    // -- the cross-language pin ---------------------------------------------

    /// The page's rule and this shell's ladder are two halves of one contract
    /// that crosses a language boundary, and `page_scale` above is a
    /// TRANSCRIPTION — nothing makes it stay true on its own. So the real
    /// source is read at compile time and the numbers it is transcribed from
    /// are pinned. Change the ladder in one language and this fails in the
    /// other, which is the only way this can be kept honest short of running
    /// both.
    #[test]
    fn the_pages_scale_rule_still_says_what_this_shell_assumes() {
        const VIEWPORT_TS: &str = include_str!("../../../app/frontend/src/render/viewport.ts");
        for needle in [
            "const BASE_W = 640;",
            "const BASE_H = 400;",
            "export const MAX_SCALE = 3;",
            // crispStep: the ladder's increment, and the fractional-dpr
            // fallback that makes `crisp_step` above a mirror and not a guess.
            "return dpr >= 1 && dpr === Math.floor(dpr) ? 1 / dpr : 1;",
            // chooseScale: snap DOWN to a crisp factor, with the same epsilon.
            "const snapped = Math.floor(capped / step + 1e-9) * step;",
        ] {
            assert!(
                VIEWPORT_TS.contains(needle),
                "app/frontend/src/render/viewport.ts no longer contains {needle:?}. \
                 That file and this one are the two ends of the WINDOW-FIT contract: this \
                 shell sizes the window to a rung of the page's own ladder, and if the \
                 ladder moved, `crisp_step`/`crisp_sizes` in lib.rs must move with it or \
                 the game stops touching its window edges. Update both, then fix this list."
            );
        }
        // The cap is a number in two languages; assert it, not just its text.
        assert_eq!(MAX_SCALE, 3.0);
    }
}
