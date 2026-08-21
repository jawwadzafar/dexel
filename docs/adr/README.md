# Architecture Decision Records

One file per decision that shaped this project, in the order they were made.
Format: Status / Context / Decision / Consequences. An ADR is never edited to
say something different — supersede it with a new one and cross-link.

| # | Decision | Status |
|---|---|---|
| [0001](0001-rust-bevy-offline-first.md) | Rust + Bevy, desktop-first, offline-first | accepted |
| [0002](0002-activity-isolation-and-privacy.md) | Activity isolation: separate crate, content-free events | accepted |
| [0003](0003-evdev-over-rdev.md) | evdev over rdev for global input | accepted |
| [0004](0004-procedural-pixel-art.md) | Procedural pixel art; generator is the source format | accepted |
| [0005](0005-anti-mashing-economy.md) | Weighted, coalesced, calibrated activity economy | accepted |
| [0006](0006-in-process-visual-verification.md) | In-process capture + vision-model verification | accepted |
| [0007](0007-always-on-top.md) | Always-on-top by default, F10 toggle | accepted |
| [0008](0008-upgrade-tracks.md) | Upgrades as data-driven tracks, bought not unlocked | accepted |
| [0009](0009-app-identity-not-titles.md) | Show app identity, never window titles | accepted |
| [0010](0010-mac-first-honest-mechanics.md) | Mac-first rescue: permissionless global signals, honest moods | accepted |
| [0011](0011-engine-pivot-to-pdf-native-stack.md) | Engine pivot: ship the PDF design on Go + HTML/NES.css | accepted |
| [0012](0012-a2-content-free-signal-set-and-permission-fork.md) | A2 signal set: permissionless-derivable only, copy/paste deferred behind a permission fork | accepted |
| [0013](0013-analytics-over-time-history-streaks-and-charts.md) | Analytics A3 over time: 30-day rolling history, server-side streaks, CSS pixel charts | accepted |
| [0014](0014-save-integrity-hmac-and-config-split.md) | Save integrity: HMAC-signed state.json + unsigned config.json; honest local anti-cheat ceiling | accepted |
