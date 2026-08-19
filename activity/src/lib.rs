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

/// A scripted activity source used only in tests.
///
/// It replays a fixed sequence of events, one [`ActivityProvider::poll`]
/// draining one event per call, so tests can exercise the game's handling of
/// activity deterministically without a window or OS.
#[derive(Debug, Clone, Default)]
pub struct MockProvider {
    events: std::collections::VecDeque<ActivityEvent>,
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
}
