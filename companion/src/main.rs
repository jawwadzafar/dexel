//! dev-companion — companion desktop game binary.
//!
//! Thin entry point: the game itself (scene, systems, save/load, UI) lives
//! in the `companion` library crate; this binary just calls `companion::run`.

fn main() {
    companion::run();
}
