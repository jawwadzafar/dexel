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
//! private `dexel-server` with `-addr 127.0.0.1:0` and SIGTERM'd it on window
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
//!   `<name>-<target-triple>` naming rule, `app.shell().sidecar(..)`,
//!   `(Receiver<CommandEvent>, CommandChild)`, `CommandEvent::Stdout(Vec<u8>)`.
//! * <https://docs.rs/tauri/latest/tauri/webview/struct.WebviewWindowBuilder.html>
//!   and `tauri::{WebviewUrl, RunEvent, Url}` (tauri 2.11.x).
//! * The `dexel status --json` / `dexel start` contract in
//!   `app/cmd_lifecycle.go` (ADR 0018), including the exit-code note on
//!   [`discover_runtime`].

use std::path::PathBuf;
use std::sync::mpsc::{self, RecvTimeoutError};
use std::time::Duration;

use tauri::{RunEvent, WebviewUrl, WebviewWindowBuilder};
use tauri_plugin_shell::process::CommandEvent;
use tauri_plugin_shell::ShellExt;

/// The `bundle.externalBin` base name. Tauri appends the target triple on
/// disk (`dexel-server-x86_64-unknown-linux-gnu`) and strips it again inside
/// the bundle, so the name used here is the bare one.
const SIDECAR_NAME: &str = "dexel-server";

/// How long any one short-lived CLI call (`status`, `start`) gets before we
/// give up and fail loudly. `start` is the slow one and it only has to fork a
/// child and wait for its readiness probe, so this is a "something is badly
/// wrong" bound, not a tuning knob.
const CLI_TIMEOUT: Duration = Duration::from_secs(20);

const WINDOW_LABEL: &str = "main";
/// The window's title bar — a DISPLAY string, so it is the capitalised product
/// name that `tauri.conf.json`'s `productName` also carries. Everything else in
/// this file stays lowercase `dexel` on purpose: those are artifacts, not
/// display text (the `dexel-server` sidecar, `~/.local/bin/dexel`, the
/// `dexel stop` command a log line tells the user to run).
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
/// here: `status --json` exits 1 when no runtime is running, which is a
/// perfectly normal answer and not a failure (see [`discover_runtime`]).
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
/// `Ok(None)` means "nothing running" — a normal answer, not a failure. Note
/// that `status --json` EXITS 1 in that case, so the exit code must not be
/// treated as an error here; the authority is the `running` field in the JSON
/// it prints (app/cmd_lifecycle.go). `status` also verifies liveness by
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
            build_window(app, url)?;
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
