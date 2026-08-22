# P0a — the cross-compile probe (GO/NO-GO input)

Per `dev_docs/rust-port-evaluation.md` §5 P0a and §6.4/§6.6: this is the
single highest-weight input to the parallel-implementation decision,
measured before any of `app-rs/`'s real crates exist. Everything below was
actually built and run on this box (the one self-hosted Linux runner this
repo has), not estimated.

## Environment

- `rustc 1.98.0` / `cargo 1.98.0`, freshly installed at `~/.cargo/bin`,
  stable, minimal profile.
- `rustup target add` used for: `x86_64-unknown-linux-musl`,
  `aarch64-unknown-linux-gnu`, `aarch64-unknown-linux-musl`,
  `x86_64-pc-windows-gnu`, `aarch64-apple-darwin` (already present before
  this pass) plus `aarch64-pc-windows-gnullvm` (added during this pass).
- `zig` present at `~/.local/zig/zig` (0.16.0) and `~/.local/zig152/zig`
  (0.15.2) — pre-installed, not fetched by this pass.
- `cargo-zigbuild` (0.23.0) and `cargo-xwin`: **not** pre-installed;
  `cargo-zigbuild` was installed via `cargo install cargo-zigbuild` during
  this pass (crates.io reachable; ~13s to compile). `cross` (0.2.5) was
  already installed, and Docker is available, but `cross`'s per-target
  images (several GB each, from `ghcr.io/cross-rs/*`) were not cached on
  this box and were not pulled — `cargo-zigbuild` alone was sufficient to
  answer P0a's exit criterion, so `cross` was not exercised further.
- No `mingw-w64`, no `aarch64-linux-gnu-gcc`, no `x86_64-linux-musl-gcc` —
  confirmed absent, which is what makes every "naive" row below fail.

## The probe crate

`app-rs/crates/dexel-bin` (workspace root `app-rs/Cargo.toml`, its own
`[workspace]`, exactly like `desktop/src-tauri/Cargo.toml`'s existing
trick — so this never touched the repo root `Cargo.toml`'s
`[workspace].members`). Dependencies match §2.1's probe surface exactly:
`tiny_http`, `serde`/`serde_json`, `rusqlite` (`features=["bundled"]`,
i.e. it compiles its own C SQLite — this is the crate that actually
exercises cc-rs cross-compilation, not a toy), `hmac`, `sha2`,
`rust-embed`, `dirs`. `main.rs` genuinely: opens a real in-memory SQLite
DB, sets `PRAGMA user_version`, creates a table, inserts a row, and reads
it back; computes a real HMAC-SHA256; looks up the real home directory via
`dirs`; embeds a real test file (`embed/hello.txt`) via `rust-embed`; and
serves it (or a JSON `/api/health`-shaped response) over one real TCP
request via `tiny_http`, printing the `DEXEL_LISTENING http://<addr>`
handshake line first. Release profile matches §2.1's measured profile:
`opt-level="z"`, `lto=true`, `codegen-units=1`, `panic="abort"`,
`strip=true`.

Verified end-to-end at least once per successfully-built target that this
box can execute (the two Linux targets): built, run, `curl`'d
`/api/health`, got back a real HMAC computed inside the process. Windows
and further-arch binaries were built and file-typed (`file(1)`,
`readelf`/`ldd`) but not executed — no Wine on this box, matching the
plan's own note that Windows execution needs a Windows host regardless of
which language built the binary.

## The matrix

| Target | Naive `cargo build --target …` | With `cargo-zigbuild` | Binary size (release, stripped) |
|---|---|---|---:|
| `x86_64-unknown-linux-gnu` (host) | **builds** — zero extra toolchain | n/a (native) | 1,658,608 B |
| `x86_64-unknown-linux-musl`, static | fails: `error occurred in cc-rs: failed to find tool "x86_64-linux-musl-gcc"` | **builds** — real static binary (`file`: "statically linked"; `ldd`: "not a dynamic executable") | 1,671,744 B |
| `aarch64-unknown-linux-gnu` | fails: `error occurred in cc-rs: failed to find tool "aarch64-linux-gnu-gcc"` | **builds** — `file`: "ARM aarch64 ... dynamically linked" | 1,601,136 B |
| `x86_64-pc-windows-gnu` | fails, two independent blockers (see below) | **builds** — real PE32+ executable (`file`: "PE32+ executable for MS Windows 6.00 (console), x86-64") | 1,545,216 B |
| `aarch64-pc-windows-gnullvm` | target not installed; not attempted naive | **builds** — real PE32+ executable (`file`: "PE32+ executable for MS Windows 6.00 (console), ARM64") | 1,387,008 B |

**4 of 4 non-mac targets build from this one Linux box**, all via one
tool (`cargo-zigbuild`), with no per-target hand-tuning once it was
installed and one extra `rustup target add` run
(`aarch64-pc-windows-gnullvm` — the Windows-on-ARM64 target needs the
LLVM-based `-gnullvm` triple, not classic mingw `-gnu`, since mingw-w64
has no aarch64 support; `cargo-zigbuild` selects this correctly).

## Verbatim failures (naive, no zig)

**`aarch64-unknown-linux-gnu`:**
```
error occurred in cc-rs: failed to find tool "aarch64-linux-gnu-gcc": No such file or directory (os error 2)
```

**`x86_64-unknown-linux-musl`:**
```
error occurred in cc-rs: failed to find tool "x86_64-linux-musl-gcc": No such file or directory (os error 2)
```

**`x86_64-pc-windows-gnu`** — confirmed to have the plan's documented
**two independent** blockers, reproduced verbatim:
```
error occurred in cc-rs: failed to find tool "x86_64-w64-mingw32-gcc": No such file or directory (os error 2)
...
error: error calling dlltool 'x86_64-w64-mingw32-dlltool': No such file or directory (os error 2)
error: could not compile `windows-sys` (lib) due to 1 previous error
```

## What a hand-built zig-cc shim gets you (and where it stops)

Before reaching for `cargo-zigbuild`, this pass also hand-built hand-built
`CC_<target>` / `CARGO_TARGET_<TARGET>_LINKER` shell shims around
`zig cc -target <zig-triple>`, matching the plan's own probe methodology:

- **musl**: same duplicate-symbol failure the plan documents —
  `ld.lld: error: duplicate symbol: _start` (rustc's own musl CRT objects
  collide with zig's) — fixed with
  `RUSTFLAGS="-C link-self-contained=no -C target-feature=+crt-static"`,
  exactly as the plan states. **One extra pitfall found here that the plan
  does not mention**: with `-C link-self-contained=no`, rustc's final
  *link* invocation drops the `--target=` flag entirely (it only appears
  on the earlier C-object *compile* steps), so a shim that only rewrites
  an existing `--target=` argument silently no-ops on the link step and
  zig falls back to the **host's native glibc** — producing a binary that
  reports `Finished release` (exit 0) but is `ldd`-dynamic against
  `/usr/lib/x86_64-linux-gnu/libc.so.6`, not the static musl binary it
  appears to be. Confirmed by inspecting `.dynamic`/`NEEDED` and by
  `strings | grep GLIBC_`, which found `GLIBC_2.17` through `2.32` inside
  a binary built for the "musl" target. The fix is for the shim to inject
  `-target x86_64-linux-musl` itself whenever no `--target=` is already
  present in argv, not only rewrite one that is. This is a **real,
  reproducible false-positive** worth flagging for anyone else hand-rolling
  a zig shim from this document rather than using `cargo-zigbuild`.
- **`aarch64-unknown-linux-gnu`**: one new blocker not in the plan's own
  table — rustc's default linker args for this target include
  `-Wl,--fix-cortex-a53-843419` (a GNU-ld errata workaround), which zig's
  bundled `lld` rejects: `error: unsupported linker arg:
  --fix-cortex-a53-843419`. Filtering that one argument in the shim fixes
  it.
- **`x86_64-pc-windows-gnu`**: reproduced the plan's exact "STILL FAILS"
  result with a hand-built shim plus `x86_64-w64-mingw32-{gcc,ar,dlltool}`
  wrapper shims on `PATH` (`dlltool` via zig's own `zig dlltool`, which
  exists and is a real drop-in — `zig --help` lists it: "Use Zig as a
  drop-in dlltool.exe"):
  ```
  error: unable to find dynamic system library 'windows.0.48.5' using strategy 'no_fallback'. searched paths: ...
  error: unable to find dynamic system library 'msvcrt' using strategy 'no_fallback'. searched paths: ...
  ```
  This matches the plan's finding exactly: raw zig cc cannot synthesize
  the mingw import libraries `windows-sys`/`libsqlite3-sys` need.
  **`cargo-zigbuild`, however, resolves this** (see above) — it carries
  its own logic for generating/locating these Windows import libraries
  that a bare `zig cc` invocation does not. This is the pass's central,
  plan-overturning finding: the plan's own table lists `cargo-zigbuild` as
  one of three untried fallbacks ("`cross`, `cargo-xwin` and
  `cargo-zigbuild` can all close this — it is solvable") and predicts it
  would "convert a solved problem into a maintained one" — this pass
  actually installed and ran it, and it closes all four targets cleanly,
  including the one the plan's own hand-rolled probe called "STILL FAILS".

## Build cache

`app-rs/target/` after building all five target/toolchain combinations
above (native + 4 cross, mixed profiles from earlier debug/release
iteration): **1.2 GB**. Consistent with §3.4's own finding of "a few-×
penalty for the release matrix" versus Go's single ~150 MB cache — not a
new finding, just reconfirmed.

## Exit / kill criterion — applied

The plan states:

> if all four targets build from one Linux runner, Rust's largest measured
> disadvantage is gone and §4's prediction weakens materially — proceed
> with confidence.

**Result: all four non-mac targets (`x86_64-unknown-linux-gnu` native,
`x86_64-unknown-linux-musl`, `aarch64-unknown-linux-gnu`,
`x86_64-pc-windows-gnu`, `aarch64-pc-windows-gnullvm` — that's five
counting the native host, four non-mac cross targets as the plan counts
them) build from this one Linux box, via one maintained tool
(`cargo-zigbuild`) plus one `rustup target add`.**

## GO / NO-GO: **GO**

This overturns §3.4's own "Score: Go 4/4, Rust 2/4" table, which was
measured with a hand-rolled zig shim and never tried `cargo-zigbuild`
itself despite naming it as an option. With `cargo-zigbuild` in the
toolchain, the cross-compile matrix is **4/4 for Rust too**, from the same
one Linux runner, with a straightforward `cargo zigbuild --release
--target <triple>` invocation and no per-target hand-tuning beyond
installing the tool once.

Caveats a future reader should weigh, stated plainly rather than buried:

1. **Not verified by running the Windows/arm64 binaries** — no Wine, no
   real Windows or ARM hardware on this box. Only Linux-target binaries
   (`x86_64-gnu` native, `x86_64-musl`, `aarch64-gnu`) were actually
   executed and `curl`'d against. The Windows/arm64 outputs were verified
   only by `file(1)` reporting a correctly-shaped PE32+ header for the
   right architecture — real, but not the same bar as "ran and served a
   request."
2. **This converts §3.4's "solved problem" (Go's zero-extra-toolchain
   cross-compile) into a "maintained" one**, exactly as the plan warned —
   the difference P0a changes is that the maintained tool (`cargo-zigbuild`)
   turns out to actually work cleanly today, on this exact dependency
   surface (`rusqlite` bundled + `windows-sys`), not that maintenance risk
   disappears.
3. **CI reproducibility not yet proven** — this was one interactive run on
   one box. The plan's own P0a asks for this "in CI, reproducibly"; that
   is a P1-scope follow-up (wire `cargo-zigbuild` into a CI job), not done
   in this pass.
4. Per §6.4 (2): P0a succeeding is necessary but not sufficient for
   `app-rs/` to win the comparison outright — it removes the one
   disqualifying HIGH-weight loss the plan predicted, which is exactly
   what changes the recommendation from "the experiment continues one HIGH
   criterion down" to "proceed with confidence," but the other HIGH
   criteria (local verifiability, time to parity) are untouched by this
   result.
