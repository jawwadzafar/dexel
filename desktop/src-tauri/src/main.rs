// Prevents an extra console window from appearing alongside the app on
// Windows release builds. Debug builds keep it, so `[dexel-server]` log
// lines stay visible while developing.
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

// All of the real work lives in the library half of this crate (see
// src/lib.rs), which is the Tauri v2 template's layout: it keeps a single
// run() reachable from a future mobile entry point without restructuring.
fn main() {
    dexel_lib::run()
}
