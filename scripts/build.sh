#!/usr/bin/env bash
#
# dev-companion — cross-compile build script.
#
# Builds the `companion` binary for the requested targets into ./build/
# (which is git-ignored; see .gitignore).
#
# Targets:
#   x86_64   -> x86_64-unknown-linux-gnu     (native, always works)
#   arm      -> aarch64-unknown-linux-gnu    (cross, via zig — needs an aarch64 sysroot)
#   arm-musl -> aarch64-unknown-linux-musl   (cross, via zig — static, needs an aarch64 musl sysroot)
#   mac      -> aarch64-apple-darwin         (ONLY on a macOS host — cross from Linux is unsupported)
#   all      -> x86_64 + arm (+ mac if on a mac)
#
# Usage:
#   ./scripts/build.sh                 # build x86_64 (native)
#   ./scripts/build.sh x86_64          # build x86_64 only
#   ./scripts/build.sh arm             # build aarch64 (gnu, dynamic)
#   ./scripts/build.sh arm-musl        # build aarch64 (musl, static)
#   ./scripts/build.sh all             # x86_64 + arm
#
# Environment overrides:
#   CARGO_TARGET_DIR   where cargo builds (default: <repo>/build/target)
#   ZIG                path to zig (default: $HOME/.local/zig/zig or `zig` on PATH)
#   ARM_SYSROOT        aarch64 sysroot dir (has lib/ + include/ for the target).
#                      Required for arm/arm-musl. See "Cross-building ARM" below.
#
# ---------------------------------------------------------------------------
# Cross-building ARM from this x86_64 box
# ---------------------------------------------------------------------------
# Bevy links native windowing libraries (wayland-client, gbm, drm, xkbcommon,
# alsa, x11). Cross-compiling those needs an **aarch64 sysroot** — the target
# platform's C libraries + headers. zig 0.16 cross-compiles the *Rust* and *C*
# fine but does NOT bundle those GUI libs for aarch64, so this machine (no
# sudo/apt) cannot produce an ARM binary on its own.
#
# Two supported ways to get an ARM binary:
#   1. Run this script with `cross` (Docker) — the recommended path, it
#      downloads a real aarch64 toolchain+sysroot:
#          cross build --release --target aarch64-unknown-linux-gnu -p companion
#      (see .github/workflows/ci.yml — the same thing, automated)
#   2. Provide ARM_SYSROOT pointing at an aarch64 sysroot and run
#          ./scripts/build.sh arm
#      The script wires zig as the C compiler/linker and a pkg-config shim
#      so the -sys crates resolve against the sysroot.
#
# A quick x86_64 sanity build (always available):
#   ./scripts/build.sh x86_64

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

TARGETS_DEFAULT="x86_64"
requested="${1:-$TARGETS_DEFAULT}"

# ---- locate zig (for cross) ---------------------------------------------
find_zig() {
  if [ -n "${ZIG:-}" ] && [ -x "$ZIG" ]; then echo "$ZIG"; return; fi
  if [ -x "$HOME/.local/zig/zig" ]; then echo "$HOME/.local/zig/zig"; return; fi
  if command -v zig >/dev/null 2>&1; then command -v zig; return; fi
  echo ""
}

# ---- target triples -------------------------------------------------------
triple_for() {
  case "$1" in
    x86_64)   echo "x86_64-unknown-linux-gnu" ;;
    arm)      echo "aarch64-unknown-linux-gnu" ;;
    arm-musl) echo "aarch64-unknown-linux-musl" ;;
    mac)      echo "aarch64-apple-darwin" ;;
    *) echo "unknown target: $1 (expected x86_64, arm, arm-musl, mac, all)" >&2; exit 2 ;;
  esac
}

# Build one cargo target into $OUT_DIR, emitting ./build/<name>/<binary>.
build_target() {
  local name="$1" triple="$2"
  local is_cross=0
  case "$triple" in aarch64*) is_cross=1 ;; esac

  local out_dir="$REPO_ROOT/build/$name"
  mkdir -p "$out_dir"

  local tdir="${CARGO_TARGET_DIR:-$REPO_ROOT/build/target}"
  if [ "$is_cross" -eq 0 ]; then
    # Native build — no cross toolchain needed.
    echo "==> building $name ($triple) [native]"
    cargo build --release --target "$triple" -p companion --target-dir "$tdir"
    cp "$tdir/$triple/release/companion" "$out_dir/companion"
  else
    cross_build "$name" "$triple" "$out_dir"
  fi
  echo "==> $name done: $out_dir/companion"
}

# Cross-build via zig (cc/linker) + a generated pkg-config shim over ARM_SYSROOT.
cross_build() {
  local name="$1" triple="$2" out_dir="$3"
  local zig; zig="$(find_zig)"
  if [ -z "$zig" ]; then
    echo "ERROR: ARM cross-build needs zig. Install zig or run \`cross build\` instead." >&2
    echo "       (zig cross-compiles Rust+C, but the aarch64 wayland/gbm/alsa libs" >&2
    echo "        must come from an ARM_SYSROOT — see the comment at the top of this file.)" >&2
    exit 3
  fi
  if [ -z "${ARM_SYSROOT:-}" ]; then
    echo "ERROR: ARM cross-build needs ARM_SYSROOT pointing at an aarch64 sysroot" >&2
    echo "       (lib/ + include/ for $triple). Without it, the wayland/gbm/alsa -sys" >&2
    echo "       crates cannot resolve their native libs. Easiest alternative:" >&2
    echo "         cross build --release --target $triple -p companion" >&2
    exit 3
  fi

  # Ensure the rust target std is installed.
  rustup target add "$triple" >/dev/null 2>&1 || true

  local shimdir="$REPO_ROOT/build/cross-shims"
  mkdir -p "$shimdir"

  # cc shim: forward to `zig cc`, remap rust triples, add -static for musl.
  cat > "$shimdir/cc" <<EOF
#!/usr/bin/env python3
import os, sys
args = list(sys.argv[1:]); out = []; i = 0
static = "musl" in "$triple"
while i < len(args):
    a = args[i]
    if a.startswith("--target="):
        a = "--target=" + a.split("=",1)[1].replace("-unknown-linux-gnu","-linux-gnu").replace("-unknown-linux-musl","-linux-musl")
    out.append(a)
    if a == "-target" and i+1 < len(args):
        out.append(args[i+1].replace("-unknown-linux-gnu","-linux-gnu").replace("-unknown-linux-musl","-linux-musl")); i += 1
    i += 1
if static and not any(x == "-static" for x in out):
    out.append("-static")
os.execv("$zig", ["$zig","cc"] + out)
EOF
  chmod +x "$shimdir/cc"

  # ar shim: forward to `zig ar`.
  printf '#!/bin/sh\nexec "%s" ar "$@"\n' "$zig" > "$shimdir/ar"
  chmod +x "$shimdir/ar"

  # pkg-config shim: resolve against ARM_SYSROOT and force the target libdir.
  cat > "$shimdir/pkg-config" <<EOF
#!/usr/bin/env python3
import os, sys
root = os.environ.get("PKG_CONFIG_SYSROOT_DIR", "$ARM_SYSROOT")
args = sys.argv[1:]
# find the .pc file in the sysroot
name = args[-1]
cands = [
    os.path.join(root, "usr/lib/aarch64-linux-gnu/pkgconfig", name + ".pc"),
    os.path.join(root, "usr/lib/pkgconfig", name + ".pc"),
    os.path.join(root, "usr/lib64/pkgconfig", name + ".pc"),
    os.path.join(root, "usr/share/pkgconfig", name + ".pc"),
    os.path.join("$ARM_SYSROOT", "lib", name + ".pc"),
]
pc = next((c for c in cands if os.path.exists(c)), None)
if pc is None:
    # last resort: a generic stub so -sys crates get -l flags; link resolved via zig
    name_base = name.replace("-client","").replace("-cursor","")
    print("Name: %s" % name)
    print("Version: 0")
    print("Libs: -L%s/lib/aarch64-linux-gnu -l%s" % (root, name))
    print("Cflags:")
    sys.exit(0)
# parse the real .pc, rewriting libdir to the sysroot
d = {}
with open(pc) as f:
    for line in f:
        line = line.strip()
        if not line or line.startswith("#"): continue
        if ":" in line and not line.startswith(("Name","Description","Version")):
            k,_,v = line.partition(":"); d[k.strip()] = v.strip()
        elif "=" in line:
            k,_,v = line.partition("="); d[k.strip()] = v.strip()
def exp(s):
    return s.replace("\${prefix}","$root").replace("\${libdir}", "$root/lib/aarch64-linux-gnu").replace("\${includedir}","$root/usr/include")
print("Name: %s" % d.get("Name", name))
print("Version: %s" % d.get("Version","0"))
print("Libs: -L%s/lib/aarch64-linux-gnu %s" % (root, exp(d.get("Libs.private", d.get("Libs","-l%s"%name)))))
print("Cflags: -I%s/usr/include" % root)
EOF
  chmod +x "$shimdir/pkg-config"

  local tdir="${CARGO_TARGET_DIR:-$REPO_ROOT/build/target}"
  export CARGO_TARGET_DIR="$tdir"
  export "CC_${triple//-/_}"="$shimdir/cc"
  export "AR_${triple//-/_}"="$shimdir/ar"
  export "CARGO_TARGET_${triple//-/_}_LINKER"="$shimdir/cc"
  export "CARGO_TARGET_${triple//-/_}_AR"="$shimdir/ar"
  export PKG_CONFIG="$shimdir/pkg-config"
  export PKG_CONFIG_SYSROOT_DIR="$ARM_SYSROOT"
  export PKG_CONFIG_PATH="$shimdir:$ARM_SYSROOT/usr/lib/aarch64-linux-gnu/pkgconfig"

  echo "==> building $name ($triple) [cross via zig + sysroot $ARM_SYSROOT]"
  cargo build --release --target "$triple" -p companion
  cp "$tdir/$triple/release/companion" "$out_dir/companion"
}

case "$requested" in
  all)
    if [ "$(uname -s)" = "Darwin" ]; then targets="x86_64 arm mac"; else targets="x86_64 arm"; fi
    ;;
  *)   targets="$requested" ;;
esac

for t in $targets; do
  build_target "$t" "$(triple_for "$t")"
done

echo ""
echo "=== artifacts ==="
for f in build/*/companion; do
  [ -f "$f" ] && { printf "%-40s " "$f"; file -b "$f" | cut -d, -f1-2; }
done
