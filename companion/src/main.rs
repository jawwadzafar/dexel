//! dev-companion — companion desktop game binary.
//!
//! M0: a minimal Bevy app that opens an empty window and does nothing else.
//! Activity wiring, HUD, progression, and persistence arrive in later
//! milestones (see docs/implementation-plan.md §4).

use bevy::prelude::*;

fn main() {
    App::new().add_plugins(DefaultPlugins).run();
}
