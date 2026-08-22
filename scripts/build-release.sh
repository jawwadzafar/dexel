#!/usr/bin/env bash
#
# dexel — release build script.
#
# Builds the Go server binary (module github.com/jawwadzafar/dexel/app,
# source under app/) for each release target, and packages each one into an
# archive holding the binary plus the licensing files a redistributed binary
# needs (README.md, LICENSE, NOTICE, THIRD-PARTY-LICENSES.md).
#
# Since EMBED-1 (docs/plan/ROADMAP.md) the binary IS the product: app/embed.go
# compiles both static trees it serves — app/public/ (the committed frontend
# bundle) and app/assets/ (the sprite PNGs) — into it with go:embed, so no
# public/ or assets/ directory is shipped, copied, or needed beside it.
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
# Archive layout
# ---------------------------------------------------------------------------
# The archive is the binary and its paperwork, nothing else:
#
#   dexel-<version>-<os>-<arch>/
#     dexel (or dexel.exe)
#     README.md
#     LICENSE
#     NOTICE
#     THIRD-PARTY-LICENSES.md
#
# Extract it anywhere — or copy just the binary out of it and delete the rest
# — and run `./dexel` (or `dexel.exe`). It serves the whole game from its own
# embedded copies of app/public and app/assets (app/embed.go), so there is
# nothing next to it to find, nothing to keep in sync, and no flags or
# environment variables to set. Earlier releases shipped public/ and assets/
# directories alongside the binary and depended on a runtime lookup finding
# them; EMBED-1 deleted that whole class of failure.
#
# The two "is the source tree complete" checks below stay, and matter MORE
# than before: whatever is missing from app/public or app/assets at build
# time is missing from the binary itself, permanently and invisibly.

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

# License/doc files bundled into every archive alongside the binary. Fails
# loudly if one is missing rather than silently shipping an incomplete
# archive.
license_files=(README.md LICENSE NOTICE THIRD-PARTY-LICENSES.md)
for f in "${license_files[@]}"; do
  if [ ! -f "$REPO_ROOT/$f" ]; then
    echo "ERROR: $REPO_ROOT/$f not found — required in every release archive" >&2
    exit 1
  fi
done

# app/public and app/assets are go:embed inputs, not files to copy: if either
# is missing or incomplete the build still SUCCEEDS and produces a binary
# that silently serves an incomplete game. Check them here, before building.
if [ ! -f "$APP_DIR/public/index.html" ]; then
  echo "ERROR: $APP_DIR/public/index.html not found — the frontend bundle must be built and committed before packaging a release (it is embedded into the binary)" >&2
  exit 1
fi

if [ ! -f "$APP_DIR/public/js/dexel.js" ]; then
  echo "ERROR: $APP_DIR/public/js/dexel.js not found — run 'npm run build' in app/frontend/ and commit the bundle before packaging a release" >&2
  exit 1
fi

if [ ! -f "$APP_DIR/assets/room_back.png" ]; then
  echo "ERROR: $APP_DIR/assets/room_back.png not found — app/assets/ is missing or incomplete (regenerate with 'python3 tools/gen_assets.py')" >&2
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
    # PR-2 (MIGRATION_PLAN.md §PR-2): stamp main.version at BUILD time so
    # the shipped binary reports a real version via `dexel version` and
    # /api/health's "version" field even once extracted from its archive
    # on a machine with no .git directory nearby — buildVersion() (the
    # existing git-describe-shaped value, still reported as "commit")
    # cannot answer that on its own.
    CGO_ENABLED="$cgo" GOOS="$os" GOARCH="$arch" go build -trimpath \
      -ldflags "-X main.version=$VERSION" -o "$stage/$bin_name" .
  )

  # No public/ or assets/ copy: both are inside the binary (app/embed.go).
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
