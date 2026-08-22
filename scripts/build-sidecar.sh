#!/usr/bin/env bash
# build-sidecar.sh — cross-compile the dexel Go server into the Tauri
# sidecar binaries the desktop shell bundles (ADR 0015 / docs/plan/
# F3-design.md §4, task T3).
#
# Tauri's `bundle.externalBin` mechanism requires each external binary to be
# named "<base>-<rust target triple>" (plus ".exe" on Windows); the bundler
# picks the one matching the target it is building for and strips the suffix
# inside the bundle. So this script's whole job is: build ./app for a set of
# GOOS/GOARCH pairs and drop each result at
#
#     desktop/src-tauri/binaries/dexel-server-<triple>[.exe]
#
# Verified against https://v2.tauri.app/develop/sidecar/ ("Tauri requires
# you to add the target triple to the sidecar binary name").
#
# Usage:
#   scripts/build-sidecar.sh              # host triple only (the common case)
#   scripts/build-sidecar.sh --all        # host + every cross target below
#   scripts/build-sidecar.sh <triple>...  # only the named triple(s)
#   scripts/build-sidecar.sh --list       # print the triple table and exit
#
# Environment:
#   GO           override the go binary (default: `go` from PATH)
#   OUT_DIR      override the output directory
#
# Honest constraints (do not "fix" these by pretending):
#   * The darwin targets use cgo — app/provider_select_darwin.go pulls in
#     activity.NewDarwinProvider(), whose real capture path is the ADR 0011
#     CGEventSource shim. A CGO_ENABLED=0 darwin build would compile but
#     ship a *blind* provider, which would be a dishonest binary. So the
#     darwin targets are only built when the HOST is macOS (where the
#     macOS Tauri bundle is built anyway) and are skipped, loudly, elsewhere.
#   * linux/windows use CGO_ENABLED=0 by design: their providers are
#     already pure Go (linux reads /dev/input/event*, windows is blind),
#     so a static build cross-compiles from anywhere.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP_DIR="$REPO_ROOT/app"
OUT_DIR="${OUT_DIR:-$REPO_ROOT/desktop/src-tauri/binaries}"
BIN_BASE="dexel-server"
GO="${GO:-go}"

# The triple -> GOOS/GOARCH/cgo table from docs/plan/F3-design.md §4.
# Fields: <rust-target-triple>|<GOOS>|<GOARCH>|<needs-cgo>
TARGETS=(
  "x86_64-unknown-linux-gnu|linux|amd64|0"
  "aarch64-unknown-linux-gnu|linux|arm64|0"
  "x86_64-pc-windows-msvc|windows|amd64|0"
  "aarch64-pc-windows-msvc|windows|arm64|0"
  "aarch64-apple-darwin|darwin|arm64|1"
  "x86_64-apple-darwin|darwin|amd64|1"
)

die() { printf 'build-sidecar: %s\n' "$*" >&2; exit 1; }
note() { printf 'build-sidecar: %s\n' "$*" >&2; }

lookup() {
  # $1 = triple; echoes "GOOS GOARCH CGO" or returns 1
  local t
  for t in "${TARGETS[@]}"; do
    if [ "${t%%|*}" = "$1" ]; then
      IFS='|' read -r _ goos goarch cgo <<<"$t"
      printf '%s %s %s\n' "$goos" "$goarch" "$cgo"
      return 0
    fi
  done
  return 1
}

# host_triple resolves the Rust target triple of THIS machine. rustc is the
# authority (it is the same string Tauri will look for), but this script must
# also work on a machine with a Go toolchain and no Rust — hence the uname
# fallback, which covers exactly the hosts in the table above.
host_triple() {
  if command -v rustc >/dev/null 2>&1; then
    # `rustc -vV` has printed a "host: <triple>" line for many years; it is
    # more portable across rustc versions than `--print host-tuple`.
    local h
    h="$(rustc -vV 2>/dev/null | sed -n 's/^host: //p' | head -n1)"
    if [ -n "$h" ]; then printf '%s\n' "$h"; return 0; fi
  fi
  local os arch
  os="$(uname -s)"; arch="$(uname -m)"
  case "$arch" in
    x86_64|amd64) arch=x86_64 ;;
    arm64|aarch64) arch=aarch64 ;;
    *) die "unsupported host architecture '$arch'; pass an explicit triple" ;;
  esac
  case "$os" in
    Linux)  printf '%s-unknown-linux-gnu\n' "$arch" ;;
    Darwin) printf '%s-apple-darwin\n' "$arch" ;;
    MINGW*|MSYS*|CYGWIN*) printf '%s-pc-windows-msvc\n' "$arch" ;;
    *) die "unsupported host OS '$os'; pass an explicit triple" ;;
  esac
}

build_one() {
  local triple="$1" goos goarch cgo out
  read -r goos goarch cgo <<<"$(lookup "$triple" || die "unknown target triple '$triple' (see --list)")"

  if [ "$cgo" = "1" ] && [ "$(uname -s)" != "Darwin" ]; then
    note "SKIP $triple — needs cgo (the real macOS activity provider) and this host is not macOS."
    note "     Build the darwin sidecars on the Mac that builds the macOS bundle (F3-design.md §4)."
    return 0
  fi

  out="$OUT_DIR/$BIN_BASE-$triple"
  [ "$goos" = "windows" ] && out="$out.exe"

  note "build $triple  (GOOS=$goos GOARCH=$goarch CGO_ENABLED=$cgo)"
  # -trimpath keeps the binary reproducible-ish and strips local paths out of
  # a binary that ships inside an installer. Build from APP_DIR so the
  # module in app/go.mod is the one that resolves.
  ( cd "$APP_DIR" && CGO_ENABLED="$cgo" GOOS="$goos" GOARCH="$goarch" \
      "$GO" build -trimpath -o "$out" . )
  note "  -> $out"
}

main() {
  command -v "$GO" >/dev/null 2>&1 || die "no Go toolchain on PATH (set \$GO or install Go); app/go.mod needs $( sed -n 's/^go //p' "$APP_DIR/go.mod" 2>/dev/null | head -n1 )"
  [ -f "$APP_DIR/go.mod" ] || die "expected a Go module at $APP_DIR/go.mod"

  case "${1:-}" in
    --list)
      printf '%-28s %-8s %-8s %s\n' "TARGET TRIPLE" "GOOS" "GOARCH" "CGO"
      local t
      for t in "${TARGETS[@]}"; do
        IFS='|' read -r tr goos goarch cgo <<<"$t"
        printf '%-28s %-8s %-8s %s\n' "$tr" "$goos" "$goarch" "$cgo"
      done
      printf '\nhost triple: %s\n' "$(host_triple)"
      return 0
      ;;
  esac

  mkdir -p "$OUT_DIR"

  local -a wanted=()
  if [ "$#" -eq 0 ]; then
    wanted=("$(host_triple)")
  elif [ "${1:-}" = "--all" ]; then
    local t
    for t in "${TARGETS[@]}"; do wanted+=("${t%%|*}"); done
  else
    wanted=("$@")
  fi

  local t
  for t in "${wanted[@]}"; do build_one "$t"; done

  note "done. contents of $OUT_DIR:"
  ls -l "$OUT_DIR" >&2
}

main "$@"
