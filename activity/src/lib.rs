//! dev-companion — activity monitoring boundary.
//!
//! This crate is deliberately **plain Rust with no Bevy dependency** and no
//! knowledge of the game. It is the single abstraction boundary through which
//! real-world user activity enters the `companion` game crate.
//!
//! The game never reads OS input or editor/git state directly — it only
//! consumes [`ActivityEvent`]s drained from an [`ActivityProvider`] trait
//! object. This is what lets VS Code / Git / AI-agent signals be added later
//! as new event producers without touching any game system (see
//! `docs/implementation-plan.md` §3.1).
//!
//! Privacy invariant: events carry **counts and focus transitions only** —
//! never key identity, text, or window titles.
//!
//! Two providers exist. [`FocusedWindowProvider`] sees only input delivered to
//! the companion's own window, so it counts nothing while you are typing in
//! your editor; `GlobalInputProvider` watches OS-level keyboard/mouse
//! activity regardless of focus and is the one that makes the premise work.
//! The latter needs an OS hook and platform-specific permissions, so it lives
//! behind the **`global-input`** Cargo feature (off by default — a default
//! build does not even download the backend crate) and its constructor returns
//! an error rather than panicking when the hook cannot start, so callers fall
//! back to [`FocusedWindowProvider`]. See the `global_input` module for that
//! trade-off in full.
//!
//! A third signal sits alongside them and deliberately does *not* implement
//! [`ActivityProvider`]: `ActiveAppWatcher` (behind the **`active-app`**
//! feature) answers "*where* is the user working?" rather than "how much?", so
//! the game can label the session "Coding in VS Code" instead of inventing a
//! project name. It is a pull-based query, not a stream of events, which is
//! why it is a separate type rather than a fourth [`ActivityEvent`] variant —
//! and the privacy invariant above applies to it in its strictest form: it
//! captures the **application identity only** (`code`, `firefox`), never a
//! window title. See the `active_app` module for how that is enforced
//! structurally.

use std::collections::VecDeque;
use std::time::Duration;

#[cfg(feature = "active-app")]
pub mod active_app;
#[cfg(feature = "global-input")]
pub mod global_input;

#[cfg(feature = "active-app")]
pub use active_app::{ActiveApp, ActiveAppError, ActiveAppWatcher};
#[cfg(feature = "global-input")]
pub use global_input::{GlobalInputError, GlobalInputProvider};

/// A single unit of user activity observed since the last poll.
///
/// Intentionally content-free: it conveys *that* activity happened and its
/// kind, never *what* was typed or clicked.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ActivityEvent {
    /// A key was pressed. Carries no key identity.
    Keystroke,
    /// The mouse moved. Carries no cursor position.
    MouseMoved,
    /// Window focus changed. `true` = now focused, `false` = unfocused.
    FocusChanged(bool),
}

/// A source of activity events.
///
/// Implementations drain whatever happened since the last call to
/// [`ActivityProvider::poll`]. The trait never returns key identity, text, or
/// window titles — counts and focus transitions only.
pub trait ActivityProvider {
    /// Drain everything that happened since the last poll.
    fn poll(&mut self) -> Vec<ActivityEvent>;
}

/// The v0.1 activity source: reports activity **while the companion's own
/// window is focused**, not global OS activity (plan §3.1, "deliberately
/// faked" for v0.1).
///
/// It is fed by the `companion` crate's Bevy input event readers, which
/// forward [`ActivityEvent`]s in via [`record`]; no game system ever reads the
/// input events directly. Polling [`drains`](ActivityProvider::poll)
/// everything recorded since the last poll.
#[derive(Debug, Default)]
pub struct FocusedWindowProvider {
    /// Events recorded since the last poll, in order.
    pending: VecDeque<ActivityEvent>,
    /// Whether the window is currently focused, so duplicate
    /// `FocusChanged` frames are not double-recorded.
    focused: bool,
}

impl FocusedWindowProvider {
    /// Feed events into the provider. Called by the `companion` crate's Bevy
    /// input systems — never by a game system reading input directly (plan
    /// §3.1).
    pub fn record(&mut self, events: impl IntoIterator<Item = ActivityEvent>) {
        for event in events {
            // The window emits `FocusChanged` every frame while focused;
            // only a genuine state flip is activity worth counting (and a
            // focus transition is still content-free per the privacy
            // invariant).
            if let ActivityEvent::FocusChanged(focused) = event {
                if focused != self.focused {
                    self.focused = focused;
                    self.pending.push_back(event);
                }
                continue;
            }
            self.pending.push_back(event);
        }
    }

    /// Is the provider's window currently focused?
    #[must_use]
    pub fn is_focused(&self) -> bool {
        self.focused
    }
}

impl ActivityProvider for FocusedWindowProvider {
    fn poll(&mut self) -> Vec<ActivityEvent> {
        self.pending.drain(..).collect()
    }
}

/// One step of the activity-rate decay/accumulation used by
/// `activity_bridge_system` (plan §3.2).
///
/// `recent_rate` is an **exponentially decaying counter expressed in
/// events/second**: each step adds the raw event count of this frame as a
/// "new" bucket, then every bucket decays toward zero at
/// [`DECAY_PER_SECOND`] (1.0 /sample, i.e. ≈ 63% per sample at 1 fps).
/// Equivalently, a burst of `N` events at rate 0 produces `N` ev/s of rate
/// immediately, which halves every `1/ln2 ≈ 0.69` samples.
///
/// The result is **clamped** to `0.0..=MAX_RECENT_RATE` so a sustained
/// max-rate mash converges to a finite ceiling instead of running away (the
/// anti-mashing rule from plan §1). Importantly, adding a *count* per frame
/// (rather than a per-frame *rate* `count/dt`) is what makes steady input
/// reach a bounded steady state proportional to the true input rate instead
/// of pinning the rate at the clamp ceiling.
///
/// All arguments are plain values — no Bevy types — so unit tests call this
/// directly without constructing an `App` (plan §3.2, `*` functions).
/// How much "evidence of real work" one activity event is worth.
///
/// Typing is the strongest signal we have and is the unit (1.0). Mouse motion
/// is deliberately worth a fraction: it is weak evidence (scrolling a page,
/// nudging a window) and it arrives orders of magnitude more often, so any
/// weight near 1.0 makes the mouse the dominant input by sheer event volume.
/// Focus changes are bookkeeping, not work, and are worth nothing.
#[must_use]
pub fn event_weight(event: &ActivityEvent) -> f32 {
    match event {
        ActivityEvent::Keystroke => 1.0,
        ActivityEvent::MouseMoved => MOUSE_WEIGHT,
        ActivityEvent::FocusChanged(_) => 0.0,
    }
}

/// Mouse motion's weight relative to a keystroke. See [`event_weight`].
pub const MOUSE_WEIGHT: f32 = 0.25;

pub fn decay_and_accumulate(previous_rate: f32, events: &[ActivityEvent], dt: Duration) -> f32 {
    if dt.is_zero() {
        return previous_rate;
    }
    let dt_secs = dt.as_secs_f32().max(f32::EPSILON); // guard: never divide by 0
    // Sum this frame's events by WEIGHT, not by count. Counting raw events
    // treats one mouse twitch as equal to one keystroke, and a mouse reports
    // motion at 125-1000 Hz while a fast typist manages ~5-10 keys/s — so an
    // unweighted count lets a few seconds of aimless mouse movement out-earn
    // a minute of real work, which is exactly the "idle game wearing a
    // productivity costume" failure this project exists to avoid.
    let added: f32 = events.iter().map(event_weight).sum();
    // Exponential decay toward zero over the elapsed dt.
    let decay = previous_rate * (-DECAY_PER_SECOND * dt_secs).exp();
    // Clamp: a sustained max-rate input converges to the ceiling instead of
    // compounding without bound; the rate can never be negative.
    (decay + added).clamp(0.0, MAX_RECENT_RATE)
}

/// Exponential decay rate of `recent_rate` toward zero, per second.
/// `rate(t) = rate(0) * e^{−DECAY_PER_SECOND·t}` with half-life
/// `1/ln(2) ≈ 0.69` s. Named const so M3's systems share the same tuning.
pub const DECAY_PER_SECOND: f32 = 1.0;

/// Finite upper bound of `recent_rate` (events/second) a sustained max-rate
/// mash can reach. Steady input below this rate never touches the ceiling;
/// an absurd mash converges here (plan §1's anti-mashing invariant).
pub const MAX_RECENT_RATE: f32 = 15.0;

/// A scripted activity source used only in tests.
///
/// It replays a fixed sequence of events, one [`ActivityProvider::poll`]
/// draining one event per call, so tests can exercise the game's handling of
/// activity deterministically without a window or OS.
#[derive(Debug, Clone, Default)]
pub struct MockProvider {
    events: VecDeque<ActivityEvent>,
}

impl MockProvider {
    /// Create a provider that will replay `events` in order, one per poll.
    pub fn new(events: impl IntoIterator<Item = ActivityEvent>) -> Self {
        Self {
            events: events.into_iter().collect(),
        }
    }
}

impl ActivityProvider for MockProvider {
    fn poll(&mut self) -> Vec<ActivityEvent> {
        // Drain at most one event per poll so a caller can observe the
        // provider being drained incrementally across frames.
        match self.events.pop_front() {
            Some(event) => vec![event],
            None => Vec::new(),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn mock_provider_replays_events_in_order() {
        let mut provider = MockProvider::new([
            ActivityEvent::Keystroke,
            ActivityEvent::MouseMoved,
            ActivityEvent::FocusChanged(true),
        ]);

        assert_eq!(provider.poll(), vec![ActivityEvent::Keystroke]);
        assert_eq!(provider.poll(), vec![ActivityEvent::MouseMoved]);
        assert_eq!(provider.poll(), vec![ActivityEvent::FocusChanged(true)]);
        // Exhausted provider yields no events.
        assert!(provider.poll().is_empty());
    }

    #[test]
    fn empty_mock_provider_yields_nothing() {
        let mut provider = MockProvider::default();
        assert!(provider.poll().is_empty());
    }

    #[test]
    fn activity_events_are_content_free() {
        // Guard the privacy invariant: events must never be able to carry
        // content. Keystroke and MouseMoved carry no payload; FocusChanged
        // only a bool. (Compile-time guarantee via the enum shape; this test
        // documents it.)
        let e = ActivityEvent::Keystroke;
        assert_eq!(e, ActivityEvent::Keystroke);
        assert_ne!(e, ActivityEvent::MouseMoved);
    }

    #[test]
    fn focused_window_provider_drains_in_order_and_empts() {
        let mut provider = FocusedWindowProvider::default();
        provider.record([
            ActivityEvent::Keystroke,
            ActivityEvent::MouseMoved,
            ActivityEvent::Keystroke,
        ]);

        assert_eq!(
            provider.poll(),
            vec![
                ActivityEvent::Keystroke,
                ActivityEvent::MouseMoved,
                ActivityEvent::Keystroke
            ]
        );
        // Second poll is empty: drain semantics.
        assert!(provider.poll().is_empty());
    }

    #[test]
    fn focused_window_focus_transitions_recorded_once() {
        let mut provider = FocusedWindowProvider::default();
        assert!(!provider.is_focused());

        // First focus flip: recorded.
        provider.record([ActivityEvent::FocusChanged(true)]);
        assert!(provider.is_focused());
        // Same state repeated (one per frame in practice): not re-recorded.
        provider.record([ActivityEvent::FocusChanged(true)]);
        assert_eq!(provider.poll(), vec![ActivityEvent::FocusChanged(true)]);

        // Flip back: recorded.
        provider.record([ActivityEvent::FocusChanged(false)]);
        assert!(!provider.is_focused());
        assert_eq!(provider.poll(), vec![ActivityEvent::FocusChanged(false)]);
    }

    /// Simulates `decay_and_accumulate` at a fixed frame rate `dt`, where
    /// `events_at(frame)` gives the event count for that frame, and returns
    /// the final `recent_rate` after `frames` frames.
    fn simulate_final(dt: Duration, events_at: impl Fn(usize) -> usize, frames: usize) -> f32 {
        let mut rate = 0.0f32;
        for frame in 0..frames {
            let events: Vec<ActivityEvent> = (0..events_at(frame))
                .map(|_| ActivityEvent::Keystroke)
                .collect();
            rate = decay_and_accumulate(rate, &events, dt);
        }
        rate
    }

    #[test]
    fn decay_and_accumulate_steady_activity_plateaus_near_input_rate() {
        // One event every 6th frame at 30 fps = 5 events/second. The rate
        // must track the true input rate and plateau near 5 ev/s — NOT hit
        // the clamp ceiling, which would mean the per-frame accumulation is
        // dominating (the §1 anti-mashing invariant).
        let dt = Duration::from_millis(1000 / 30); // 30 fps
        let rate = simulate_final(dt, |frame| usize::from(frame.is_multiple_of(6)), 30 * 4);
        // Analytic steady state for a 1-in-6 pattern at true rate r_true =
        // 5 ev/s with k = DECAY_PER_SECOND = 1.0/s and per-frame addition:
        //   rate_ss = (count/frame·per_frame_count) / (1 - e^{-k·dt}) where
        //   count is added on every 6th frame = 5/30 × (1/(1-0.96745))
        //           ≈ 0.1667 / 0.03253 ≈ 5.12 ev/s. Allow [0, 10) to be
        //   robust to per-frame decay between events.
        assert!(
            (0.0..10.0).contains(&rate),
            "steady ~5 ev/s input should plateau near 5 ev/s (well under the \
             anti-mash ceiling of {MAX_RECENT_RATE}), got {rate}"
        );
        assert!(
            rate < MAX_RECENT_RATE,
            "steady 5 ev/s must not hit the anti-mash ceiling, got {rate}"
        );
    }

    #[test]
    fn decay_and_accumulate_burst_then_silence_decays_to_near_zero() {
        // A 1-second burst of 1 event per 50 ms frame = 20 ev/s lifts the
        // rate…
        let frame = Duration::from_millis(50);
        let rate = simulate_final(frame, |_| 1, 20);
        assert!(rate > 5.0, "burst should clearly lift the rate, got {rate}");

        // …then 10 s of silence decays it: with DECAY_PER_SECOND = 1/s (half
        // life ≈ 0.69 s) the residual is 20 * e^-10 ≈ 0.0009 ev/s. One
        // continuous run over 11 s: activity for the first 20 frames.
        let residual = simulate_final(frame, |frame| usize::from(frame < 20), 20 + 10 * 1000 / 50);
        assert!(
            residual < 0.1,
            "after 10 s of silence the residual should be ~0, got {residual}"
        );
    }

    #[test]
    fn decay_and_accumulate_sustained_max_rate_mash_does_not_runaway() {
        // Fire an event every frame at 120 frames/second forever. The rate
        // must (a) stay finite, and (b) converge near — but never exceed —
        // MAX_RECENT_RATE instead of compounding without bound.
        let frame = Duration::from_micros(1_000_000u64 / 120);
        let mut rate = 0.0f32;
        for _ in 0..(120 * 60) {
            rate = decay_and_accumulate(rate, &[ActivityEvent::Keystroke], frame);
            assert!(rate.is_finite(), "rate ran away: {rate}");
        }
        assert!(
            rate > MAX_RECENT_RATE * 0.8,
            "sustained max-rate input should approach the ceiling, got {rate}"
        );
        assert!(
            rate <= MAX_RECENT_RATE,
            "sustained max-rate input must not exceed the ceiling, got {rate}"
        );
    }

    #[test]
    fn decay_and_accumulate_zero_dt_is_a_noop_and_idle_stays_zero() {
        assert_eq!(decay_and_accumulate(3.0, &[], Duration::ZERO), 3.0);
        // No activity ever: rate stays at zero.
        assert_eq!(
            decay_and_accumulate(0.0, &[], Duration::from_millis(16)),
            0.0
        );
        // Never negative, even for absurd inputs.
        assert!(decay_and_accumulate(0.0, &[], Duration::from_secs(1000)) >= 0.0);
    }
}
