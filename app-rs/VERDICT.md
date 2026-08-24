# app-rs: experiment concluded — Go stays

**Decision (2026-08-24, owner + overseer): dexel remains Go. This tree is a
frozen experiment record, not a live track.**

Why (full analysis: dev_docs/rust-port-evaluation.md + dev_docs/rust-parallel/):
- The one architectural prize — in-process Tauri unification — became an
  anti-goal: the shipped product model deliberately decouples the window from
  the background runtime (closing the window must not stop dexel). In-process
  would re-couple them.
- The measurable wins (binary 12.6→2.6MB, RSS ~11→~6MB, no GC) are cosmetic
  for a desktop companion ticking once per second.
- The one genuine win — Rust's exhaustive, compile-time content-free privacy
  guarantee — is back-portable to the Go tests (~150 lines) without a port.
- Cost to parity: 18–29 agent-days re-proving 257 tests, the anti-cheat chain,
  and the honesty invariants — the riskiest possible category of rewrite.
- P0 result kept for the record: cargo-zigbuild builds all 4 non-mac targets
  from one Linux box; the goldens under dev_docs/rust-parallel/goldens/ remain
  valuable to Go itself as wire-contract regression fixtures.

Revisit ONLY if: dexel must target a platform Go cannot serve, or the desktop
model changes to window-owns-runtime. Otherwise do not re-litigate.
