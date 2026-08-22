#!/usr/bin/env bash
#
# dexel — release build script.
#
# Builds the Go server binary (module github.com/jawwadzafar/dexel/app,
# source under app/) for each release target, and packages each one into a
# self-contained archive: the binary plus the two static trees it serves
# (app/public/ and the repo's assets/) plus the licensing files a
# redistributed binary needs (README.md, LICENSE, NOTICE,
# THIRD-PARTY-LICENSES.md).
#
# Why this exists as a script rather than inline YAML: it needs to be
# runnable and testable on a laptop with no CI involved at all — the exact
# same command release.yml runs is the one a human runs locally to sanity
# check a release before tagging.
#
# ---------------------------------------------------------------------------
# Targets
# ---------------------------------------------------------------------------
#   linux/amd64    CGO_ENABLED=0  (pure Go — cross-compiles cleanly from any host)
#   linux/arm64    CGO_ENABLED=0  (pure Go — cross-compiles cleanly from any host)
#   windows/amd64  CGO_ENABLED=0  (pure Go — cross-compiles cleanly from any host)
#   windows/arm64  CGO_ENABLED=0  (pure Go — cross-compiles cleanly from any host)
#   darwin/arm64   CGO_ENABLED=1  (internal/activity/provider_darwin.go is cgo —
#                  Cocoa/CoreGraphics via Objective-C — and needs a real macOS
#                  clang toolchain; this CANNOT be cross-built from Linux.
#                  Only attempted when running natively on darwin, or when
#                  explicitly requested via DEXEL_RELEASE_TARGETS.)
#
# The default target list (below) excludes darwin/arm64 unless this script
# is itself running on a darwin host. release.yml's macOS job is gated the
# same way one level up (a `mac`-labelled runner), so on the one Linux
# self-hosted runner this repo currently has, darwin/arm64 is simply never
# attempted.
#
# ---------------------------------------------------------------------------
# Usage
# ---------------------------------------------------------------------------
#   scripts/build-release.sh                  # build the default target list
#   scripts/build-release.sh linux/amd64       # build just one target
#   scripts/build-release.sh linux/amd64 windows/amd64
#
# Env overrides:
#   DEXEL_RELEASE_TARGETS   space-separated "os/arch" list, overrides both
#                           the default list and any command-line args.
#   DEXEL_RELEASE_VERSION   version string used in archive names and passed
#                           through as CHANGELOG context; default is
#                           `git describe --tags --always --dirty`.
#   DIST_DIR                where archives land (default: <repo>/dist,
#                           gitignored).
#
# Output: for each target, DIST_DIR/dexel-<version>-<os>-<arch>/ (the archive
# staging directory, left in place for inspection) and
# DIST_DIR/dexel-<version>-<os>-<arch>.tar.gz (or .zip for windows), plus one
# DIST_DIR/sha256sums.txt covering every archive produced this run.
#
# ---------------------------------------------------------------------------
# Archive layout (why it's flat)
# ---------------------------------------------------------------------------
# app/internal/assets.LocateVerbose() finds assets/ by walking upward from
# the *executable's own directory* looking for an "assets" subdirectory
# (among other strategies — see that file's doc comment). main.go's
# -public flag defaults to "./public", resolved relative to the process's
# cwd with no upward walk of its own. So a binary run as `./dexel` from a
# directory that itself contains ./public and ./assets satisfies both
# lookups with zero flags or environment variables: publicDir="./public"
# resolves directly, and searchUpward(executableDir) finds
# "<executableDir>/assets" on its very first probe. That is why each
# archive is packaged flat:
#
#   dexel-<version>-<os>-<arch>/
#     dexel (or dexel.exe)
#     public/                  (copy of app/public — the committed frontend bundle)
#     assets/                  (copy of the repo's assets/ — sprites)
#     README.md
#     LICENSE
#     NOTICE
#     THIRD-PARTY-LICENSES.md
#
# `cd` into the extracted directory and run `./dexel` (or `dexel.exe`) —
# it Just Works with no flags.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
APP_DIR="$REPO_ROOT/app"
DIST_DIR="${DIST_DIR:-$REPO_ROOT/dist}"

VERSION="${DEXEL_RELEASE_VERSION:-$(cd "$REPO_ROOT" && git describe --tags --always --dirty 2>/dev/null || echo "dev")}"

# ---- target list -----------------------------------------------------------

default_targets() {
  local list="linux/amd64 linux/arm64 windows/amd64 windows/arm64"
  if [ "$(go env GOHOSTOS 2>/dev/null || uname -s | tr '[:upper:]' '[:lower:]')" = "darwin" ]; then
    list="$list darwin/arm64"
  fi
  echo "$list"
}

if [ -n "${DEXEL_RELEASE_TARGETS:-}" ]; then
  # shellcheck disable=SC2206
  targets=($DEXEL_RELEASE_TARGETS)
elif [ "$#" -gt 0 ]; then
  targets=("$@")
else
  # shellcheck disable=SC2207
  targets=($(default_targets))
fi

echo "==> dexel release build"
echo "    version: $VERSION"
echo "    targets: ${targets[*]}"
echo "    dist:    $DIST_DIR"
echo ""

mkdir -p "$DIST_DIR"
rm -f "$DIST_DIR/sha256sums.txt"

# ---- helpers ----------------------------------------------------------------

# License/doc files bundled into every archive alongside the binary and the
# two static trees. Fails loudly if one is missing rather than silently
# shipping an incomplete archive.
license_files=(README.md LICENSE NOTICE THIRD-PARTY-LICENSES.md)
for f in "${license_files[@]}"; do
  if [ ! -f "$REPO_ROOT/$f" ]; then
    echo "ERROR: $REPO_ROOT/$f not found — required in every release archive" >&2
    exit 1
  fi
done

if [ ! -f "$APP_DIR/public/index.html" ]; then
  echo "ERROR: $APP_DIR/public/index.html not found — the frontend bundle must be built and committed before packaging a release" >&2
  exit 1
fi

if [ ! -f "$REPO_ROOT/assets/room_back.png" ]; then
  echo "ERROR: $REPO_ROOT/assets/room_back.png not found — assets/ is missing or incomplete" >&2
  exit 1
fi

archive_paths=()

build_one() {
  local pair="$1" os arch
  os="${pair%/*}"
  arch="${pair#*/}"

  local name="dexel-${VERSION}-${os}-${arch}"
  local stage="$DIST_DIR/$name"
  rm -rf "$stage"
  mkdir -p "$stage"

  local bin_name="dexel"
  [ "$os" = "windows" ] && bin_name="dexel.exe"

  local cgo=0
  if [ "$os" = "darwin" ]; then
    cgo=1
    if [ "$(uname -s | tr '[:upper:]' '[:lower:]')" != "darwin" ]; then
      echo "ERROR: darwin/$arch requires cgo (internal/activity/provider_darwin.go, Cocoa/CoreGraphics via Objective-C) and a macOS clang toolchain — it cannot be cross-built from $(uname -s). Build it natively on a macOS host." >&2
      exit 1
    fi
  fi

  echo "==> building $os/$arch (CGO_ENABLED=$cgo)"
  (
    cd "$APP_DIR"
    CGO_ENABLED="$cgo" GOOS="$os" GOARCH="$arch" go build -trimpath -o "$stage/$bin_name" .
  )

  cp -r "$APP_DIR/public" "$stage/public"
  cp -r "$REPO_ROOT/assets" "$stage/assets"
  for f in "${license_files[@]}"; do
    cp "$REPO_ROOT/$f" "$stage/$f"
  done

  local archive
  if [ "$os" = "windows" ]; then
    archive="$DIST_DIR/$name.zip"
    rm -f "$archive"
    (cd "$DIST_DIR" && zip -rq "$name.zip" "$name")
  else
    archive="$DIST_DIR/$name.tar.gz"
    rm -f "$archive"
    tar -C "$DIST_DIR" -czf "$archive" "$name"
  fi

  echo "    -> $archive"
  archive_paths+=("$archive")
}

for t in "${targets[@]}"; do
  build_one "$t"
done

echo ""
echo "==> checksums"
(
  cd "$DIST_DIR"
  : > sha256sums.txt
  for a in "${archive_paths[@]}"; do
    sha256sum "$(basename "$a")" >> sha256sums.txt
  done
  cat sha256sums.txt
)

echo ""
echo "==> done: ${#archive_paths[@]} archive(s) in $DIST_DIR"
