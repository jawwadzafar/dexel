//! Active-**application** detection, behind the `active-app` Cargo feature.
//!
//! The rest of this crate answers "*is* the user working?" as a content-free
//! rate ([`ActivityEvent`](crate::ActivityEvent)). This module answers the one
//! extra question the game needs to stop inventing fake project names and
//! start mirroring the real session: "*where* is the user working?" — so the
//! HUD can say **"Coding in VS Code"** instead of "Fix login flow".
//!
//! # The privacy boundary is the whole design
//!
//! We capture the **application identity only**: the window's class / app-id,
//! e.g. `code`, `firefox`, `kitty`. We never capture a **window title**, and
//! that is not a matter of taste. A title is the single most sensitive string
//! on a developer's screen: it is the file you have open, the document you are
//! writing, the page you are reading, the person you are messaging. There is
//! no version of this game that is worth knowing that.
//!
//! The rule is enforced structurally rather than by discipline:
//!
//! * The X11 backend interns exactly the properties in
//!   [`READABLE_PROPERTIES`] and indexes that array to build a request. It
//!   *cannot* ask for `_NET_WM_NAME` / `WM_NAME` / `_NET_WM_VISIBLE_NAME`
//!   without someone editing that one array — which a unit test forbids.
//! * [`ActiveApp`] has private fields and exactly one constructor,
//!   [`ActiveApp::from_app_id`], which runs every string through
//!   [`sanitize_app_id`]. There is no field a title could be assigned to and
//!   no path into the struct that skips sanitization.
//! * `WM_CLASS` is parsed by `class_from_wm_class`, which takes the *class*
//!   field and drops the rest of the buffer, so even a malformed property
//!   carrying extra strings cannot smuggle one out.
//! * Nothing here logs, and no error variant carries anything read off a
//!   window.
//!
//! Sanitization also treats the app name as **untrusted input**, because it
//! is: any process on the machine can set its own `WM_CLASS` to a megabyte of
//! terminal escapes. [`sanitize_app_id`] lowercases, strips paths, collapses
//! anything outside `[a-z0-9._-]`, and caps the result at
//! [`MAX_APP_ID_LEN`] — so whatever the HUD renders is short, printable and
//! boring by construction.
//!
//! # Why X11/XWayland, and what was probed and rejected
//!
//! Getting the focused window is per-window-system work, and on Wayland it is
//! deliberately restricted — there is no portable API, by design, because
//! "which app is the user looking at" is exactly the kind of thing Wayland
//! stopped letting arbitrary clients ask. The options were probed on the
//! target machine (GNOME Shell 50.1, Ubuntu, `XDG_SESSION_TYPE=wayland`)
//! *before* any Rust was written:
//!
//! * **`org.gnome.Shell.Introspect.GetWindows` (D-Bus) — rejected, denied.**
//!   This is the right API in principle: it returns per-window `app-id`s
//!   without titles. In practice GNOME gates it to an allowlist of
//!   `xdg-desktop-portal` implementations, and calling it as an ordinary user
//!   process answers
//!   `org.freedesktop.DBus.Error.AccessDenied: GetWindows is not allowed`
//!   (`GetRunningApplications` likewise). On older GNOME the
//!   `org.gnome.shell introspect` GSettings key could open it up; on GNOME 50
//!   that key no longer exists (`No such key "introspect"`), so there is
//!   nothing for a user to flip. Implementing it in Rust would mean pulling a
//!   whole async D-Bus stack in to receive a permission error, so we don't.
//! * **`org.gnome.Shell.Eval` — rejected, disabled.** Returns `(false, '')`:
//!   unsafe-mode is off, as it is on every stock GNOME since 41.
//! * **AT-SPI accessibility bus — rejected, not running.** The bus address is
//!   advertised but the socket does not exist, because
//!   `org.gnome.desktop.interface toolkit-accessibility` is `false`. Turning
//!   accessibility on system-wide to power a cosmetic HUD label is not a
//!   trade we get to make on the user's behalf.
//! * **`xdg-desktop-portal` — rejected, no such API.** There is no
//!   focused-window portal interface to call.
//! * **`/proc` heuristics — rejected on principle.** Guessing the focused
//!   window from process CPU/state is wrong by design: it would confidently
//!   report the wrong app rather than admit it does not know.
//! * **X11 `_NET_ACTIVE_WINDOW` + `WM_CLASS` — implemented.** This is the
//!   EWMH standard route. It works fully on an X11 session, and on a Wayland
//!   session it still works for every **XWayland-hosted** client — which on
//!   Linux is a large share of exactly the apps this feature is about
//!   (Electron editors and many browsers still default to the X11 backend).
//!   [`x11rb`] speaks the wire protocol over a plain socket in pure Rust, so
//!   there is no `libX11`, no `libxcb` and no `pkg-config` involved — which
//!   matters, because this machine has neither the X11 dev packages nor sudo
//!   to install them.
//!
//! # What "unknown" means, and why it is a first-class answer
//!
//! When a **Wayland-native** window has focus, mutter points
//! `_NET_ACTIVE_WINDOW` at an internal focus-proxy window that has no
//! properties at all — no `WM_CLASS`, nothing. Observed on this machine:
//! `_NET_ACTIVE_WINDOW` = `0x400003`, `xprop -id 0x400003` prints nothing.
//! That is not a bug to work around; it is the compositor declining to tell
//! us, and the correct response is to say so.
//!
//! So [`ActiveAppWatcher::current_app`] returns `Option<String>`, and `None`
//! is a normal, tested outcome meaning "no opinion". The game must render its
//! generic fallback then — never a guess. Likewise
//! [`ActiveAppWatcher::new`] returns an error instead of panicking when there
//! is no reachable window server at all (a headless CI container, a bare TTY,
//! a Wayland session with XWayland disabled), so the caller can simply skip
//! the feature.
//!
//! # Cost
//!
//! [`ActiveAppWatcher::current_app`] is built to be called every frame. It
//! answers from a cached value and only talks to the X server when that value
//! is older than [`REFRESH_INTERVAL`] (~1 s), because the focused application
//! changes on human timescales, not frame timescales. A refresh is two small
//! property requests on a local socket; there is no thread and no lock, so
//! there is nothing for the game loop to contend with.

use std::fmt;
use std::time::{Duration, Instant};

/// Every X11 window property this module may ever ask the server for.
///
/// **This array is the privacy boundary.** The backend interns these names
/// once and then only ever indexes this array (via
/// `PROP_NET_ACTIVE_WINDOW` / `PROP_WM_CLASS`) to build a request, so the
/// set of things we can learn about the user's windows is exactly what you can
/// read here. Adding a title-bearing property — `WM_NAME`, `_NET_WM_NAME`,
/// `_NET_WM_VISIBLE_NAME`, `WM_ICON_NAME` — would break the crate's stated
/// guarantee, and `only_class_properties_are_readable` fails if anyone tries.
pub const READABLE_PROPERTIES: [&str; 2] = ["_NET_ACTIVE_WINDOW", "WM_CLASS"];

/// Index of `_NET_ACTIVE_WINDOW` in [`READABLE_PROPERTIES`]: the EWMH root
/// property naming the focused window.
const PROP_NET_ACTIVE_WINDOW: usize = 0;

/// Index of `WM_CLASS` in [`READABLE_PROPERTIES`]: the ICCCM
/// `instance\0class\0` pair, i.e. the application's identity and nothing else.
const PROP_WM_CLASS: usize = 1;

/// Longest app id [`sanitize_app_id`] will ever return.
///
/// The value is rendered in a small pixel-art HUD, so a long string is a
/// layout bug even when it is honest; and since `WM_CLASS` is attacker-
/// controlled (any process may set its own), a hard cap is also the thing that
/// makes "app name" a bounded quantity rather than an arbitrary blob. 32 is
/// comfortably longer than every real-world app id
/// (`org.gnome.TextEditor` is 21).
pub const MAX_APP_ID_LEN: usize = 32;

/// How stale the cached answer may get before [`ActiveAppWatcher`] asks the
/// window server again.
///
/// One second, because a human switches windows a few times a minute at most,
/// and a companion that notices "you moved to the browser" within a second is
/// indistinguishable from one that notices instantly. Everything below this
/// interval is served from memory, which is what makes `current_app()` safe to
/// call from a per-frame system.
pub const REFRESH_INTERVAL: Duration = Duration::from_secs(1);

/// Why active-application detection could not be started.
///
/// Both variants mean "run the game without this feature", never "give up":
/// the caller is expected to fall back to its generic label. Neither carries
/// anything observed about the user's windows — construction fails before any
/// window is looked at, and would not record it anyway.
#[derive(Debug)]
pub enum ActiveAppError {
    /// No backend is compiled in for this platform (anything but Linux, for
    /// now). Deliberately a runtime error rather than a compile failure, so a
    /// cross-platform build can enable the feature and degrade.
    UnsupportedPlatform,
    /// There is no window server we can ask. Typically no `DISPLAY` at all (a
    /// bare TTY, a headless CI container), a Wayland session with XWayland
    /// disabled, or a stale/missing `XAUTHORITY` cookie. The payload is the
    /// connection diagnostic — a protocol/socket message, never window data.
    NoWindowServer(String),
}

impl fmt::Display for ActiveAppError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::UnsupportedPlatform => f.write_str(
                "active-application detection is not implemented for this platform \
                 (Linux/X11 only for now)",
            ),
            Self::NoWindowServer(reason) => write!(
                f,
                "could not reach a window server to read the focused window \
                 ({reason}) — check DISPLAY/XAUTHORITY, or accept that this \
                 session exposes no focused-window API"
            ),
        }
    }
}

impl std::error::Error for ActiveAppError {}

/// The foreground application, in the two forms the game wants.
///
/// Private fields with a single sanitizing constructor
/// ([`ActiveApp::from_app_id`]) are the point: there is no way to put a window
/// title, a file path or a URL in here, because there is no way to put
/// *anything* in here that has not been through [`sanitize_app_id`].
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ActiveApp {
    /// The sanitized application id, e.g. `code`, `firefox`,
    /// `org.kde.konsole`. Lowercase `[a-z0-9._-]`, at most
    /// [`MAX_APP_ID_LEN`] bytes.
    id: String,
    /// A human-facing name for [`Self::id`], e.g. `VS Code`. Falls back to the
    /// id itself when we have no honest friendlier name.
    display: String,
}

impl ActiveApp {
    /// Build from a raw application id / window class, sanitizing it.
    ///
    /// Returns `None` when nothing usable survives sanitization (an empty
    /// class, or one made entirely of rejected characters) — the same "no
    /// opinion" answer the rest of the module uses.
    ///
    /// Public so the `companion` crate can build one in its own tests without
    /// a window server, and because going through this constructor is *always*
    /// the right way to make an `ActiveApp`.
    #[must_use]
    pub fn from_app_id(raw: &str) -> Option<Self> {
        let id = sanitize_app_id(raw)?;
        let display = display_name(&id).to_owned();
        Some(Self { id, display })
    }

    /// The sanitized raw id, for logic and comparisons (`"code"`).
    #[must_use]
    pub fn id(&self) -> &str {
        &self.id
    }

    /// The friendly name, for rendering (`"VS Code"`).
    #[must_use]
    pub fn display(&self) -> &str {
        &self.display
    }
}

/// Friendly display names for application ids we recognise.
///
/// Kept **small and honest** on purpose. This is a courtesy for ids that are
/// genuinely unpleasant to read (`jetbrains-idea`, `org.kde.konsole`), not an
/// app database — an unknown id renders as itself, which is always truthful,
/// whereas a stale or guessed mapping renders a lie. `kitty` is listed
/// deliberately: its friendly name really is lowercase, and encoding that
/// stops anyone "fixing" it with a title-case rule.
const FRIENDLY_NAMES: &[(&str, &str)] = &[
    ("alacritty", "Alacritty"),
    ("chromium", "Chromium"),
    ("code", "VS Code"),
    ("code-oss", "VS Code"),
    ("cursor", "Cursor"),
    ("emacs", "Emacs"),
    ("firefox", "Firefox"),
    ("firefox-esr", "Firefox"),
    ("google-chrome", "Chrome"),
    ("jetbrains-clion", "CLion"),
    ("jetbrains-idea", "IntelliJ"),
    ("jetbrains-idea-ce", "IntelliJ"),
    ("jetbrains-pycharm", "PyCharm"),
    ("kitty", "kitty"),
    ("konsole", "Konsole"),
    ("org.gnome.console", "Console"),
    ("org.gnome.nautilus", "Files"),
    ("org.gnome.ptyxis", "Ptyxis"),
    ("org.kde.konsole", "Konsole"),
    ("slack", "Slack"),
    ("vscodium", "VSCodium"),
    ("zed", "Zed"),
];

/// The friendly name for a sanitized app id, or the id itself if we have none.
///
/// Never invents a name: the fallback is the (already sanitized, already
/// length-capped) id, so an unrecognised app shows up as `foo-bar` rather than
/// as a wrong guess.
#[must_use]
pub fn display_name(app_id: &str) -> &str {
    FRIENDLY_NAMES
        .iter()
        .find(|(id, _)| *id == app_id)
        .map_or(app_id, |(_, name)| *name)
}

/// Is this byte allowed in an app id? See [`sanitize_app_id`].
///
/// `[a-z0-9._-]` — enough for every real app id (`code`, `google-chrome`,
/// `org.gnome.Nautilus` lowercased, `jetbrains-idea`) and nothing else. No
/// spaces, no slashes, no control bytes, no ANSI escapes, no combining marks,
/// nothing bidirectional.
fn is_allowed(ch: char) -> bool {
    ch.is_ascii_lowercase() || ch.is_ascii_digit() || matches!(ch, '.' | '_' | '-')
}

/// Turn an arbitrary window class / app-id into something safe to render.
///
/// Treats the input as **untrusted**, because it is: `WM_CLASS` is set by the
/// application itself, so it can be long, non-UTF-8-ish, full of control bytes
/// or deliberately deceptive. The steps, in order:
///
/// 1. **Strip paths.** Some toolkits report `/usr/lib/firefox/firefox` or a
///    Windows-style path as the class. Only the last component can be an
///    identity, and keeping the rest would leak the user's filesystem layout.
/// 2. **Lowercase** (ASCII only, so no locale surprises), because `Code`,
///    `code` and `CODE` are one application.
/// 3. **Collapse everything else.** Any run of disallowed characters becomes a
///    single `-`, which turns `Google Chrome` into `google-chrome` instead of
///    the word-mashing `googlechrome` that dropping them would give.
/// 4. **Cap** at [`MAX_APP_ID_LEN`]. The output is ASCII by construction, so
///    truncating bytes can never split a character.
/// 5. **Trim** leading/trailing separators, including one left behind by the
///    cap.
///
/// Returns `None` if nothing is left — an empty class is "unknown", not `""`.
#[must_use]
pub fn sanitize_app_id(raw: &str) -> Option<String> {
    // Step 1: last path component only. `rsplit` always yields at least one
    // item, so this cannot fail; `unwrap_or` is just to avoid the panic path.
    let base = raw.rsplit(['/', '\\']).next().unwrap_or(raw);

    // Steps 2 and 3, in one pass. `pending_separator` defers writing the `-`
    // until we know a *kept* character follows it, which is what stops a
    // trailing run of junk from producing a trailing dash.
    let mut out = String::with_capacity(base.len().min(MAX_APP_ID_LEN));
    let mut pending_separator = false;
    for ch in base.chars() {
        let ch = ch.to_ascii_lowercase();
        if is_allowed(ch) {
            if pending_separator && !out.is_empty() {
                out.push('-');
            }
            pending_separator = false;
            out.push(ch);
        } else {
            pending_separator = true;
        }
    }

    // Step 4. Safe as a byte truncation: `is_allowed` admits ASCII only.
    out.truncate(MAX_APP_ID_LEN);
    // Step 5. Also cleans up a separator the truncation stranded at the end.
    let trimmed = out.trim_matches(|ch| matches!(ch, '-' | '.' | '_'));

    (!trimmed.is_empty()).then(|| trimmed.to_owned())
}

/// Extract the **class** field from a raw ICCCM `WM_CLASS` property value.
///
/// The property is `instance\0class\0`: `("code", "Code")` for VS Code,
/// `("firefox", "Firefox")`, `("navigator", "Firefox")` on older builds. We
/// take the *class* because it is the stable application identity — the
/// instance is sometimes a per-window role (`Navigator`) that names the window
/// rather than the app. With only one field present, that field is used.
///
/// **Everything past the second field is discarded.** A well-formed `WM_CLASS`
/// has exactly two strings, but the property is application-controlled, so a
/// third string could be anything; ignoring the tail structurally is cheaper
/// than trusting it.
///
/// The bytes are Latin-1 per ICCCM. Lossy decoding is fine here because
/// [`sanitize_app_id`] rejects every non-ASCII character anyway.
fn class_from_wm_class(value: &[u8]) -> Option<String> {
    let mut fields = value.split(|byte| *byte == 0).filter(|f| !f.is_empty());
    let instance = fields.next()?;
    let class = fields.next().unwrap_or(instance);
    Some(String::from_utf8_lossy(class).into_owned())
}

/// Should the cached answer be refreshed from the window server?
///
/// Split out as a free function of plain values so the cadence is unit-tested
/// with synthetic [`Instant`]s instead of by sleeping.
fn needs_refresh(last_refresh: Option<Instant>, now: Instant) -> bool {
    match last_refresh {
        // Never asked: the first `current_app()` must produce a real answer.
        None => true,
        Some(last) => now.saturating_duration_since(last) >= REFRESH_INTERVAL,
    }
}

/// Watches which application currently has focus.
///
/// Construct with [`ActiveAppWatcher::new`], which returns an
/// [`ActiveAppError`] rather than panicking when no window server can be
/// reached. Then call [`current_app`](Self::current_app) (or
/// [`current_app_display`](Self::current_app_display)) as often as you like:
/// answers come from a cache that refreshes at most every
/// [`REFRESH_INTERVAL`].
///
/// `None` from either accessor means **"we do not know"** — a Wayland-native
/// window has focus, the desktop itself is focused, or the window vanished
/// mid-query. It never means "no app". Render a generic label; do not guess.
pub struct ActiveAppWatcher {
    /// The window-server connection, or `None` once it has failed
    /// unrecoverably (see [`Focus::Disconnected`]) — at which point the
    /// watcher degrades permanently to `None` instead of retrying a dead
    /// socket every second.
    backend: Option<backend::Backend>,
    /// Last answer, served to every caller until it goes stale.
    current: Option<ActiveApp>,
    /// When `current` was computed. `None` = never.
    last_refresh: Option<Instant>,
}

impl fmt::Debug for ActiveAppWatcher {
    /// Hand-written because the backend holds a socket that has no useful
    /// `Debug`, and because a derived impl on a struct holding user-visible
    /// state is worth thinking about once rather than inheriting: only the
    /// sanitized id is ever printed.
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("ActiveAppWatcher")
            .field("connected", &self.backend.is_some())
            .field("current", &self.current.as_ref().map(ActiveApp::id))
            .finish()
    }
}

impl ActiveAppWatcher {
    /// Start watching the foreground application.
    ///
    /// # Errors
    ///
    /// Returns [`ActiveAppError::UnsupportedPlatform`] where no backend is
    /// compiled in, or [`ActiveAppError::NoWindowServer`] when the window
    /// server cannot be reached (no `DISPLAY`, headless CI, XWayland
    /// disabled). Never panics and never blocks on a prompt: the caller is
    /// expected to carry on without the feature.
    ///
    /// Note that succeeding here does **not** promise that
    /// [`current_app`](Self::current_app) will ever return `Some`: on a
    /// Wayland session the connection is fine and the compositor still
    /// declines to name Wayland-native windows. That is the documented
    /// degradation, not a failure.
    pub fn new() -> Result<Self, ActiveAppError> {
        Ok(Self {
            backend: Some(backend::Backend::connect()?),
            current: None,
            last_refresh: None,
        })
    }

    /// The foreground application's sanitized id — `"code"`, `"firefox"` — or
    /// `None` if unknown.
    ///
    /// Cheap enough for a per-frame system: everything within
    /// [`REFRESH_INTERVAL`] of the last real query is answered from memory.
    pub fn current_app(&mut self) -> Option<String> {
        self.current().map(|app| app.id.clone())
    }

    /// The foreground application's friendly name — `"VS Code"`, `"Firefox"` —
    /// or `None` if unknown.
    ///
    /// Falls back to the sanitized id for apps not in `FRIENDLY_NAMES`, so
    /// this never returns a guessed or invented name.
    pub fn current_app_display(&mut self) -> Option<String> {
        self.current().map(|app| app.display.clone())
    }

    /// Both forms at once, borrowed — for a caller that wants the id *and* the
    /// display name without cloning twice.
    pub fn current(&mut self) -> Option<&ActiveApp> {
        let now = Instant::now();
        if needs_refresh(self.last_refresh, now) {
            self.refresh();
            self.last_refresh = Some(now);
        }
        self.current.as_ref()
    }

    /// Is a usable window-server connection still open?
    ///
    /// For a one-line startup/diagnostic log. `false` means every subsequent
    /// answer will be `None` (the connection was never made, or has since
    /// died), so the caller can stop expecting app names.
    #[must_use]
    pub fn is_connected(&self) -> bool {
        self.backend.is_some()
    }

    /// Ask the window server once and update the cache.
    fn refresh(&mut self) {
        let Some(backend) = self.backend.as_mut() else {
            // Already retired: stay silent rather than pretending to know.
            self.current = None;
            return;
        };
        match backend.focused_class() {
            // The only place a raw, server-supplied string enters the type —
            // and it goes straight through the sanitizing constructor.
            Focus::Class(raw) => self.current = ActiveApp::from_app_id(&raw),
            Focus::Unknown => self.current = None,
            Focus::Disconnected => {
                // A dead socket never recovers, and re-dialling it every
                // second from the game loop would be worse than admitting
                // defeat. Drop it; `is_connected()` now reports the truth.
                self.backend = None;
                self.current = None;
            }
        }
    }
}

/// What one query of the window server produced.
///
/// Three outcomes rather than `Result<Option<_>, _>` because the middle case —
/// "the server answered clearly that it will not name the focused window" — is
/// the *normal* case on Wayland, not an error, and conflating it with a broken
/// connection would make the watcher retire itself on a healthy session.
enum Focus {
    /// The focused window's raw `WM_CLASS` class field, exactly as it came off
    /// the wire and **not yet sanitized**. Callers must funnel it through
    /// [`ActiveApp::from_app_id`].
    Class(String),
    /// Connected and answered, but there is no window we can name: nothing is
    /// focused, the desktop is focused, the focused window is Wayland-native
    /// (so mutter offers a property-less proxy), or it disappeared between our
    /// two requests.
    Unknown,
    /// The connection is unusable. The caller must retire the backend.
    Disconnected,
}

#[cfg(target_os = "linux")]
mod backend {
    //! Linux backend: EWMH `_NET_ACTIVE_WINDOW` + ICCCM `WM_CLASS`, spoken
    //! directly over the X11 socket by `x11rb`'s pure-Rust `RustConnection`.
    //!
    //! Works fully on an X11 session, and on a Wayland session for every
    //! XWayland-hosted client. For Wayland-native clients the compositor
    //! reports a property-less proxy window and we return [`Focus::Unknown`] —
    //! see the module docs for why that is the right answer rather than
    //! something to work around.

    use x11rb::connection::Connection;
    use x11rb::errors::{ConnectionError, ReplyError};
    use x11rb::protocol::xproto::{Atom, AtomEnum, ConnectionExt, Window};
    use x11rb::rust_connection::RustConnection;

    use super::{
        ActiveAppError, Focus, PROP_NET_ACTIVE_WINDOW, PROP_WM_CLASS, READABLE_PROPERTIES,
        class_from_wm_class,
    };

    /// Bytes of `WM_CLASS` we are willing to read, expressed in the 32-bit
    /// words the X11 `GetProperty` request counts in.
    ///
    /// `WM_CLASS` is application-controlled and unbounded on the wire, so the
    /// request itself carries the bound: 64 words = 256 bytes. Every real
    /// class pair fits in a few dozen, and a hostile app that sets a megabyte
    /// of junk gets truncated by the *server* — we never allocate it.
    const MAX_WM_CLASS_WORDS: u32 = 64;

    /// `GetProperty` reply `format` for byte-string data. `WM_CLASS` is a list
    /// of 8-bit characters; anything else is a property we do not understand
    /// and will not try to interpret.
    const FORMAT_BYTES: u8 = 8;

    /// A live X11 connection plus the atoms for [`READABLE_PROPERTIES`].
    pub(super) struct Backend {
        conn: RustConnection,
        /// The screen's root window — where EWMH puts `_NET_ACTIVE_WINDOW`.
        root: Window,
        /// Interned atoms, positionally matching [`READABLE_PROPERTIES`].
        /// **This array is the entire vocabulary of things we can ask about a
        /// window.** A request is built by indexing it, so there is no way to
        /// name a property that is not in that list.
        atoms: [Atom; READABLE_PROPERTIES.len()],
    }

    /// Why a single query failed, distinguishing "ask again in a second" from
    /// "this connection is finished".
    enum QueryError {
        /// A per-request X11 error, overwhelmingly `BadWindow`: the focused
        /// window was destroyed between our two requests. Normal during window
        /// switching; the connection is healthy.
        Transient,
        /// Socket/protocol failure. Nothing will work again.
        Disconnected,
    }

    impl From<ReplyError> for QueryError {
        fn from(err: ReplyError) -> Self {
            match err {
                // The server rejected this request but is still talking to us.
                ReplyError::X11Error(_) => Self::Transient,
                ReplyError::ConnectionError(err) => err.into(),
            }
        }
    }

    impl From<ConnectionError> for QueryError {
        fn from(_: ConnectionError) -> Self {
            Self::Disconnected
        }
    }

    impl Backend {
        /// Connect to the X server named by `DISPLAY` and intern our atoms.
        pub(super) fn connect() -> Result<Self, ActiveAppError> {
            // `x11rb::connect` reads DISPLAY and XAUTHORITY itself and speaks
            // the protocol over the unix socket. No C library is involved, so
            // this cannot fail for want of libX11 — only for want of a server.
            let (conn, screen) = x11rb::connect(None)
                .map_err(|err| ActiveAppError::NoWindowServer(err.to_string()))?;

            let root = conn
                .setup()
                .roots
                .get(screen)
                .ok_or_else(|| ActiveAppError::NoWindowServer(format!("no such screen: {screen}")))?
                .root;

            // Intern every readable property up front: one round trip at
            // startup instead of one per query. `only_if_exists = false` so a
            // server that has never seen `_NET_ACTIVE_WINDOW` (no EWMH window
            // manager running yet) still yields an atom — the later
            // `GetProperty` then simply comes back empty, which is
            // `Focus::Unknown`, exactly as it should be.
            let mut atoms = [0; READABLE_PROPERTIES.len()];
            for (atom, name) in atoms.iter_mut().zip(READABLE_PROPERTIES) {
                let cookie = conn
                    .intern_atom(false, name.as_bytes())
                    .map_err(|err| ActiveAppError::NoWindowServer(err.to_string()))?;
                *atom = cookie
                    .reply()
                    .map_err(|err| ActiveAppError::NoWindowServer(err.to_string()))?
                    .atom;
            }

            Ok(Self { conn, root, atoms })
        }

        /// One full query: which window has focus, and what is its class?
        pub(super) fn focused_class(&self) -> Focus {
            match self.query() {
                Ok(Some(class)) => Focus::Class(class),
                Ok(None) => Focus::Unknown,
                // A transient X11 error is indistinguishable, from the game's
                // point of view, from "nothing nameable is focused": both mean
                // "no answer this second".
                Err(QueryError::Transient) => Focus::Unknown,
                Err(QueryError::Disconnected) => Focus::Disconnected,
            }
        }

        /// The two requests, in order. `Ok(None)` = connected but unknown.
        fn query(&self) -> Result<Option<String>, QueryError> {
            let Some(window) = self.active_window()? else {
                return Ok(None);
            };
            self.window_class(window)
        }

        /// Read `_NET_ACTIVE_WINDOW` off the root window.
        ///
        /// `Ok(None)` when the property is absent (no EWMH window manager) or
        /// explicitly zero (EWMH's "no window has focus").
        fn active_window(&self) -> Result<Option<Window>, QueryError> {
            let reply = self
                .conn
                .get_property(
                    false, // never delete the user's property
                    self.root,
                    self.atoms[PROP_NET_ACTIVE_WINDOW],
                    AtomEnum::WINDOW,
                    0, // long_offset: from the start
                    1, // long_length: one window id is all there is
                )?
                .reply()?;

            // `value32` returns None unless the reply really is 32-bit data,
            // so a wrong-typed property degrades to "unknown" rather than
            // being misread.
            let window = reply.value32().and_then(|mut ids| ids.next());
            // EWMH uses 0 (`None` in X11 terms) for "no active window", e.g.
            // while the desktop itself has focus.
            Ok(window.filter(|id| *id != 0))
        }

        /// Read `WM_CLASS` off `window` and return its class field.
        ///
        /// **The only window-specific request this module makes.** It names
        /// `self.atoms[PROP_WM_CLASS]` — there is no code path here that can
        /// ask for a title.
        fn window_class(&self, window: Window) -> Result<Option<String>, QueryError> {
            let reply = self
                .conn
                .get_property(
                    false,
                    window,
                    self.atoms[PROP_WM_CLASS],
                    // `AtomEnum::ANY` rather than `STRING`: ICCCM mandates
                    // STRING but a few toolkits publish UTF8_STRING, and
                    // demanding the exact type would silently blind us to
                    // them. The `format` check below is what keeps this safe —
                    // we still only accept byte-string data.
                    AtomEnum::ANY,
                    0,
                    MAX_WM_CLASS_WORDS,
                )?
                .reply()?;

            if reply.format != FORMAT_BYTES {
                // Absent (`format == 0`, the Wayland focus-proxy case) or some
                // 16/32-bit property we will not guess at.
                return Ok(None);
            }
            Ok(class_from_wm_class(&reply.value))
        }
    }
}

#[cfg(not(target_os = "linux"))]
mod backend {
    //! Stub backend for platforms with no active-application implementation.
    //!
    //! Compiling rather than failing to build keeps `active-app` a portable
    //! feature flag: a macOS/Windows build can enable it and simply never
    //! learns an app name, exactly as `global-input` does. The obvious future
    //! backends are `NSWorkspace.frontmostApplication` (macOS) and
    //! `GetForegroundWindow` + the process image name (Windows) — both of
    //! which can report an application identity without a window title, so
    //! this module's privacy boundary survives the port.

    use super::{ActiveAppError, Focus};

    pub(super) struct Backend;

    impl Backend {
        pub(super) fn connect() -> Result<Self, ActiveAppError> {
            Err(ActiveAppError::UnsupportedPlatform)
        }

        pub(super) fn focused_class(&self) -> Focus {
            Focus::Unknown
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    impl ActiveAppWatcher {
        /// A watcher with **no** backend at all: the exact state a watcher
        /// reaches after its connection dies, and the state the whole
        /// degradation contract is written about. Constructible on any
        /// machine, with no window server and no display.
        fn detached() -> Self {
            Self {
                backend: None,
                current: None,
                last_refresh: None,
            }
        }
    }

    // ---------------------------------------------------------------- privacy

    /// The structural privacy lock, in the spirit of
    /// `activity_events_are_content_free`: this module may read an
    /// application's *class* and nothing that carries a title.
    #[test]
    fn only_class_properties_are_readable() {
        /// Every X11 property that carries a window title, i.e. file names,
        /// document names, page titles, chat contents. Reading any of these
        /// would break the crate's privacy guarantee.
        const TITLE_PROPERTIES: [&str; 5] = [
            "WM_NAME",
            "_NET_WM_NAME",
            "_NET_WM_VISIBLE_NAME",
            "WM_ICON_NAME",
            "_NET_WM_ICON_NAME",
        ];

        for title in TITLE_PROPERTIES {
            assert!(
                !READABLE_PROPERTIES.contains(&title),
                "{title} carries the window title and must never be readable"
            );
        }

        // Belt and braces: no *future* property whose name hints at a title or
        // a location may be added either, even one not listed above.
        for readable in READABLE_PROPERTIES {
            for forbidden in ["NAME", "TITLE", "URL", "PATH", "FILE", "DOCUMENT"] {
                assert!(
                    !readable.contains(forbidden),
                    "{readable} looks like it names content, not an application"
                );
            }
        }

        // And the list itself stays exactly this short. Growing it is a
        // deliberate act that has to come here first.
        assert_eq!(
            READABLE_PROPERTIES,
            ["_NET_ACTIVE_WINDOW", "WM_CLASS"],
            "the readable-property set is the privacy boundary; changing it \
             needs a privacy review, not a test edit"
        );
    }

    #[test]
    fn active_app_has_no_field_a_title_could_reach() {
        // `from_app_id` is the only constructor, and it sanitizes. So even
        // handed a full window title — the thing we promise never to
        // capture — the struct cannot hold it verbatim: it is lowercased,
        // collapsed and hard-capped.
        let title = "main.rs - dev-companion - Visual Studio Code";
        let app = ActiveApp::from_app_id(title).expect("sanitizes to something");
        assert!(app.id().len() <= MAX_APP_ID_LEN);
        assert_ne!(app.id(), title);
        assert!(!app.id().contains(' '));
        // The display name of an unknown id is the sanitized id, never the
        // original string.
        assert_eq!(app.display(), app.id());
    }

    #[test]
    fn wm_class_parsing_ignores_everything_after_the_class() {
        // A well-formed property: instance, then class.
        assert_eq!(
            class_from_wm_class(b"code\0Code\0").as_deref(),
            Some("Code")
        );
        // A malformed one carrying a third string — a title, say. It is
        // dropped structurally, not filtered.
        assert_eq!(
            class_from_wm_class(b"code\0Code\0secret-document.txt\0").as_deref(),
            Some("Code"),
            "only the class field may ever be read out of WM_CLASS"
        );
        // One field only: it is both instance and class.
        assert_eq!(class_from_wm_class(b"kitty\0").as_deref(), Some("kitty"));
        assert_eq!(class_from_wm_class(b"kitty").as_deref(), Some("kitty"));
        // Empty / all-NUL property: no opinion.
        assert_eq!(class_from_wm_class(b""), None);
        assert_eq!(class_from_wm_class(b"\0\0\0"), None);
    }

    // ----------------------------------------------------------- sanitization

    #[test]
    fn sanitize_lowercases_and_keeps_real_app_ids_intact() {
        assert_eq!(sanitize_app_id("Code").as_deref(), Some("code"));
        assert_eq!(sanitize_app_id("code").as_deref(), Some("code"));
        assert_eq!(sanitize_app_id("Firefox").as_deref(), Some("firefox"));
        assert_eq!(sanitize_app_id("kitty").as_deref(), Some("kitty"));
        assert_eq!(
            sanitize_app_id("jetbrains-idea").as_deref(),
            Some("jetbrains-idea")
        );
        assert_eq!(
            sanitize_app_id("org.kde.konsole").as_deref(),
            Some("org.kde.konsole")
        );
        assert_eq!(
            sanitize_app_id("Gnome-terminal").as_deref(),
            Some("gnome-terminal")
        );
    }

    #[test]
    fn sanitize_strips_paths_so_no_filesystem_layout_leaks() {
        assert_eq!(
            sanitize_app_id("/usr/lib/firefox/firefox").as_deref(),
            Some("firefox")
        );
        assert_eq!(
            sanitize_app_id("/home/someone/secret-project/bin/editor").as_deref(),
            Some("editor"),
            "a path component must never survive: it names the user's disk"
        );
        // Windows-style separators too, for a future win32 backend.
        assert_eq!(
            sanitize_app_id(r"C:\Program Files\Foo\foo.exe").as_deref(),
            Some("foo.exe")
        );
        // A trailing separator leaves nothing to name.
        assert_eq!(sanitize_app_id("/usr/bin/"), None);
    }

    #[test]
    fn sanitize_collapses_disallowed_characters_into_single_dashes() {
        assert_eq!(
            sanitize_app_id("Google Chrome").as_deref(),
            Some("google-chrome"),
            "words must stay separated, not be mashed together"
        );
        assert_eq!(
            sanitize_app_id("Visual   Studio\tCode").as_deref(),
            Some("visual-studio-code"),
            "a run of junk collapses to one dash"
        );
        // Control bytes, ANSI escapes and other terminal-hostile input: gone.
        assert_eq!(
            sanitize_app_id("co\u{1b}[31mde\n\0").as_deref(),
            Some("co-31mde")
        );
        // Non-ASCII is not lowercasable and not allowed, so it is a separator.
        assert_eq!(sanitize_app_id("Kalkül").as_deref(), Some("kalk-l"));
        // Leading and trailing junk never produces a stray dash.
        assert_eq!(sanitize_app_id("  code  ").as_deref(), Some("code"));
        assert_eq!(sanitize_app_id("--code--").as_deref(), Some("code"));
        assert_eq!(sanitize_app_id("...code...").as_deref(), Some("code"));
    }

    #[test]
    fn sanitize_caps_length_and_never_ends_in_a_separator() {
        let long = "a".repeat(200);
        let out = sanitize_app_id(&long).expect("plain letters survive");
        assert_eq!(out.len(), MAX_APP_ID_LEN);

        // The cap must also hold for input that is *mostly* junk, where the
        // collapsing pass generates dashes of its own.
        let noisy = "Some Very Long Window Title That Goes On And On For Ever";
        let out = sanitize_app_id(noisy).expect("collapses to something");
        assert_eq!(out.len(), MAX_APP_ID_LEN);
        assert!(
            !out.ends_with('-'),
            "truncation must not strand a separator: {out}"
        );

        // A cap that lands exactly on a dash still yields a clean id.
        let boundary = format!("{}-tail", "b".repeat(MAX_APP_ID_LEN - 1));
        let out = sanitize_app_id(&boundary).expect("letters survive");
        assert!(out.len() <= MAX_APP_ID_LEN);
        assert!(!out.ends_with('-'), "{out}");
    }

    #[test]
    fn sanitize_output_is_always_in_the_allowed_charset() {
        // Every id the HUD can be handed, from any input, is boring ASCII.
        for raw in [
            "Code",
            "/usr/bin/Firefox",
            "Google Chrome",
            "org.kde.Konsole",
            "co\u{1b}[31mde",
            "Kalkül",
            "日本語のアプリ",
            "a".repeat(500).as_str(),
            "\u{202e}reversed",
            "app;rm -rf ~",
        ] {
            if let Some(id) = sanitize_app_id(raw) {
                assert!(
                    id.chars().all(is_allowed),
                    "sanitize({raw:?}) produced {id:?}, which escapes [a-z0-9._-]"
                );
                assert!(id.len() <= MAX_APP_ID_LEN, "sanitize({raw:?}) = {id:?}");
                assert!(!id.is_empty());
            }
        }
    }

    #[test]
    fn sanitize_returns_none_when_nothing_nameable_is_left() {
        assert_eq!(sanitize_app_id(""), None);
        assert_eq!(sanitize_app_id("   "), None);
        assert_eq!(sanitize_app_id("---"), None);
        assert_eq!(sanitize_app_id("日本語"), None, "no ASCII to keep");
        assert_eq!(ActiveApp::from_app_id(""), None);
        assert_eq!(ActiveApp::from_app_id("!!!"), None);
    }

    // ------------------------------------------------------- friendly display

    #[test]
    fn display_names_map_known_ids_and_fall_back_honestly() {
        assert_eq!(display_name("code"), "VS Code");
        assert_eq!(display_name("firefox"), "Firefox");
        assert_eq!(display_name("org.kde.konsole"), "Konsole");
        assert_eq!(display_name("kitty"), "kitty");
        assert_eq!(display_name("jetbrains-idea"), "IntelliJ");
        // Unknown ids render as themselves: truthful, never invented.
        assert_eq!(display_name("some-unknown-editor"), "some-unknown-editor");
        assert_eq!(display_name(""), "");

        // And through the real constructor.
        let app = ActiveApp::from_app_id("Code").expect("valid");
        assert_eq!(app.id(), "code");
        assert_eq!(app.display(), "VS Code");
    }

    #[test]
    fn friendly_map_keys_are_valid_sanitized_ids() {
        // A key that could never come out of `sanitize_app_id` is dead weight
        // that would silently never match — e.g. an upper-case or spaced key.
        for (id, name) in FRIENDLY_NAMES {
            assert_eq!(
                sanitize_app_id(id).as_deref(),
                Some(*id),
                "{id} is not a sanitized id, so it can never be looked up"
            );
            assert!(!name.is_empty(), "{id} maps to an empty display name");
        }
        // Sorted by id: keeps the table reviewable and duplicate-free.
        let mut sorted: Vec<&str> = FRIENDLY_NAMES.iter().map(|(id, _)| *id).collect();
        let original = sorted.clone();
        sorted.sort_unstable();
        sorted.dedup();
        assert_eq!(sorted, original, "FRIENDLY_NAMES must be sorted and unique");
    }

    // --------------------------------------------------- caching / degradation

    #[test]
    fn refresh_cadence_is_at_most_once_per_interval() {
        let start = Instant::now();
        // Never queried: must query.
        assert!(needs_refresh(None, start));
        // Just queried: must not.
        assert!(!needs_refresh(Some(start), start));
        assert!(!needs_refresh(
            Some(start),
            start + REFRESH_INTERVAL - Duration::from_millis(1)
        ));
        // A full interval later: query again.
        assert!(needs_refresh(Some(start), start + REFRESH_INTERVAL));
        assert!(needs_refresh(Some(start), start + Duration::from_secs(60)));
        // A clock that appears to go backwards (an `Instant` from before the
        // last refresh) must not be read as "an eternity has passed".
        assert!(!needs_refresh(Some(start + Duration::from_secs(5)), start));
    }

    #[test]
    fn a_watcher_with_no_backend_reports_unknown_forever() {
        // The explicit degradation contract: when neither the compositor nor
        // X11 will name the focused window, the game gets `None` — not a
        // guess, not a panic — every time it asks.
        let mut watcher = ActiveAppWatcher::detached();
        assert!(!watcher.is_connected());
        for _ in 0..1000 {
            assert_eq!(watcher.current_app(), None);
            assert_eq!(watcher.current_app_display(), None);
            assert!(watcher.current().is_none());
        }
        assert!(!watcher.is_connected());
    }

    #[test]
    fn refresh_retires_a_disconnected_backend_and_clears_the_cache() {
        // Drive the state machine `refresh` implements without a window
        // server: a cached answer plus a dead connection must collapse to
        // "unknown", permanently.
        let mut watcher = ActiveAppWatcher::detached();
        // Seed the cache exactly as a successful refresh would have.
        watcher.current = ActiveApp::from_app_id("code");
        watcher.last_refresh = Some(Instant::now());

        // A fresh cache is served without touching the backend at all —
        // which is why this returns an app name despite there being no
        // connection.
        assert_eq!(watcher.current_app().as_deref(), Some("code"));
        assert_eq!(watcher.current_app_display().as_deref(), Some("VS Code"));

        // Once it goes stale, the refresh finds no backend and reports the
        // truth, rather than leaving a stale app name on screen forever.
        // `checked_sub` so a machine whose monotonic clock started moments ago
        // cannot panic here; `None` would mean "never refreshed", which also
        // forces a refresh.
        watcher.last_refresh = Instant::now().checked_sub(REFRESH_INTERVAL * 2);
        assert_eq!(watcher.current_app(), None);
        assert!(!watcher.is_connected());

        // And a watcher that has never queried must not serve a phantom
        // cache: the first call always refreshes.
        let mut fresh = ActiveAppWatcher::detached();
        fresh.current = ActiveApp::from_app_id("firefox");
        assert_eq!(fresh.current_app(), None);
    }

    #[test]
    fn cached_answers_are_cheap_enough_for_a_per_frame_call() {
        // `current_app` is called from a game system; the cached path must not
        // touch the network or a lock. A generous bound: this only fails if
        // someone puts I/O on the hot path.
        let mut watcher = ActiveAppWatcher::detached();
        watcher.current = ActiveApp::from_app_id("code");
        watcher.last_refresh = Some(Instant::now());

        let start = Instant::now();
        for _ in 0..100_000 {
            let _ = watcher.current();
        }
        let elapsed = start.elapsed();
        assert!(
            elapsed < Duration::from_secs(1),
            "100k cached reads took {elapsed:?} — the hot path is doing real work"
        );
    }

    #[test]
    fn new_degrades_gracefully_instead_of_panicking() {
        // Runs on a dev box with a display (where it connects) and in a
        // headless container (where it must fail cleanly). Either way: no
        // panic, no hang, and no window title anywhere in the outcome.
        match ActiveAppWatcher::new() {
            Ok(mut watcher) => {
                assert!(watcher.is_connected());
                // May legitimately be `None` — a Wayland-native window has
                // focus, or nothing does. What must hold is that whatever
                // comes back is a sanitized id.
                if let Some(id) = watcher.current_app() {
                    assert!(id.chars().all(is_allowed), "unsanitized id: {id:?}");
                    assert!(id.len() <= MAX_APP_ID_LEN);
                    assert!(watcher.current_app_display().is_some());
                }
            }
            Err(err) => {
                // A caller-actionable message, and no panic path.
                assert!(!err.to_string().is_empty());
            }
        }
    }

    #[test]
    fn error_messages_are_useful_and_carry_no_window_data() {
        for err in [
            ActiveAppError::UnsupportedPlatform,
            ActiveAppError::NoWindowServer("connection refused".to_owned()),
        ] {
            let message = err.to_string();
            assert!(!message.is_empty());
            // Errors are raised before any window is inspected, so there is
            // nothing user-visible to leak; assert the shape stays that way.
            assert!(!message.contains("WM_NAME"));
        }
    }
}
