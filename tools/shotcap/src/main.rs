//! Visual verification capture: run the REAL game app with a seeded save
//! under the normal winit event-loop runner, capture its own framebuffer via
//! Bevy's `Screenshot` component, then stop the app by writing an `AppExit`
//! message after a few seconds.
//!
//! Env:
//!   DEV_COMPANION_SEED_COINS      (default 65)
//!   DEV_COMPANION_SEED_XP         (default 40)
//!   DEV_COMPANION_SEED_WORK_DONE  (default 36.9)
//!   SHOTCAP_OUT                   (default /tmp/opencode/shotcap-out)
//!   SHOTCAP_EXIT_SECS             (default 7)

use bevy::app::AppExit;
use bevy::prelude::*;
use bevy::render::view::screenshot::{Screenshot, save_to_disk};

fn capture(mut commands: Commands, times: Res<Time>, mut exit_writer: MessageWriter<AppExit>) {
    let s = times.elapsed_secs();
    let at = s.floor() as u32;
    if at == 2 || at == 5 {
        let out_dir =
            std::env::var("SHOTCAP_OUT").unwrap_or_else(|_| "/tmp/opencode/shotcap-out".into());
        commands
            .spawn(Screenshot::primary_window())
            .observe(save_to_disk(format!("{out_dir}/shot_{at}s.png")));
        info!("screenshot requested at t={s:.2}s");
    }
    let exit_at: f32 = std::env::var("SHOTCAP_EXIT_SECS")
        .ok()
        .and_then(|v| v.parse().ok())
        .unwrap_or(7.0);
    if s >= exit_at {
        info!("exit time reached (t={s:.2}s); stopping");
        exit_writer.write(AppExit::Success);
    }
}

fn main() {
    let seed_wallet: u64 = std::env::var("DEV_COMPANION_SEED_COINS")
        .ok()
        .and_then(|v| v.parse().ok())
        .unwrap_or(65);
    let seed_xp: u32 = std::env::var("DEV_COMPANION_SEED_XP")
        .ok()
        .and_then(|v| v.parse().ok())
        .unwrap_or(40);
    let seed_work: f32 = std::env::var("DEV_COMPANION_SEED_WORK_DONE")
        .ok()
        .and_then(|v| v.parse().ok())
        .unwrap_or(36.9);

    let out_dir =
        std::env::var("SHOTCAP_OUT").unwrap_or_else(|_| "/tmp/opencode/shotcap-out".into());
    std::fs::create_dir_all(&out_dir).expect("create output dir");

    let mut app = companion::build_app_with_seed(seed_wallet, seed_xp, seed_work);
    app.add_systems(Update, capture);
    app.run();
}
