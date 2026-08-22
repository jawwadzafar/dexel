//! dexel desktop shell — a native window around dexel's existing Go server.
//!
//! Implements ADR 0015 / docs/plan/F3-design.md task T1. dexel is NOT a Rust
//! app: the product is a Go backend serving an HTML/NES.css frontend over
//! loopback (ADR 0011). This crate is packaging only — it runs that same
//! compiled binary as a Tauri **sidecar** and points a webview at the URL the
//! server reports.
//!
//! Lifecycle, in the order it happens:
//!
//! 1. `setup` spawns the `dexel-server` sidecar with `-addr 127.0.0.1:0`
//!    (loopback only, OS-assigned port) and nothing else. Since EMBED-1
//!    (docs/plan/ROADMAP.md) the sidecar is a SELF-CONTAINED binary: it
//!    carries the frontend (`app/public`) and the sprites (`app/assets`)
//!    inside itself via `go:embed`, so there is nothing for this shell to
//!    locate, bundle as a Tauri resource, or point it at. The `-public` /
//!    `DEXEL_ASSETS_DIR` arguments this file used to pass — and the
//!    `bundle.resources` map in tauri.conf.json that fed them — are gone;
//!    both survive in the Go server only as explicit dev overrides.
//! 2. A dedicated reader thread drains the sidecar's stdout/stderr into the
//!    log and watches for the one machine-readable handshake line
//!    `DEXEL_LISTENING http://127.0.0.1:<port>` (printed by app/main.go the
//!    moment its listener binds), then hands that URL back to `setup`.
//! 3. `setup` builds the single window on that URL via
//!    [`tauri::WebviewUrl::External`]. Because the page's origin IS the
//!    server's origin, the frontend's `location.host`-derived WebSocket URL
//!    is already correct and the server's same-origin check accepts it with
//!    no `-insecure-origin` and no wildcard (ADR 0015 §Security).
//! 4. On exit the sidecar is SIGTERM'd (so main.go's handler runs its final
//!    save) and hard-killed only if it outstays the grace period.
//!
//! Why the shell — not the JS — learns the port: the window cannot be created
//! until the URL is known, so the port has to arrive on a channel Rust owns.
//! stdout is the one it already owns from `spawn()`.
//!
//! ## Verified against
//!
//! * <https://v2.tauri.app/develop/sidecar/> — `bundle.externalBin`, the
//!   `<name>-<target-triple>` naming rule, `app.shell().sidecar(..)`,
//!   `(Receiver<CommandEvent>, CommandChild)`, `CommandEvent::Stdout(Vec<u8>)`.
//! * <https://docs.rs/tauri/latest/tauri/webview/struct.WebviewWindowBuilder.html>
//!   and `tauri::{WebviewUrl, RunEvent, Url}` (tauri 2.11.x).
//! * <https://docs.rs/tauri-plugin-shell/latest/tauri_plugin_shell/process/>
//!   — `Command::arg`, `CommandChild::{pid, kill}` (note `kill`
//!   consumes `self`, which is why the child lives in an `Option`).
//!
//! ## NOT verified
//!
//! This file has never been compiled: the machine that authored it had no
//! Rust toolchain and no webkit2gtk. Everything above is doc-checked, not
//! build-checked. See `../README.md` for what the first real build must
//! confirm.

use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::mpsc::{self, RecvTimeoutError};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use tauri::{Manager, RunEvent, WebviewUrl, WebviewWindowBuilder};
use tauri_plugin_shell::process::{CommandChild, CommandEvent};
use tauri_plugin_shell::ShellExt;

/// The `bundle.externalBin` base name. Tauri appends the target triple on
/// disk (`dexel-server-x86_64-unknown-linux-gnu`) and strips it again inside
/// the bundle, so the name used here is the bare one.
const SIDECAR_NAME: &str = "dexel-server";

/// The exact prefix app/main.go prints to stdout once bound. Keep in sync
/// with `handshakeLine` there — it is a two-process wire contract, and
/// main.go has a unit test pinning its shape.
const HANDSHAKE_PREFIX: &str = "DEXEL_LISTENING ";

/// How long to wait for that line before giving up. Binding a loopback
/// socket is effectively instantaneous, so this is a "something is badly
/// wrong" bound, not a tuning knob. Failing loudly beats a window that never
/// appears.
const HANDSHAKE_TIMEOUT: Duration = Duration::from_secs(20);

/// How long the sidecar gets to honour SIGTERM (persist state, stop the
/// provider, shut the HTTP server down) before we escalate to SIGKILL.
const SHUTDOWN_GRACE: Duration = Duration::from_secs(3);
const SHUTDOWN_POLL: Duration = Duration::from_millis(25);

const WINDOW_LABEL: &str = "main";
const WINDOW_TITLE: &str = "dexel";
/// The frontend root is a fixed 640x400 canvas plus its sprint/ticker chrome
/// (~660x430). 660x460 is both the default AND the minimum, so the
/// fixed-pixel layout can never be clipped (F3-design.md §5).
const WINDOW_W: f64 = 660.0;
const WINDOW_H: f64 = 460.0;

/// Owns the spawned sidecar so it can be terminated exactly once.
///
/// Held in Tauri's managed state, which is why the child sits behind a
/// `Mutex<Option<..>>` rather than being owned directly: `CommandChild::kill`
/// takes `self` by value, so terminating requires *moving* the child out,
/// and `Option::take` makes that idempotent — `RunEvent::ExitRequested`
/// followed by `RunEvent::Exit` calls [`Self::shutdown`] twice and the second
/// call is a no-op.
struct SidecarGuard {
    child: Mutex<Option<CommandChild>>,
    /// Set by the reader thread when the sidecar's event stream reports
    /// termination (or simply ends). Read only on unix, where it is how we
    /// learn that SIGTERM was honoured without having to reap the pid
    /// ourselves — the reader thread owns the event stream, not us.
    #[cfg_attr(not(unix), allow(dead_code))]
    exited: Arc<AtomicBool>,
}

impl SidecarGuard {
    /// Terminate the sidecar, preferring the graceful path.
    ///
    /// On unix: SIGTERM by pid first, because `CommandChild::kill` maps to
    /// `std::process::Child::kill` (SIGKILL), which would bypass main.go's
    /// `signal.Notify(SIGINT, SIGTERM)` handler and therefore its final
    /// save. Escalate to the hard kill only if the process outstays
    /// [`SHUTDOWN_GRACE`]. Elsewhere (Windows) the hard kill is accepted:
    /// autosave already bounds loss to ~30s (ADR 0015 §Lifecycle).
    fn shutdown(&self) {
        let Ok(mut slot) = self.child.lock() else {
            log::error!("sidecar mutex poisoned; refusing to guess at termination");
            return;
        };
        let Some(child) = slot.take() else {
            // Already terminated — the ExitRequested/Exit double-call path.
            return;
        };
        let pid = child.pid();

        #[cfg(unix)]
        {
            log::info!("sidecar pid {pid}: SIGTERM (graceful — lets it save on the way out)");
            // SAFETY: a plain libc call. `pid` came from the CommandChild we
            // are holding right now, so it names our own child and cannot
            // have been reaped-and-recycled behind our back.
            let sent = unsafe { libc::kill(pid as libc::pid_t, libc::SIGTERM) } == 0;
            if sent {
                let deadline = Instant::now() + SHUTDOWN_GRACE;
                while Instant::now() < deadline {
                    if self.exited.load(Ordering::SeqCst) {
                        log::info!("sidecar pid {pid}: exited cleanly after SIGTERM");
                        // Deliberately no kill(): the process is gone, and
                        // dropping CommandChild does not signal anything.
                        return;
                    }
                    std::thread::sleep(SHUTDOWN_POLL);
                }
                log::warn!(
                    "sidecar pid {pid}: still alive {SHUTDOWN_GRACE:?} after SIGTERM; escalating"
                );
            } else {
                log::warn!("sidecar pid {pid}: SIGTERM failed; escalating");
            }
        }

        match child.kill() {
            Ok(()) => log::info!("sidecar pid {pid}: hard-killed"),
            Err(e) => log::error!("sidecar pid {pid}: hard kill failed: {e}"),
        }
    }
}

/// Spawn the sidecar and block until it reports its URL.
///
/// On any failure the child is terminated before returning, so a shell that
/// cannot come up never leaves a headless Go server (and its loopback
/// socket) behind.
fn spawn_sidecar(app: &tauri::AppHandle) -> Result<(String, SidecarGuard), String> {
    // No -public, no DEXEL_ASSETS_DIR, no resource-directory resolution:
    // EMBED-1 made the sidecar self-contained (see the module docs), so the
    // only thing this shell tells it is which address to bind.
    let command = app
        .shell()
        .sidecar(SIDECAR_NAME)
        .map_err(|e| format!("locate sidecar {SIDECAR_NAME:?}: {e}"))?
        // 127.0.0.1:0 — loopback only (never a wildcard), OS-assigned port
        // so a busy 8080 or a second instance can never collide. The real
        // port comes back over the handshake below.
        .arg("-addr")
        .arg("127.0.0.1:0");

    let (mut rx, child) = command.spawn().map_err(|e| format!("spawn sidecar: {e}"))?;

    let exited = Arc::new(AtomicBool::new(false));
    let guard = SidecarGuard {
        child: Mutex::new(Some(child)),
        exited: Arc::clone(&exited),
    };

    let (url_tx, url_rx) = mpsc::channel::<String>();
    let reader = std::thread::Builder::new()
        .name("dexel-sidecar-reader".into())
        .spawn(move || {
            let mut announced = false;
            // blocking_recv() is correct here and cannot panic: this is a
            // plain OS thread with no tokio runtime entered (blocking_recv
            // only panics when called from inside an async context).
            //
            // This loop must keep running for the life of the sidecar, not
            // just until the handshake: it is the only reader of the child's
            // stdout/stderr pipes, and abandoning them would eventually
            // block the Go process on a full pipe buffer.
            while let Some(event) = rx.blocking_recv() {
                match event {
                    CommandEvent::Stdout(bytes) => {
                        let line = String::from_utf8_lossy(&bytes);
                        let line = line.trim_end();
                        log::info!("[dexel-server] {line}");
                        if !announced {
                            if let Some(url) = line.strip_prefix(HANDSHAKE_PREFIX) {
                                let url = url.trim().to_string();
                                log::info!("handshake: sidecar is serving {url}");
                                // Only stop looking once the value is
                                // actually delivered; a send error means
                                // setup gave up, and there is nothing to do
                                // but keep draining the pipes.
                                announced = url_tx.send(url).is_ok();
                            }
                        }
                    }
                    // main.go's human-readable log goes to stderr; forward it
                    // at info too, so a packaged crash is diagnosable from
                    // the log file alone.
                    CommandEvent::Stderr(bytes) => {
                        log::info!("[dexel-server] {}", String::from_utf8_lossy(&bytes).trim_end());
                    }
                    CommandEvent::Error(err) => {
                        log::error!("[dexel-server] pipe error: {err}");
                    }
                    CommandEvent::Terminated(payload) => {
                        log::warn!("[dexel-server] terminated: {payload:?}");
                        exited.store(true, Ordering::SeqCst);
                    }
                    // CommandEvent is #[non_exhaustive]; ignore anything a
                    // future plugin release adds rather than failing to build.
                    _ => {}
                }
            }
            // Stream closed: the child is gone whether or not we saw an
            // explicit Terminated event.
            exited.store(true, Ordering::SeqCst);
            log::info!("sidecar event stream closed");
        });

    if let Err(e) = reader {
        guard.shutdown();
        return Err(format!("spawn sidecar reader thread: {e}"));
    }

    match url_rx.recv_timeout(HANDSHAKE_TIMEOUT) {
        Ok(url) => Ok((url, guard)),
        Err(RecvTimeoutError::Timeout) => {
            guard.shutdown();
            Err(format!(
                "sidecar never printed {HANDSHAKE_PREFIX:?} within {HANDSHAKE_TIMEOUT:?} \
                 (see the [dexel-server] lines in the log for what it did say)"
            ))
        }
        Err(RecvTimeoutError::Disconnected) => {
            guard.shutdown();
            Err("sidecar exited before printing its handshake line".to_string())
        }
    }
}

/// Build dexel's one window, pointed at the sidecar's loopback URL.
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
        // so sidecar output reaches both a terminal (dev) and a log file
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
            let (url, guard) = spawn_sidecar(&handle)?;
            let url = tauri::Url::parse(&url)
                .map_err(|e| format!("sidecar reported an unparseable URL {url:?}: {e}"))?;

            // Manage BEFORE building the window, so the window-build failure
            // path below (and the run-loop handler) can still reach the child.
            app.manage(guard);

            if let Err(e) = build_window(app, url) {
                // Never leave a headless server running for a window that
                // does not exist.
                app.state::<SidecarGuard>().shutdown();
                return Err(Box::new(e));
            }
            Ok(())
        })
        .build(tauri::generate_context!())
        .expect("error while building the dexel desktop shell")
        .run(|handle, event| {
            // With a single window, closing it raises ExitRequested; handling
            // Exit as well covers a programmatic quit. shutdown() is
            // idempotent, so being called for both is fine. We deliberately
            // do NOT call api.prevent_exit() — quitting means quitting.
            if matches!(event, RunEvent::ExitRequested { .. } | RunEvent::Exit) {
                if let Some(guard) = handle.try_state::<SidecarGuard>() {
                    guard.shutdown();
                }
            }
        });
}
