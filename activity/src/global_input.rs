//! OS-global activity source (plan §3.1, "swapping in a real OS-global
//! listener later"), behind the `global-input` Cargo feature.
//!
//! [`FocusedWindowProvider`](crate::FocusedWindowProvider) only sees input
//! delivered to the companion's *own* window, which is exactly the wrong
//! window: while you are coding, your editor has focus and the game has none,
//! so nothing is ever counted. [`GlobalInputProvider`] closes that gap by
//! observing keyboard/mouse activity at the OS level, regardless of which
//! window is focused.
//!
//! Three properties are load-bearing here and are enforced by construction,
//! not by convention:
//!
//! 1. **No Bevy.** This module is plain Rust, like the rest of the crate. The
//!    game still only ever sees [`ActivityEvent`]s.
//! 2. **Privacy.** The OS hook hands us a raw `(type, code, value)` triple per
//!    event. The `code` (i.e. *which* key) is used only to classify the event
//!    into a content-free [`ActivityEvent`] variant, in one expression, and is
//!    then dropped. It never reaches a field, a log line, or a return value.
//!    Nothing but two `usize` counters crosses the thread boundary.
//! 3. **Graceful degradation.** Construction either returns a working provider
//!    or a [`GlobalInputError`]. It never panics and never blocks waiting for
//!    permission, so the caller can fall back to `FocusedWindowProvider`.
//!
//! ## Why `evdev` and not `rdev`
//!
//! `rdev` is the usual cross-platform pick and is what plan §3.1 guessed at,
//! but evaluating both at this milestone (as the plan asks) rules it out for
//! our actual target:
//!
//! * **It cannot see the events we need under Wayland.** `rdev`'s Linux
//!   backend is X11 `XRecord`. On a Wayland session (this project's dev
//!   machine included) `XRecord` observes only XWayland clients, so a
//!   Wayland-native editor's keystrokes — the whole point of the feature —
//!   are invisible. `evdev` reads the kernel's `/dev/input/event*` character
//!   devices, which sit *below* the display server: X11, Wayland and even a
//!   bare TTY all work identically.
//! * **It drags in a C toolchain.** `rdev` depends on the `x11` crate, whose
//!   build script `pkg-config`s `x11`/`xtst` and *panics* if the dev packages
//!   are missing — which is what happens on this machine (no `sudo`, no
//!   `libxtst-dev`), so even `cargo check --features global-input` would fail.
//!   `evdev` is pure Rust over `libc`/`nix` ioctls: no C headers, no
//!   `pkg-config`, no link-time system dependency.
//! * **Nothing here needs `rdev`'s extra surface.** `rdev` also *synthesises*
//!   input and reports key identity and cursor coordinates; we deliberately
//!   want none of that (see the privacy invariant above). The narrower crate
//!   is the safer dependency.
//!
//! The cost of that choice is that `evdev` is **Linux-only**. That is handled
//! explicitly rather than by a compile error: the dependency is declared only
//! for `cfg(target_os = "linux")`, and on any other platform
//! [`GlobalInputProvider::new`] compiles fine and returns
//! [`GlobalInputError::UnsupportedPlatform`], so a macOS/Windows build falls
//! back to `FocusedWindowProvider` today and can gain a `CGEventTap` /
//! `SetWindowsHookEx` backend later behind this same feature and this same
//! public API.
//!
//! ## Linux permission requirements
//!
//! Reading `/dev/input/event*` requires read access to those device nodes,
//! which are `root:input` mode `0660` on every mainstream distro. In practice
//! that means **the user must be in the `input` group**
//! (`sudo usermod -aG input "$USER"`, then re-login), or a udev rule must
//! grant access. No `sudo`, no setuid and no capability is needed at runtime,
//! and the devices are only ever *read* — never `grab()`bed, so input is never
//! stolen from the focused application.
//!
//! When that access is missing (or in a headless CI container with no input
//! devices at all), `evdev`'s enumeration simply yields nothing openable and
//! [`GlobalInputProvider::new`] returns
//! [`GlobalInputError::NoReadableInputDevices`] — the graceful-degradation
//! path, exercised by a unit test that runs with or without permission.

use std::fmt;
use std::io;
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};
use std::thread::JoinHandle;

use crate::{ActivityEvent, ActivityProvider};

/// Why the OS-global hook could not be started.
///
/// Every variant is a "fall back to `FocusedWindowProvider`" signal rather
/// than a fatal error; none of them carries anything about *what* the user
/// typed, because the hook never got far enough to observe input (and would
/// not record it anyway).
#[derive(Debug)]
pub enum GlobalInputError {
    /// This platform has no OS-global backend compiled in (i.e. anything that
    /// is not Linux, for now). Deliberately an error rather than a compile
    /// failure so a cross-platform build can enable the feature and fall back.
    UnsupportedPlatform,
    /// `/dev/input` yielded no keyboard/mouse device we could open for
    /// reading. Almost always a permission problem: see the module docs — the
    /// user usually needs to be in the `input` group. Also the expected
    /// outcome in a headless CI container with no input devices.
    NoReadableInputDevices,
    /// The hook thread itself could not be spawned (the OS refused). Kept
    /// distinct because, unlike the others, retrying may help.
    HookThreadFailed(io::Error),
}

impl fmt::Display for GlobalInputError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::UnsupportedPlatform => f.write_str(
                "OS-global input monitoring is not implemented for this platform \
                 (Linux only for now)",
            ),
            Self::NoReadableInputDevices => f.write_str(
                "no readable keyboard/mouse device in /dev/input — the user \
                 usually needs to be in the `input` group (usermod -aG input \
                 \"$USER\", then re-login)",
            ),
            Self::HookThreadFailed(err) => {
                write!(f, "could not spawn the global input hook thread: {err}")
            }
        }
    }
}

impl std::error::Error for GlobalInputError {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            Self::HookThreadFailed(err) => Some(err),
            Self::UnsupportedPlatform | Self::NoReadableInputDevices => None,
        }
    }
}

/// The content-free tally shared between the OS hook thread (which only
/// increments) and the game thread (which only drains).
///
/// This is the *entire* payload that crosses the thread boundary: two counts.
/// Not a queue of events, not a queue of keycodes — there is structurally
/// nowhere for key identity to be smuggled through, which is the point.
///
/// Atomics rather than a `Mutex` so the hook thread can never make
/// [`ActivityProvider::poll`] — called every frame from the game loop — wait
/// on a lock.
#[derive(Debug, Default)]
struct ActivityTally {
    keystrokes: AtomicUsize,
    mouse_moves: AtomicUsize,
}

/// The **hook side** of the tally: the only writes the OS thread may make.
///
/// `allow(dead_code)` off Linux is deliberate rather than a leftover: on a
/// platform with no backend compiled in (see the stub `backend` below) nothing
/// but the unit tests calls these, and deleting them would delete the API the
/// next backend has to implement against.
#[cfg_attr(not(target_os = "linux"), allow(dead_code))]
impl ActivityTally {
    /// Upper bound on the events of one kind that may sit undrained here.
    ///
    /// The hook thread keeps counting even if the game stops polling (window
    /// minimised, main loop stalled, a debugger breakpoint). Capping keeps the
    /// `Vec` that [`ActivityProvider::poll`] eventually allocates bounded, and
    /// the cap is far above [`crate::MAX_RECENT_RATE`] × any sane frame time,
    /// so it cannot distort the rate meter during normal play.
    const MAX_PENDING_PER_KIND: usize = 4096;

    /// Count one keypress.
    fn record_keystroke(&self) {
        Self::bump(&self.keystrokes);
    }

    /// Count one mouse movement (already coalesced by the caller).
    fn record_mouse_move(&self) {
        Self::bump(&self.mouse_moves);
    }

    /// Increment `counter`, saturating at [`Self::MAX_PENDING_PER_KIND`].
    fn bump(counter: &AtomicUsize) {
        // `fetch_update` rather than a load/compare/`fetch_add`: there is only
        // one writer today (the hook thread), but the cap must hold even if a
        // future backend uses one thread per device.
        let _ = counter.fetch_update(Ordering::AcqRel, Ordering::Acquire, |count| {
            (count < Self::MAX_PENDING_PER_KIND).then_some(count + 1)
        });
    }
}

/// The **game side** of the tally: the only read the game thread makes.
impl ActivityTally {
    /// Take everything counted since the last drain, resetting the counts.
    ///
    /// Ordering within the returned vec is not meaningful: the tally stores
    /// counts, so keystrokes come before mouse moves. That is fine because
    /// every consumer of a poll treats the result as a multiset (see
    /// [`crate::decay_and_accumulate`], which only reads `len()`).
    fn drain(&self) -> Vec<ActivityEvent> {
        let keystrokes = self.keystrokes.swap(0, Ordering::AcqRel);
        let mouse_moves = self.mouse_moves.swap(0, Ordering::AcqRel);
        let mut events = Vec::with_capacity(keystrokes + mouse_moves);
        events.extend(std::iter::repeat_n(ActivityEvent::Keystroke, keystrokes));
        events.extend(std::iter::repeat_n(ActivityEvent::MouseMoved, mouse_moves));
        events
    }
}

/// An [`ActivityProvider`] fed by an **OS-level** keyboard/mouse hook, so it
/// counts the user's activity in their editor — not just in the game window.
///
/// Construct with [`GlobalInputProvider::new`], which fails (rather than
/// panics) whenever the hook cannot be started; callers are expected to fall
/// back to [`FocusedWindowProvider`](crate::FocusedWindowProvider) on
/// failure.
///
/// The hook runs on its own thread and only ever increments two counters;
/// [`ActivityProvider::poll`] swaps those counters to zero and returns that
/// many content-free events, so polling is allocation-only and never blocks
/// the game loop.
///
/// Unlike `FocusedWindowProvider` this provider never emits
/// [`ActivityEvent::FocusChanged`]: "is *our* window focused" is a
/// window-manager question, and irrelevant to a source that is global by
/// definition. Dropping the provider stops the hook thread.
#[derive(Debug)]
pub struct GlobalInputProvider {
    /// Shared with the hook thread; drained by `poll`.
    tally: Arc<ActivityTally>,
    /// Set on drop to ask the hook thread to finish its current sweep and exit.
    stop: Arc<AtomicBool>,
    /// `None` only in unit tests, which exercise the tally/drain logic with no
    /// OS hook at all.
    hook: Option<JoinHandle<()>>,
    /// How many input devices the hook is watching. Reported for the caller's
    /// startup log; a count, never a device identity.
    watched_devices: usize,
}

impl GlobalInputProvider {
    /// Start observing OS-global keyboard/mouse activity.
    ///
    /// # Errors
    ///
    /// Returns [`GlobalInputError`] — never panics, never blocks on a
    /// permission prompt — if the platform has no backend, if no input device
    /// can be opened for reading (the usual "user is not in the `input`
    /// group" case), or if the hook thread cannot be spawned. Callers should
    /// treat any error as "use `FocusedWindowProvider` instead".
    pub fn new() -> Result<Self, GlobalInputError> {
        let tally = Arc::new(ActivityTally::default());
        let stop = Arc::new(AtomicBool::new(false));
        let hook = spawn_hook(Arc::clone(&tally), Arc::clone(&stop))?;
        Ok(Self {
            tally,
            stop,
            hook: Some(hook.handle),
            watched_devices: hook.watched_devices,
        })
    }

    /// How many OS input devices the hook is watching.
    ///
    /// Useful for a one-line startup log ("global input active, 3 devices").
    /// A count only — device names are never exposed, per the privacy
    /// invariant.
    #[must_use]
    pub fn watched_devices(&self) -> usize {
        self.watched_devices
    }
}

impl ActivityProvider for GlobalInputProvider {
    fn poll(&mut self) -> Vec<ActivityEvent> {
        // Two atomic swaps plus one allocation: no lock, no I/O, no waiting on
        // the hook thread. Safe to call every frame.
        self.tally.drain()
    }
}

impl Drop for GlobalInputProvider {
    fn drop(&mut self) {
        self.stop.store(true, Ordering::Release);
        if let Some(hook) = self.hook.take() {
            // The thread checks the flag once per sweep, so this waits at most
            // ~SWEEP_INTERVAL. A panicked thread yields `Err`, which is
            // deliberately ignored: a drop must not panic.
            let _ = hook.join();
        }
    }
}

/// A started hook: the thread to join on drop, plus how many devices it
/// watches.
struct Hook {
    handle: JoinHandle<()>,
    watched_devices: usize,
}

#[cfg(target_os = "linux")]
mod backend {
    //! Linux backend: read the kernel's evdev character devices directly, so
    //! the display server (X11, Wayland, none) is irrelevant.

    use std::sync::Arc;
    use std::sync::atomic::{AtomicBool, Ordering};
    use std::thread;
    use std::time::Duration;

    use evdev::{Device, EventType, InputEvent, KeyCode, RelativeAxisCode};

    use super::{ActivityTally, GlobalInputError, Hook};

    /// How long the hook thread sleeps between sweeps of the input devices.
    ///
    /// Roughly one 60 fps frame, for three reasons: it bounds the latency of a
    /// keystroke reaching the game to about one frame (nothing is ever *lost* —
    /// the kernel buffers events until we read them), it bounds how long
    /// dropping the provider waits for the thread to see the stop flag, and it
    /// is the window over which mouse motion is coalesced in [`sweep`].
    const SWEEP_INTERVAL: Duration = Duration::from_millis(16);

    /// `input_event.value` for a key *press*.
    ///
    /// Kernel key events carry `1` = press, `2` = auto-repeat (key held down),
    /// `0` = release. Counting only presses means one physical keypress is one
    /// [`crate::ActivityEvent::Keystroke`], and leaning on a key does not fake
    /// activity — consistent with the anti-mashing intent of plan §1.
    const KEY_PRESS: i32 = 1;

    /// First event code that is **not** a keyboard key.
    ///
    /// `linux/input-event-codes.h` lays out `KEY_*` in `0x000..0x100` and then
    /// the `BTN_*` block from `BTN_MISC`/`BTN_0` = `0x100` (mouse buttons,
    /// gamepad buttons, tablet tips…). Comparing a code against this single
    /// threshold answers "was this a typing key?" without learning *which*
    /// key — see [`record_event`].
    const FIRST_NON_KEYBOARD_CODE: u16 = KeyCode::BTN_0.code();

    /// Open every keyboard/mouse-ish device we are allowed to read and start
    /// the sweep thread.
    pub(super) fn spawn_hook(
        tally: Arc<ActivityTally>,
        stop: Arc<AtomicBool>,
    ) -> Result<Hook, GlobalInputError> {
        // `evdev::enumerate` silently skips device nodes it cannot open, which
        // is exactly the behaviour we want: without the `input` group this
        // yields nothing and we report NoReadableInputDevices instead of
        // failing hard on the first EACCES.
        let mut devices: Vec<Device> = evdev::enumerate()
            .map(|(_path, device)| device)
            .filter(is_of_interest)
            // Non-blocking so one unused device can never stall the sweep (and
            // so the stop flag is always observed within one sweep). A device
            // whose flags cannot be set is dropped rather than risking a
            // blocking read.
            .filter(|device| device.set_nonblocking(true).is_ok())
            .collect();

        if devices.is_empty() {
            return Err(GlobalInputError::NoReadableInputDevices);
        }
        let watched_devices = devices.len();

        let handle = thread::Builder::new()
            .name("activity-global-input".to_owned())
            .spawn(move || {
                while !stop.load(Ordering::Acquire) {
                    sweep(&mut devices, &tally);
                    if devices.is_empty() {
                        // Every device vanished (unplugged, or the session
                        // ended). Nothing left to read, so exit instead of
                        // spinning; `poll` keeps working and simply returns
                        // nothing.
                        break;
                    }
                    thread::sleep(SWEEP_INTERVAL);
                }
            })
            .map_err(GlobalInputError::HookThreadFailed)?;

        Ok(Hook {
            handle,
            watched_devices,
        })
    }

    /// Is this device one that can report typing or mouse movement?
    ///
    /// Capability bits only (`EV_KEY` / `EV_REL`) — deliberately inclusive,
    /// because irrelevant events from an included device are filtered per
    /// event in [`record_event`] anyway, whereas excluding a real keyboard
    /// would silently break the feature.
    fn is_of_interest(device: &Device) -> bool {
        let events = device.supported_events();
        events.contains(EventType::KEY) || events.contains(EventType::RELATIVE)
    }

    /// Drain whatever the kernel has buffered on each device into `tally`.
    fn sweep(devices: &mut Vec<Device>, tally: &ActivityTally) {
        // Mouse motion is coalesced to at most one MouseMoved per sweep. A
        // 1000 Hz gaming mouse emits an EV_REL pair per poll, which would
        // otherwise pin `recent_rate` at its ceiling and drown out typing.
        // One per ~frame also matches what `FocusedWindowProvider` sees, since
        // Bevy reports mouse motion per frame.
        let mut mouse_moved = false;

        devices.retain_mut(|device| match device.fetch_events() {
            Ok(events) => {
                for event in events {
                    record_event(&event, tally, &mut mouse_moved);
                }
                true
            }
            // Nothing buffered (the normal case), or a signal interrupted the
            // read: keep the device.
            Err(err)
                if matches!(
                    err.kind(),
                    std::io::ErrorKind::WouldBlock | std::io::ErrorKind::Interrupted
                ) =>
            {
                true
            }
            // Anything else (ENODEV from an unplugged keyboard, EIO from a
            // suspended device) is permanent for this fd: drop it, or the
            // sweep would busy-fail on it forever.
            Err(_) => false,
        });

        if mouse_moved {
            tally.record_mouse_move();
        }
    }

    /// Classify one raw kernel event into the tally.
    ///
    /// The whole classification is these three arms, and all either arm can do
    /// is add `1` to a counter — there is no branch in which anything about the
    /// event survives. EV_MSC scancodes (which *do* carry key identity), EV_ABS
    /// coordinates, LEDs and force feedback are all ignored outright.
    fn record_event(event: &InputEvent, tally: &ActivityTally, mouse_moved: &mut bool) {
        match event.event_type() {
            EventType::KEY if is_keyboard_press(event) => tally.record_keystroke(),
            EventType::RELATIVE if is_pointer_motion(event) => *mouse_moved = true,
            _ => {}
        }
    }

    /// Is this EV_KEY event a *press* of a *keyboard* key?
    ///
    /// **Privacy-critical.** `event.code()` — i.e. *which* key — is read here
    /// and only here, compared against one constant, and dropped when this
    /// function returns a `bool`. Nothing derived from it is stored, logged or
    /// returned. Keeping the code (or a keysym, or the typed character) would
    /// break the crate's stated privacy guarantee.
    ///
    /// Auto-repeat (`value == 2`) and releases (`0`) are not new activity, so
    /// leaning on a key does not fake typing. `BTN_*` codes are mouse/gamepad
    /// buttons and are excluded: there is no content-free `ActivityEvent` for
    /// a click, and inventing one is not this milestone's job.
    fn is_keyboard_press(event: &InputEvent) -> bool {
        event.value() == KEY_PRESS && event.code() < FIRST_NON_KEYBOARD_CODE
    }

    /// Is this EV_REL event pointer movement?
    ///
    /// Axis identity only — the delta *value* is never read, because a cursor
    /// position is content we do not want. Wheel/scroll axes are excluded to
    /// stay in parity with `FocusedWindowProvider`, which is fed from Bevy's
    /// `MouseMotion` alone.
    fn is_pointer_motion(event: &InputEvent) -> bool {
        let axis = event.code();
        axis == RelativeAxisCode::REL_X.0 || axis == RelativeAxisCode::REL_Y.0
    }
}

#[cfg(not(target_os = "linux"))]
mod backend {
    //! Stub backend for platforms with no global hook implementation yet.
    //!
    //! Compiling (rather than failing to build) keeps `global-input` a
    //! portable feature flag: a macOS/Windows build can enable it and simply
    //! falls back to `FocusedWindowProvider` at runtime.

    use super::{ActivityTally, Arc, AtomicBool, GlobalInputError, Hook};

    pub(super) fn spawn_hook(
        _tally: Arc<ActivityTally>,
        _stop: Arc<AtomicBool>,
    ) -> Result<Hook, GlobalInputError> {
        Err(GlobalInputError::UnsupportedPlatform)
    }
}

// Whichever of the two backends above was compiled for this target supplies
// `spawn_hook`; everything outside them is platform-independent.
use backend::spawn_hook;

#[cfg(test)]
mod tests {
    use std::time::Duration;

    use super::*;

    impl GlobalInputProvider {
        /// A provider with **no** OS hook, sharing its tally with the caller.
        ///
        /// Lets the tests drive the exact counter/drain path the real hook
        /// uses (`ActivityTally` → `ActivityProvider::poll`) on any machine,
        /// with no devices, no permissions and no thread.
        fn detached() -> (Self, Arc<ActivityTally>) {
            let tally = Arc::new(ActivityTally::default());
            let provider = Self {
                tally: Arc::clone(&tally),
                stop: Arc::new(AtomicBool::new(false)),
                hook: None,
                watched_devices: 0,
            };
            (provider, tally)
        }
    }

    #[test]
    fn poll_drains_the_shared_tally_then_returns_empty() {
        let (mut provider, tally) = GlobalInputProvider::detached();
        assert!(provider.poll().is_empty(), "a fresh tally polls empty");

        // Stand in for the hook thread.
        tally.record_keystroke();
        tally.record_keystroke();
        tally.record_mouse_move();
        tally.record_keystroke();

        let events = provider.poll();
        assert_eq!(
            events
                .iter()
                .filter(|e| **e == ActivityEvent::Keystroke)
                .count(),
            3
        );
        assert_eq!(
            events
                .iter()
                .filter(|e| **e == ActivityEvent::MouseMoved)
                .count(),
            1
        );
        assert_eq!(events.len(), 4, "nothing else is ever emitted: {events:?}");

        // Drain semantics, matching FocusedWindowProvider.
        assert!(provider.poll().is_empty());

        // Counting resumes after a drain.
        tally.record_keystroke();
        assert_eq!(provider.poll(), vec![ActivityEvent::Keystroke]);
    }

    #[test]
    fn tally_is_capped_so_an_unpolled_provider_cannot_grow_without_bound() {
        let (mut provider, tally) = GlobalInputProvider::detached();
        for _ in 0..(ActivityTally::MAX_PENDING_PER_KIND + 500) {
            tally.record_keystroke();
            tally.record_mouse_move();
        }

        let events = provider.poll();
        assert_eq!(events.len(), 2 * ActivityTally::MAX_PENDING_PER_KIND);
        // The cap is per kind, so neither kind starves the other.
        assert_eq!(
            events
                .iter()
                .filter(|e| **e == ActivityEvent::Keystroke)
                .count(),
            ActivityTally::MAX_PENDING_PER_KIND
        );
    }

    #[test]
    fn poll_returns_promptly_and_never_blocks() {
        // `poll` is called every frame from the game loop; it must not wait on
        // the hook thread. A generous bound — this only has to fail if someone
        // introduces a lock or an I/O read into the poll path.
        let (mut provider, tally) = GlobalInputProvider::detached();
        for _ in 0..1000 {
            tally.record_keystroke();
        }
        let start = std::time::Instant::now();
        let events = provider.poll();
        let elapsed = start.elapsed();
        assert_eq!(events.len(), 1000);
        assert!(
            elapsed < Duration::from_millis(50),
            "poll took {elapsed:?} — it must not block the game loop"
        );
    }

    #[test]
    fn global_events_are_content_free() {
        // The privacy invariant for *this* provider, in the spirit of
        // `activity_events_are_content_free`: the hook can only ever produce
        // the two payload-free variants, and `ActivityEvent` still has nowhere
        // to put a key identity.
        let (mut provider, tally) = GlobalInputProvider::detached();
        tally.record_keystroke();
        tally.record_mouse_move();
        for event in provider.poll() {
            // Exhaustive by variant *and* by payload: adding a key-identity
            // field (`Keystroke(KeyCode)`) or a coordinate
            // (`MouseMoved { x, y }`) stops this compiling, and adding a new
            // variant the hook can emit stops it passing.
            match event {
                ActivityEvent::Keystroke | ActivityEvent::MouseMoved => {}
                ActivityEvent::FocusChanged(_) => {
                    panic!("a global provider has no window focus to report")
                }
            }
        }

        // A key identity cannot fit: the enum is a bare discriminant plus the
        // FocusChanged bool. A `KeyCode`/`u16`/`String` field would grow this.
        assert_eq!(std::mem::size_of::<ActivityEvent>(), 1);
    }

    #[test]
    fn new_degrades_gracefully_instead_of_panicking() {
        // Runs on a dev box (where it starts a real hook) and in a headless CI
        // container with no /dev/input access (where it must fail cleanly).
        // Either way: no panic, no hang.
        match GlobalInputProvider::new() {
            Ok(mut provider) => {
                assert!(
                    provider.watched_devices() > 0,
                    "a successful hook watches at least one device"
                );
                // The real hook thread is running now; poll must still be
                // immediate, and dropping must stop the thread without a hang.
                let _ = provider.poll();
            }
            Err(err) => {
                // A caller-actionable message, and no panic path.
                assert!(!err.to_string().is_empty());
            }
        }
    }
}
