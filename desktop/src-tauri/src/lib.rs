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
//!    Because the page's origin IS the server's origin, the frontend's
//!    `location.host`-derived WebSocket URL is already correct and the
//!    server's same-origin check accepts it with no `-insecure-origin` and no
//!    wildcard (ADR 0015 §Security).
//! 4. On window close this shell terminates NOTHING. The runtime keeps
//!    ticking, keeps counting keystrokes and keeps autosaving. `dexel stop`
//!    is the only thing that stops it — that is the whole point.
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
/// The frontend root is a fixed 640x400 canvas plus its sprint/ticker chrome
/// (~660x430). 660x460 is both the default AND the minimum, so the
/// fixed-pixel layout can never be clipped (F3-design.md §5).
const WINDOW_W: f64 = 660.0;
const WINDOW_H: f64 = 460.0;

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
fn discover_runtime(app: &tauri::AppHandle, driver: &Driver) -> Result<Option<String>, String> {
    let out = run_cli(app, driver, &["status", "--json"])?;
    let parsed: serde_json::Value = serde_json::from_str(out.stdout.trim()).map_err(|e| {
        format!(
            "`status --json` printed unparseable JSON ({e}): {}",
            out.stdout
        )
    })?;

    if parsed.get("running").and_then(|v| v.as_bool()) != Some(true) {
        if let Some(reason) = parsed.get("reason").and_then(|v| v.as_str()) {
            log::info!("no runtime yet: {reason}");
        }
        return Ok(None);
    }
    let url = parsed.get("url").and_then(|v| v.as_str()).ok_or_else(|| {
        format!(
            "`status --json` reported running with no url: {}",
            out.stdout
        )
    })?;
    Ok(Some(url.to_string()))
}

/// The URL to open the window on: an already-running runtime if there is one,
/// otherwise a freshly started one.
///
/// `start` is the CLI's own detached-runtime path — it takes the lock, writes
/// `runtime.json` and waits for readiness — so a runtime started here is
/// indistinguishable from one started by `dexel start` in a terminal, and
/// outlives this window exactly the same way.
fn runtime_url(app: &tauri::AppHandle) -> Result<String, String> {
    let driver = resolve_driver();
    log::info!("driving {}", driver.describe());
    if let Some(url) = discover_runtime(app, &driver)? {
        log::info!("attaching to the running dexel runtime at {url} (this window owns nothing)");
        return Ok(url);
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

    match discover_runtime(app, &driver)? {
        Some(url) => {
            log::info!("started the dexel runtime at {url}");
            log::info!("it keeps running after this window closes — `dexel stop` is what stops it");
            Ok(url)
        }
        None => {
            Err("`start` reported success but no runtime was discoverable afterwards".to_string())
        }
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
fn watch_for_a_moved_runtime(app: &tauri::App, loaded: tauri::Url) {
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
        std::thread::spawn(move || {
            re_point_if_moved(&handle, &target, &current);
            checking.store(false, Ordering::SeqCst);
        });
    });
}

/// One re-resolve: ask the CLI where the runtime is and navigate only if that
/// is somewhere other than where this webview already is.
fn re_point_if_moved(
    app: &tauri::AppHandle,
    window: &tauri::WebviewWindow,
    current: &Mutex<tauri::Url>,
) {
    let driver = resolve_driver();
    match discover_runtime(app, &driver) {
        Ok(Some(found)) => {
            let parsed = match tauri::Url::parse(&found) {
                Ok(u) => u,
                Err(e) => {
                    log::warn!("`status --json` reported an unparseable URL {found:?}: {e}");
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
            if let Err(e) = window.navigate(parsed.clone()) {
                log::error!("could not navigate the window to {parsed}: {e}");
                return;
            }
            *cur = parsed;
        }
        // Not an error and not something to fix from here: `dexel stop` means
        // stopped, and this shell does not resurrect the daemon.
        Ok(None) => log::info!("no runtime is running; leaving this window as it is"),
        Err(e) => log::warn!("could not ask where the runtime is: {e}"),
    }
}

/// Build dexel's one window, pointed at the runtime's loopback URL.
fn build_window(app: &tauri::App, url: tauri::Url) -> tauri::Result<()> {
    WebviewWindowBuilder::new(app, WINDOW_LABEL, WebviewUrl::External(url))
        .title(WINDOW_TITLE)
        .inner_size(WINDOW_W, WINDOW_H)
        // Same as the default size: the layout is fixed-pixel and does not
        // reflow, so shrinking below this would clip it rather than adapt.
        .min_inner_size(WINDOW_W, WINDOW_H)
        .resizable(true)
        // ADR 0007: a companion the editor buries never gets seen.
        .always_on_top(true)
        // Native frame in Phase 1 — free drag/close. The in-page wordmark
        // titlebar stays; a frameless look is Phase 2 polish.
        .decorations(true)
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
            let url = runtime_url(&handle)?;
            let url = tauri::Url::parse(&url)
                .map_err(|e| format!("runtime reported an unparseable URL {url:?}: {e}"))?;

            // No managed child, no guard: there is nothing for this shell to
            // own or terminate. If the window fails to build we simply fail —
            // the runtime is not ours to clean up, and killing it would be
            // exactly the bug this shell was rewritten to remove.
            build_window(app, url.clone())?;
            watch_for_a_moved_runtime(app, url);
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
