#!/usr/bin/env bash
#
# dexel — macOS (darwin/arm64) release build, sign, notarize and upload.
#
# THIS SCRIPT ONLY RUNS ON THE OWNER'S MAC. It is the hand-run counterpart to
# `.github/workflows/release.yml`'s gated `release-macos` job, which is a
# deliberate no-op until a `mac`-labelled self-hosted runner exists (see that
# file's comment above the job, and docs/plan/MAC-RELEASE.md §7 "When CI takes
# over"). Everything the Linux runner cannot produce, this produces:
#
#   1. dexel-<TAG>-darwin-arm64.tar.gz   the CLI/runtime archive, packaged to
#                                        scripts/build-release.sh's exact
#                                        layout (binary + the four licence
#                                        files, nothing else)
#   2. dexel-<TAG>-darwin-arm64.dmg      the Tauri desktop bundle, signed and
#      (or ...-unsigned.dmg)             notarized when the Apple env is set
#   3. a MERGED sha256sums.txt           the release's existing checksum file
#                                        with the darwin lines added/replaced
#
# WHY DARWIN IS DIFFERENT, in one paragraph, so nobody tries to "fix" it by
# cross-compiling: app/internal/activity/provider_darwin.go is cgo —
# Cocoa/CoreGraphics via Objective-C, `#cgo LDFLAGS: -framework Cocoa
# -framework CoreGraphics`. A CGO_ENABLED=0 darwin build does not degrade to a
# blind provider, it FAILS TO LINK (activity.NewDarwinProvider has no non-cgo
# definition). So darwin/arm64 needs a real macOS clang toolchain and the
# Apple SDK, i.e. this Mac. scripts/build-release.sh says the same thing and
# refuses the target on a non-darwin host.
#
# ---------------------------------------------------------------------------
# Usage
# ---------------------------------------------------------------------------
#   bash scripts/mac-release.sh                  # latest release tag, upload
#   bash scripts/mac-release.sh v0.2.0           # a specific tag, upload
#   bash scripts/mac-release.sh --dry-run        # build + verify, upload NOTHING
#   bash scripts/mac-release.sh v0.2.0 --dry-run
#   bash scripts/mac-release.sh --help
#
# Environment:
#   APPLE_SIGNING_IDENTITY   "Developer ID Application: NAME (TEAMID)" — when
#                            set, tauri-bundler code-signs the .app/.dmg.
#   APPLE_ID / APPLE_PASSWORD / APPLE_TEAM_ID
#                            when all three are set (and signing is on),
#                            tauri-bundler ALSO notarizes and staples.
#                            APPLE_PASSWORD is an APP-SPECIFIC password from
#                            appleid.apple.com, never the account password.
#   DEXEL_ALLOW_TAG_MISMATCH=1
#                            build even though HEAD is not the tag / the tree
#                            is dirty. Off by default on purpose: an artifact
#                            named v0.2.0 that is not v0.2.0's source is the
#                            one failure nobody can debug after the fact.
#   DIST_DIR                 where archives land (default <repo>/dist, gitignored)
#   GH_REPO_OVERRIDE         target another repo (testing only)
#
# ---------------------------------------------------------------------------
# Portability note: this script must run under /bin/bash, which on macOS is
# still bash 3.2 (2007). So: no associative arrays, no `mapfile`, no `${x,,}`,
# no `[[ -v ]]`, and no expansion of a possibly-empty array under `set -u`
# (`"${arr[@]}"` on an empty array is an "unbound variable" error before bash
# 4.4). Keep it that way; the owner should not have to `brew install bash` to
# cut a release.
# ---------------------------------------------------------------------------

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
APP_DIR="$REPO_ROOT/app"
DESKTOP_DIR="$REPO_ROOT/desktop"
TAURI_DIR="$DESKTOP_DIR/src-tauri"
DIST_DIR="${DIST_DIR:-$REPO_ROOT/dist}"
GH_REPO="${GH_REPO_OVERRIDE:-jawwadzafar/dexel}"

# The darwin target. v1 is arm64 only — see docs/plan/MAC-RELEASE.md
# § "Adding an Intel build later" for how amd64/universal would be added.
GOOS="darwin"
GOARCH="arm64"
RUST_TRIPLE="aarch64-apple-darwin"

# Mirrored from scripts/build-release.sh (READ-ONLY by ownership rules — this
# script duplicates its recipe deliberately rather than refactoring it, so a
# release cannot be broken from two directions at once). If that list ever
# changes there, change it here: both are checked against the repo root below.
LICENSE_FILES="README.md LICENSE THIRD-PARTY-LICENSES.md"

# ---------------------------------------------------------------------------
# output helpers — loud on purpose; this script is run by a human, rarely
# ---------------------------------------------------------------------------

if [ -t 1 ]; then
  C_RST=$'\033[0m'; C_B=$'\033[1m'; C_RED=$'\033[31m'; C_YEL=$'\033[33m'; C_GRN=$'\033[32m'
else
  C_RST=""; C_B=""; C_RED=""; C_YEL=""; C_GRN=""
fi

step()  { printf '\n%s==> %s%s\n' "$C_B" "$*" "$C_RST"; }
info()  { printf '    %s\n' "$*"; }
ok()    { printf '    %sOK%s %s\n' "$C_GRN" "$C_RST" "$*"; }
warn()  { printf '%sWARNING:%s %s\n' "$C_YEL" "$C_RST" "$*" >&2; }
die()   { printf '\n%sERROR:%s %s\n' "$C_RED" "$C_RST" "$*" >&2; exit 1; }

# die_fix prints the failure and the exact command that fixes it.
die_fix() {
  printf '\n%sERROR:%s %s\n' "$C_RED" "$C_RST" "$1" >&2
  shift
  printf '%sFIX:%s\n' "$C_B" "$C_RST" >&2
  while [ "$#" -gt 0 ]; do printf '    %s\n' "$1" >&2; shift; done
  exit 1
}

# ---------------------------------------------------------------------------
# args
# ---------------------------------------------------------------------------

usage() {
  cat <<'USAGE_TEXT_EOF'
dexel — macOS (darwin/arm64) release build, sign, notarize and upload.
Runs ONLY on macOS. See docs/plan/MAC-RELEASE.md for the full run-book.

USAGE
  bash scripts/mac-release.sh [TAG] [--dry-run]

ARGUMENTS
  TAG          the release tag to build and attach assets to (e.g. v0.2.0).
               Defaults to the latest published release tag in
               jawwadzafar/dexel, read via `gh release view`.

FLAGS
  --dry-run    build, test, sign and verify everything; upload NOTHING.
  -h, --help   this text.

SIGNING (tiers are chosen by which variables are set — see the run-book)
  tier 1  nothing set                       -> unsigned, .dmg named "-unsigned"
  tier 2  APPLE_SIGNING_IDENTITY            -> code-signed
  tier 3  + APPLE_ID APPLE_PASSWORD APPLE_TEAM_ID
                                            -> signed, notarized and stapled
                                               (APPLE_PASSWORD is an
                                               app-specific password)

OTHER ENVIRONMENT
  DEXEL_ALLOW_TAG_MISMATCH=1  build even if HEAD is not TAG / the tree is dirty
  DIST_DIR                    where archives land (default: <repo>/dist)
  GO                          override the go binary
  GH_REPO_OVERRIDE            target another repo (testing only)

WHAT IT PRODUCES
  dist/dexel-<TAG>-darwin-arm64.tar.gz   the CLI/runtime archive
  dist/dexel-<TAG>-darwin-arm64.dmg      the desktop bundle
       (or ...-darwin-arm64-unsigned.dmg in tier 1)
  a merged sha256sums.txt attached to the release
USAGE_TEXT_EOF
  exit "${1:-0}"
}

TAG=""
DRY_RUN=0

while [ "$#" -gt 0 ]; do
  case "$1" in
    --dry-run) DRY_RUN=1 ;;
    -h|--help) usage 0 ;;
    -*) die_fix "unknown flag '$1'" "bash scripts/mac-release.sh --help" ;;
    *)
      [ -z "$TAG" ] || die_fix "more than one TAG given ('$TAG' and '$1')" \
        "bash scripts/mac-release.sh [TAG] [--dry-run]"
      TAG="$1"
      ;;
  esac
  shift
done

# ===========================================================================
# 1. PREREQUISITES — every one of them checked before anything is built, each
#    with the exact command that fixes it. A release build that dies 8 minutes
#    in because `gh` was never authenticated is a bad release build.
# ===========================================================================

step "prerequisites"

# ---- macOS + arm64 --------------------------------------------------------
host_os="$(uname -s)"
host_arch="$(uname -m)"

if [ "$host_os" != "Darwin" ]; then
  die_fix "this script builds darwin/arm64 natively and this host is '$host_os', not Darwin." \
    "Run it on the owner's Mac. Nothing here can be cross-built from $host_os:" \
    "app/internal/activity/provider_darwin.go is cgo (Cocoa/CoreGraphics) and" \
    "does not link with CGO_ENABLED=0, and 'cargo tauri build' cannot produce a" \
    ".dmg off a Mac at all." \
    "" \
    "On Linux, release.yml's 'release' job already covers linux/* and windows/*."
fi
ok "macOS ($(sw_vers -productVersion 2>/dev/null || echo 'version unknown'))"

if [ "$host_arch" != "arm64" ]; then
  die_fix "expected an Apple Silicon Mac (uname -m = arm64), got '$host_arch'." \
    "This script's v1 target is darwin/arm64 only." \
    "If you are on an Intel Mac and want a darwin/amd64 release, see" \
    "docs/plan/MAC-RELEASE.md §8 'Adding an Intel build later'." \
    "If you are on Apple Silicon and see x86_64 here, you are inside a Rosetta" \
    "shell — start a native Terminal (or run: arch -arm64 bash scripts/mac-release.sh)."
fi
ok "arm64"

# ---- Xcode Command Line Tools --------------------------------------------
# xcode-select -p is the canonical check; it exits non-zero and prints
# "error: unable to find utility..." when the CLT are absent. clang is then
# checked separately because a stale/partial CLT install can satisfy the first
# and not the second — and cgo will need the compiler, not the path.
if ! xcode-select -p >/dev/null 2>&1; then
  die_fix "the Xcode Command Line Tools are not installed (xcode-select -p failed)." \
    "xcode-select --install" \
    "" \
    "cgo needs a real clang + the macOS SDK to build provider_darwin.go, and" \
    "Rust needs them to link. Re-run this script once the installer finishes."
fi
if ! clang --version >/dev/null 2>&1; then
  die_fix "clang is not usable even though xcode-select -p succeeded ($(xcode-select -p))." \
    "sudo rm -rf /Library/Developer/CommandLineTools && xcode-select --install" \
    "" \
    "If you have full Xcode installed, point the toolchain at it:" \
    "sudo xcode-select -s /Applications/Xcode.app/Contents/Developer"
fi
ok "Xcode CLT at $(xcode-select -p)"

# ---- Go >= the version app/go.mod asks for -------------------------------
# The minimum is READ FROM app/go.mod rather than hardcoded, so bumping the
# module's go directive cannot leave this check quietly behind.
GO="${GO:-go}"
GO_MIN="$(sed -n 's/^go \([0-9][0-9.]*\).*/\1/p' "$APP_DIR/go.mod" | head -n1)"
[ -n "$GO_MIN" ] || GO_MIN="1.27"

# ver_ge "$have" "$want" — true when have >= want, comparing up to three
# dotted numeric components. Pure bash (no `sort -V`: BSD sort on macOS has
# no -V, so relying on it is exactly the kind of Linux-ism that breaks here).
ver_ge() {
  have_maj="${1%%.*}"; rest="${1#*.}"
  case "$1" in *.*) : ;; *) rest="0.0" ;; esac
  have_min="${rest%%.*}"; have_pat="${rest#*.}"
  case "$rest" in *.*) : ;; *) have_pat="0" ;; esac

  want_maj="${2%%.*}"; wrest="${2#*.}"
  case "$2" in *.*) : ;; *) wrest="0.0" ;; esac
  want_min="${wrest%%.*}"; want_pat="${wrest#*.}"
  case "$wrest" in *.*) : ;; *) want_pat="0" ;; esac

  # strip any non-numeric tail (e.g. "1.27rc1" -> "1.27")
  have_maj="${have_maj%%[!0-9]*}"; have_min="${have_min%%[!0-9]*}"; have_pat="${have_pat%%[!0-9]*}"
  want_maj="${want_maj%%[!0-9]*}"; want_min="${want_min%%[!0-9]*}"; want_pat="${want_pat%%[!0-9]*}"

  [ -n "$have_maj" ] || have_maj=0; [ -n "$have_min" ] || have_min=0; [ -n "$have_pat" ] || have_pat=0
  [ -n "$want_maj" ] || want_maj=0; [ -n "$want_min" ] || want_min=0; [ -n "$want_pat" ] || want_pat=0

  if [ "$have_maj" -gt "$want_maj" ]; then return 0; fi
  if [ "$have_maj" -lt "$want_maj" ]; then return 1; fi
  if [ "$have_min" -gt "$want_min" ]; then return 0; fi
  if [ "$have_min" -lt "$want_min" ]; then return 1; fi
  if [ "$have_pat" -lt "$want_pat" ]; then return 1; fi
  return 0
}

if ! command -v "$GO" >/dev/null 2>&1; then
  die_fix "no Go toolchain on PATH (app/go.mod needs go >= $GO_MIN)." \
    "brew install go" \
    "" \
    "Or download the darwin-arm64 pkg from https://go.dev/dl/ ." \
    "If Go is installed somewhere unusual: GO=/path/to/go bash scripts/mac-release.sh"
fi
GO_VER="$("$GO" env GOVERSION 2>/dev/null | sed 's/^go//')"
[ -n "$GO_VER" ] || GO_VER="$("$GO" version 2>/dev/null | sed -n 's/^go version go\([0-9][0-9.]*\).*/\1/p')"
if ! ver_ge "${GO_VER:-0}" "$GO_MIN"; then
  die_fix "Go $GO_VER is older than app/go.mod's minimum ($GO_MIN)." \
    "brew upgrade go     # or: brew install go" \
    "" \
    "Then confirm: go env GOVERSION"
fi
ok "go $GO_VER (>= $GO_MIN, from app/go.mod)"

# cgo must be usable — this is THE thing that separates a mac build from a
# Linux one, so prove it now with a real cgo compile rather than discovering
# it inside a 12 MB build. `go env CC` is what cgo will actually invoke.
if [ "$("$GO" env CGO_ENABLED)" = "0" ]; then
  die_fix "CGO_ENABLED is 0 in your Go environment; darwin/arm64 cannot be built without cgo." \
    "go env -u CGO_ENABLED" \
    "" \
    "(Or unset CGO_ENABLED in your shell profile. provider_darwin.go is" \
    "Objective-C and NewDarwinProvider has no non-cgo definition, so a" \
    "CGO_ENABLED=0 darwin build fails to LINK.)"
fi
ok "cgo enabled (CC=$("$GO" env CC))"

# ---- rustup / cargo -------------------------------------------------------
if ! command -v cargo >/dev/null 2>&1; then
  die_fix "cargo is not on PATH — the Tauri desktop bundle needs the Rust toolchain." \
    "curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh" \
    "source \"\$HOME/.cargo/env\"" \
    "" \
    "(If rustup is already installed, the second line is all you need — a fresh" \
    "non-login shell often does not have ~/.cargo/bin on PATH.)"
fi
ok "cargo $(cargo --version 2>/dev/null | awk '{print $2}')"

if ! rustup target list --installed 2>/dev/null | grep -qx "$RUST_TRIPLE"; then
  # Not fatal on an arm64 Mac (the host target is installed by definition when
  # rustup installed the toolchain), but a missing/absent rustup is worth
  # saying out loud rather than letting cargo fail cryptically later.
  warn "rustup does not report the '$RUST_TRIPLE' target as installed."
  warn "If 'cargo tauri build' later fails to find a target, run: rustup target add $RUST_TRIPLE"
fi

# ---- tauri-cli ------------------------------------------------------------
# `cargo tauri --version` is the honest check: `command -v cargo-tauri` can
# find a binary that a toolchain change has left unrunnable.
if ! tauri_ver="$(cargo tauri --version 2>/dev/null)"; then
  die_fix "tauri-cli is not installed (cargo tauri --version failed)." \
    "cargo install tauri-cli --locked" \
    "" \
    "To pin the v2 line explicitly (what docs/plan/TAURI-FIRST-BUILD.md used):" \
    "cargo install tauri-cli --version '^2.0.0' --locked"
fi
case "$tauri_ver" in
  *" 2."*|*"tauri-cli 2"*|2.*) ok "tauri-cli: $tauri_ver" ;;
  *) warn "tauri-cli reports '$tauri_ver' — desktop/src-tauri targets Tauri v2. If bundling fails: cargo install tauri-cli --version '^2.0.0' --locked" ;;
esac

# ---- gh, authenticated, with access to the repo --------------------------
if ! command -v gh >/dev/null 2>&1; then
  die_fix "the GitHub CLI (gh) is not installed — it is how assets reach the release." \
    "brew install gh" \
    "gh auth login"
fi
# In CI, the GitHub Actions GITHUB_TOKEN (exported as GH_TOKEN) is how assets
# reach the release, and gh picks it up from the environment automatically.
# That token is an app token with NO user identity, so `gh auth status` (and
# `gh api user` below) return non-zero against it even though it uploads fine
# with contents:write — the interactive gate would wrongly reject a perfectly
# valid CI run. So when a token is present in the environment, trust it and
# skip the interactive-auth check; the `gh repo view` gate just below still
# confirms the token can actually reach the repo, so this is not blind.
if [ -z "${GH_TOKEN:-}" ] && [ -z "${GITHUB_TOKEN:-}" ] && ! gh auth status >/dev/null 2>&1; then
  die_fix "gh is installed but not authenticated." \
    "gh auth login" \
    "" \
    "Choose GitHub.com, then a protocol, then authenticate in the browser." \
    "The token needs the 'repo' scope to upload release assets."
fi
if ! gh repo view "$GH_REPO" >/dev/null 2>&1; then
  die_fix "gh is authenticated but cannot see $GH_REPO." \
    "gh auth status                       # which account is active?" \
    "gh auth switch                       # switch to the account that owns $GH_REPO" \
    "gh auth refresh -h github.com -s repo   # or re-grant the 'repo' scope"
fi
ok "gh authenticated for $GH_REPO ($(gh api user -q .login 2>/dev/null || echo 'user unknown'))"

# ---- shasum (macOS has no sha256sum) -------------------------------------
# Same reasoning as build-release.sh's sha256_of: fail on a missing checksum
# tool NOW, not after everything has been built and archived.
sha256_of() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1"
  elif command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1"
  else
    echo "ERROR: neither shasum nor sha256sum is available — cannot checksum $1" >&2
    return 1
  fi
}
if ! sha256_of /dev/null >/dev/null 2>&1; then
  die_fix "no usable sha256 tool found (need 'shasum' — it ships with macOS — or 'sha256sum')." \
    "xcode-select --install     # shasum comes with the CLT / system perl" \
    "brew install coreutils     # provides gsha256sum/sha256sum as a fallback"
fi
ok "sha256 via $(command -v shasum >/dev/null 2>&1 && echo 'shasum -a 256' || echo sha256sum)"

# ---- the archive's paperwork, and the go:embed inputs -------------------
# Mirrored from build-release.sh, and it matters just as much here: whatever
# is missing from app/public or app/assets at build time is missing from the
# binary itself, permanently and invisibly (app/embed.go).
for f in $LICENSE_FILES; do
  [ -f "$REPO_ROOT/$f" ] || die "$REPO_ROOT/$f not found — required in every release archive (build-release.sh's layout)"
done
[ -f "$APP_DIR/public/index.html" ] || die "$APP_DIR/public/index.html not found — the frontend bundle is go:embed'd into the binary and must be committed before packaging a release"
[ -f "$APP_DIR/public/js/dexel.js" ] || die_fix "$APP_DIR/public/js/dexel.js not found — the embedded frontend bundle is missing." \
  "cd app/frontend && npm ci && npm run build     # then commit the bundle"
[ -f "$APP_DIR/assets/room_back.png" ] || die_fix "$APP_DIR/assets/room_back.png not found — app/assets/ is missing or incomplete." \
  "python3 tools/gen_assets.py"
ok "licence files + go:embed inputs present"

# ===========================================================================
# 2. TAG resolution and provenance
# ===========================================================================

step "tag"

if [ -z "$TAG" ]; then
  TAG="$(gh release view --repo "$GH_REPO" --json tagName -q .tagName 2>/dev/null || true)"
  [ -n "$TAG" ] || die_fix "no TAG given and no published release found in $GH_REPO to take one from." \
    "gh release list --repo $GH_REPO" \
    "bash scripts/mac-release.sh v0.1.0"
  info "no TAG given — using the latest release tag from $GH_REPO"
fi

case "$TAG" in
  v[0-9]*.[0-9]*.[0-9]*) : ;;
  *) die_fix "TAG '$TAG' does not look like a release tag (expected vMAJOR.MINOR.PATCH, e.g. v0.2.0)." \
       "gh release list --repo $GH_REPO" ;;
esac
ok "TAG=$TAG"

# The release must already exist: this script ATTACHES darwin assets to the
# release release.yml published for this tag, it never creates one. (Creating
# it here would produce a mac-only release with no linux/windows assets and no
# generated notes.)
release_exists=0
if gh release view "$TAG" --repo "$GH_REPO" >/dev/null 2>&1; then
  release_exists=1
  ok "release $TAG exists in $GH_REPO"
else
  if [ "$DRY_RUN" -eq 1 ]; then
    warn "no release '$TAG' in $GH_REPO — fine for --dry-run (nothing is uploaded), but a real run would stop here."
  else
    die_fix "there is no release '$TAG' in $GH_REPO to attach assets to." \
      "git push origin $TAG           # let release.yml build and publish it first" \
      "gh release list --repo $GH_REPO" \
      "" \
      "This script only ADDS darwin assets to an existing release. If you want to" \
      "see what it would build without uploading: bash scripts/mac-release.sh $TAG --dry-run"
  fi
fi

# Provenance: the artifact must be built from the tag it is named after.
head_tags="$(cd "$REPO_ROOT" && git tag --points-at HEAD 2>/dev/null || true)"
tree_dirty="$(cd "$REPO_ROOT" && git status --porcelain -- app desktop scripts 2>/dev/null || true)"
provenance_bad=""
echo "$head_tags" | grep -qx "$TAG" || provenance_bad="HEAD is not at tag $TAG (HEAD tags: ${head_tags:-none}, HEAD: $(cd "$REPO_ROOT" && git rev-parse --short HEAD))"
if [ -n "$tree_dirty" ]; then
  provenance_bad="${provenance_bad:+$provenance_bad; }the working tree is dirty under app/, desktop/ or scripts/"
fi
if [ -n "$provenance_bad" ]; then
  if [ "${DEXEL_ALLOW_TAG_MISMATCH:-0}" = "1" ]; then
    warn "PROVENANCE OVERRIDE: $provenance_bad"
    warn "Building anyway because DEXEL_ALLOW_TAG_MISMATCH=1. The artifacts will be NAMED $TAG regardless."
  else
    die_fix "$provenance_bad" \
      "git fetch --tags && git checkout $TAG" \
      "git status --porcelain           # then commit or stash what it lists" \
      "" \
      "Why this is fatal: the archive is named dexel-$TAG-darwin-arm64 and the" \
      "binary is stamped -X main.version=$TAG. If the source is not the tag, the" \
      "checksum published for $TAG is a hash of something that does not exist in" \
      "git, and no one can ever reproduce it." \
      "" \
      "To override deliberately: DEXEL_ALLOW_TAG_MISMATCH=1 bash scripts/mac-release.sh $TAG"
  fi
else
  ok "HEAD is $TAG, tree clean under app/ desktop/ scripts/"
fi

# ===========================================================================
# 3. SIGNING TIER — decided BEFORE any build, because tauri-bundler reads
#    these variables from the environment at bundle time. Deciding after the
#    build would mean building twice.
# ===========================================================================

step "signing tier"

SIGN_TIER="unsigned"
if [ -n "${APPLE_SIGNING_IDENTITY:-}" ]; then
  SIGN_TIER="signed"
  if [ -n "${APPLE_ID:-}" ] && [ -n "${APPLE_PASSWORD:-}" ] && [ -n "${APPLE_TEAM_ID:-}" ]; then
    SIGN_TIER="notarized"
  fi
fi

case "$SIGN_TIER" in
  notarized)
    ok "tier 3: SIGN + NOTARIZE  (identity: $APPLE_SIGNING_IDENTITY, team: $APPLE_TEAM_ID, apple id: $APPLE_ID)"
    info "tauri-bundler signs, submits to Apple's notary service and staples the ticket."
    info "This adds several minutes to the bundle step and needs network access."
    # A Developer ID identity that is not actually in the keychain is a
    # 6-minutes-later failure; check now.
    if command -v security >/dev/null 2>&1; then
      if ! security find-identity -v -p codesigning 2>/dev/null | grep -Fq "$APPLE_SIGNING_IDENTITY"; then
        die_fix "APPLE_SIGNING_IDENTITY is set to '$APPLE_SIGNING_IDENTITY' but no such code-signing identity is in the keychain." \
          "security find-identity -v -p codesigning     # list what IS available; copy a name from it" \
          "" \
          "If the certificate is not there at all, see docs/plan/MAC-RELEASE.md" \
          "§3 'Getting a Developer ID certificate'."
      fi
    fi
    ;;
  signed)
    ok "tier 2: SIGN ONLY  (identity: $APPLE_SIGNING_IDENTITY)"
    warn "Not notarizing: APPLE_ID / APPLE_PASSWORD / APPLE_TEAM_ID are not all set."
    warn "A signed-but-not-notarized .dmg still trips Gatekeeper on another Mac."
    warn "See docs/plan/MAC-RELEASE.md §2 'The three tiers' and §3."
    if command -v security >/dev/null 2>&1; then
      security find-identity -v -p codesigning 2>/dev/null | grep -Fq "$APPLE_SIGNING_IDENTITY" \
        || die_fix "APPLE_SIGNING_IDENTITY '$APPLE_SIGNING_IDENTITY' is not a code-signing identity in this keychain." \
             "security find-identity -v -p codesigning"
    fi
    ;;
  unsigned)
    ok "tier 1: UNSIGNED"
    info "APPLE_SIGNING_IDENTITY is not set, so nothing will be signed or notarized."
    info "The .dmg will be named with an explicit '-unsigned' suffix so it cannot be"
    info "mistaken for a signed build, and the download caveat is printed at the end."
    if [ -n "${APPLE_ID:-}" ] || [ -n "${APPLE_TEAM_ID:-}" ]; then
      warn "APPLE_ID and/or APPLE_TEAM_ID are set but APPLE_SIGNING_IDENTITY is NOT."
      warn "Notarization requires a signature first, so those are being ignored."
    fi
    ;;
esac

# ===========================================================================
# 4. BUILD the CLI/runtime binary, test it, package it exactly as
#    scripts/build-release.sh does.
# ===========================================================================

ARCHIVE_BASE="dexel-$TAG-$GOOS-$GOARCH"
STAGE="$DIST_DIR/$ARCHIVE_BASE"
TARBALL="$DIST_DIR/$ARCHIVE_BASE.tar.gz"

mkdir -p "$DIST_DIR"

step "build dexel $TAG for $GOOS/$GOARCH (CGO_ENABLED=1)"

# Idempotent: a re-run rebuilds from scratch rather than layering onto a
# previous run's staging directory.
rm -rf "$STAGE"
mkdir -p "$STAGE"
rm -f "$TARBALL"

# Identical flags to build-release.sh's build_one: -trimpath, and
# -ldflags "-s -w -X main.version=$VERSION". -s -w is ~31.7% off the binary
# with nothing the product reads lost (Go panics keep full stack traces —
# that comes from .gopclntab, which -s -w does not touch). -X main.version
# is what makes `dexel version` and /api/health honest once the binary has
# been unpacked on a machine with no .git anywhere near it.
(
  cd "$APP_DIR"
  CGO_ENABLED=1 GOOS="$GOOS" GOARCH="$GOARCH" "$GO" build -trimpath \
    -ldflags "-s -w -X main.version=$TAG" -o "$STAGE/dexel" .
)
ok "built $STAGE/dexel ($(wc -c <"$STAGE/dexel" | tr -d ' ') bytes)"

step "verify the binary reports $TAG"
version_out="$("$STAGE/dexel" version)"
info "$version_out"
case "$version_out" in
  "dexel $TAG ("*) ok "version stamp correct" ;;
  *) die "the binary reports '$version_out' but should start with 'dexel $TAG (' — the -X main.version ldflag did not take. Do not ship this." ;;
esac

step "go test ./... (with -race)"
# -race on darwin needs no CC juggling: cgo's default compiler on darwin is
# clang, which the CLT check above already proved works. That is why this
# calls `go test` directly instead of scripts/test-race.sh — that script
# exists to solve a LINUX problem (the runner has no gcc, so it probes and
# exports CC before running). Nothing it does is needed or wanted here.
(
  cd "$APP_DIR"
  CGO_ENABLED=1 "$GO" test -race ./...
)
ok "test suite green"

step "package $ARCHIVE_BASE.tar.gz"
# The archive is the binary and its paperwork, nothing else — no public/ or
# assets/ directory, because app/embed.go compiled both into the binary
# (EMBED-1). Layout, filename and tar invocation all mirror
# scripts/build-release.sh exactly, so this archive is indistinguishable from
# one the Linux pipeline would have produced if it could.
for f in $LICENSE_FILES; do
  cp "$REPO_ROOT/$f" "$STAGE/$f"
done
tar -C "$DIST_DIR" -czf "$TARBALL" "$ARCHIVE_BASE"
ok "$TARBALL"
info "contents:"
tar -tzf "$TARBALL" | sed 's/^/      /'

# ===========================================================================
# 5. BUILD the sidecar, then the desktop bundle.
# ===========================================================================

step "build the Tauri sidecar (scripts/build-sidecar.sh)"
# On a darwin host build-sidecar.sh resolves the host triple itself and emits
# desktop/src-tauri/binaries/Dexel-aarch64-apple-darwin — the exact name
# tauri.conf.json's bundle.externalBin ("binaries/Dexel") resolves. It reads
# DEXEL_RELEASE_VERSION for the same -X main.version stamp, so the daemon
# inside the .app reports $TAG too, not a git describe.
DEXEL_RELEASE_VERSION="$TAG" GO="$GO" bash "$SCRIPT_DIR/build-sidecar.sh"

SIDECAR="$TAURI_DIR/binaries/Dexel-$RUST_TRIPLE"
[ -f "$SIDECAR" ] || die "expected the sidecar at $SIDECAR and it is not there — bundle.externalBin resolves exactly this name, so the bundle would ship without a runtime. Check scripts/build-sidecar.sh's output above."
sidecar_ver="$("$SIDECAR" version 2>/dev/null || true)"
case "$sidecar_ver" in
  "dexel $TAG ("*) ok "sidecar $SIDECAR reports: $sidecar_ver" ;;
  *) die "the sidecar reports '$sidecar_ver' rather than 'dexel $TAG (...)'. The bundle would ship a mis-stamped runtime." ;;
esac

step "cargo tauri build"
# tauri.conf.json carries its own bundle version, independent of the git tag.
# A mismatch is not fatal (the .dmg is renamed to the release convention
# below either way) but the version INSIDE the app bundle's Info.plist comes
# from that file, so say it out loud.
conf_ver="$(sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$TAURI_DIR/tauri.conf.json" | head -n1)"
if [ -n "$conf_ver" ] && [ "v$conf_ver" != "$TAG" ]; then
  warn "desktop/src-tauri/tauri.conf.json says version \"$conf_ver\" but this release is $TAG."
  warn "The .dmg will be named for $TAG, but the .app's Info.plist will say $conf_ver."
  warn "Bump \"version\" in tauri.conf.json (and commit it) if the bundle should match the tag."
fi

# The one Linux-only workaround that must NOT leak here: PKG_CONFIG /
# GDK_BACKEND are for webkit2gtk. macOS uses WKWebView from the system, so
# there is nothing to point pkg-config at.
(
  cd "$TAURI_DIR"
  cargo tauri build
)

# Locate the outputs by glob rather than by an assumed filename: the name
# tauri-bundler produces is derived from productName + the conf version +
# the arch ("Dexel_0.1.0_aarch64.dmg" today) and none of those are ours to fix.
BUNDLE_DIR="$TAURI_DIR/target/release/bundle"
APP_PATH=""
for candidate in "$BUNDLE_DIR/macos/"*.app; do
  [ -d "$candidate" ] || continue
  APP_PATH="$candidate"
done
[ -n "$APP_PATH" ] || die "no .app found under $BUNDLE_DIR/macos/ after 'cargo tauri build'. Check its output above."
ok ".app: $APP_PATH"

SRC_DMG=""
for candidate in "$BUNDLE_DIR/dmg/"*.dmg; do
  [ -f "$candidate" ] || continue
  SRC_DMG="$candidate"
done
if [ -z "$SRC_DMG" ]; then
  die_fix "no .dmg found under $BUNDLE_DIR/dmg/ — the .app built but the disk image did not." \
    "Run this from a Terminal on the Mac's own desktop, not over ssh/tmux:" \
    "  cd desktop/src-tauri && cargo tauri build --bundles dmg" \
    "" \
    "Why: tauri-bundler's dmg step drives Finder via AppleScript to lay the" \
    "window out, and that needs a real logged-in GUI session. It also needs" \
    "hdiutil, which fails if another dexel .dmg is still mounted:" \
    "  hdiutil info | grep -i dexel     # then: hdiutil detach <the /dev/diskNsM>"
fi
ok "dmg: $SRC_DMG"

# ===========================================================================
# 6. VERIFY the signature — the tier decides how hard this is asserted.
# ===========================================================================

DMG_SUFFIX=""
[ "$SIGN_TIER" = "unsigned" ] && DMG_SUFFIX="-unsigned"
DMG_NAME="dexel-$TAG-$GOOS-$GOARCH$DMG_SUFFIX.dmg"
DMG="$DIST_DIR/$DMG_NAME"

rm -f "$DMG"
cp "$SRC_DMG" "$DMG"
ok "staged $DMG"

step "verify signing ($SIGN_TIER)"
case "$SIGN_TIER" in
  unsigned)
    # Say what is true, and prove it rather than asserting it.
    if codesign -dv "$APP_PATH" 2>&1 | grep -q 'Signature=adhoc\|code object is not signed'; then
      ok "confirmed unsigned/ad-hoc, as expected for this tier"
    else
      warn "codesign reports something on $APP_PATH even though no signing identity was set:"
      codesign -dv "$APP_PATH" 2>&1 | sed 's/^/      /' || true
    fi
    ;;
  signed|notarized)
    info "codesign --verify --deep --strict on the .app"
    if codesign --verify --deep --strict --verbose=2 "$APP_PATH" 2>&1 | sed 's/^/      /'; then
      ok "code signature valid"
    else
      die "codesign --verify failed on $APP_PATH — the bundle is not correctly signed. Do not ship it."
    fi

    # Gatekeeper's own opinion on the disk image. --context
    # context:primary-signature is the incantation that makes spctl assess the
    # dmg's signature rather than the quarantine policy for a downloaded file.
    info "spctl -a -t open --context context:primary-signature on the .dmg"
    spctl_out="$(spctl -a -t open --context context:primary-signature -v "$DMG" 2>&1 || true)"
    printf '      %s\n' "$spctl_out"

    info "xcrun stapler validate on the .dmg"
    stapler_out="$(xcrun stapler validate "$DMG" 2>&1 || true)"
    printf '      %s\n' "$stapler_out"

    if [ "$SIGN_TIER" = "notarized" ]; then
      # Tier 3 claims notarization, so tier 3 proves it. Both of these are
      # fatal here and advisory in tier 2, which is the whole difference
      # between the tiers as far as verification is concerned.
      case "$spctl_out" in
        *accepted*) ok "Gatekeeper accepts the .dmg" ;;
        *) die "spctl did not accept $DMG ('$spctl_out'). A notarized build must be accepted — check the notarization output in the cargo tauri build log above." ;;
      esac
      case "$stapler_out" in
        *"validate action worked"*) ok "notarization ticket is stapled" ;;
        *) die "stapler could not validate a ticket on $DMG ('$stapler_out'). It was submitted for notarization but the ticket is not stapled — do not publish it as notarized." ;;
      esac
    else
      case "$spctl_out" in
        *accepted*) ok "Gatekeeper accepts the .dmg (unexpected for a non-notarized build, but good)" ;;
        *) warn "Gatekeeper does not accept the .dmg: $spctl_out" ;;
      esac
      warn "This tier is signed but NOT notarized: downloaders will still see a Gatekeeper prompt."
    fi
    ;;
esac

# ===========================================================================
# 7. CHECKSUMS + UPLOAD
# ===========================================================================

step "checksums"

# The darwin lines, in build-release.sh's exact "<hex>  <name>" two-space
# format so `shasum -a 256 -c` / `sha256sum -c` read them back unchanged, and
# a two-field awk (what install.sh uses) finds them.
NEW_SUMS="$DIST_DIR/.mac-release-new-sums.txt"
: > "$NEW_SUMS"
(
  cd "$DIST_DIR"
  sha256_of "$ARCHIVE_BASE.tar.gz"
  sha256_of "$DMG_NAME"
) >> "$NEW_SUMS"
sed 's/^/    /' "$NEW_SUMS"

# THE TIER-TRANSITION TRAP. The .dmg changes NAME between tiers
# (dexel-$TAG-darwin-arm64.dmg vs ...-unsigned.dmg), so re-running this script
# with signing switched on does not overwrite the unsigned asset — it adds a
# second one, and the unsigned line stays in sha256sums.txt forever, next to
# the signed one, for the same release. Whichever dmg name this run did NOT
# produce is therefore treated as obsolete: its line is dropped from the
# merged checksums, and the asset itself is deleted from the release after the
# upload succeeds.
OBSOLETE_NAMES="$DIST_DIR/.mac-release-obsolete.txt"
: > "$OBSOLETE_NAMES"
for candidate in "dexel-$TAG-$GOOS-$GOARCH.dmg" "dexel-$TAG-$GOOS-$GOARCH-unsigned.dmg"; do
  [ "$candidate" = "$DMG_NAME" ] && continue
  echo "$candidate" >> "$OBSOLETE_NAMES"
done
if [ -s "$OBSOLETE_NAMES" ]; then
  info "superseded by this run's tier ($SIGN_TIER), will be removed if present:"
  sed 's/^/      /' "$OBSOLETE_NAMES"
fi

# merge_sums <current> <new-lines> <obsolete-names>   -> merged file on stdout
#
# The part people get wrong. The release's sha256sums.txt was written by
# release.yml's Linux job and already covers linux/* and windows/*; this run
# must ADD the darwin lines without dropping those, and a SECOND run of this
# script must produce the same file rather than a file with the darwin lines
# twice. So: dedupe by FILENAME (field 2), new lines win, existing lines keep
# their original order, darwin lines are appended at the end, and any
# obsolete name is dropped outright.
#
# `*` prefixes (what `sha256sum -b` emits) are stripped for the comparison
# only, so `<hash> *name` and `<hash>  name` are recognised as the same file.
#
# The three inputs are told apart by FILENAME rather than by NR==FNR (which
# only distinguishes two) or gawk's ARGIND (which the awk macOS ships does
# not have).
merge_sums() {
  awk -v NEWF="$2" -v OBSF="$3" '
    function key(s) { sub(/^\*/, "", s); return s }
    # the lines we are about to add — remember their filenames
    FILENAME == NEWF { if (NF >= 2) drop[key($2)] = 1; next }
    # names superseded by this run - one bare filename per line
    FILENAME == OBSF { if (NF >= 1) drop[key($1)] = 1; next }
    # the existing file — keep every line whose filename we are not replacing
    NF >= 2 && (key($2) in drop) { next }
    NF >= 1 { print }
  ' "$2" "$3" "$1"
  cat "$2"
}

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/dexel-mac-release.XXXXXX")"
trap 'rm -rf "$TMP_DIR"' EXIT INT TERM

CURRENT_SUMS="$TMP_DIR/sha256sums.current.txt"
: > "$CURRENT_SUMS"
if [ "$release_exists" -eq 1 ]; then
  if gh release download "$TAG" --repo "$GH_REPO" --pattern sha256sums.txt --dir "$TMP_DIR" >/dev/null 2>&1; then
    mv "$TMP_DIR/sha256sums.txt" "$CURRENT_SUMS"
    ok "downloaded the release's current sha256sums.txt ($(grep -c . "$CURRENT_SUMS" | tr -d ' ') line(s))"
  else
    warn "release $TAG has no sha256sums.txt asset — writing a fresh one containing only the darwin lines."
    warn "That is expected only if release.yml never published one for this tag. Check: gh release view $TAG --repo $GH_REPO"
  fi
fi

MERGED_SUMS="$DIST_DIR/sha256sums.merged.txt"
merge_sums "$CURRENT_SUMS" "$NEW_SUMS" "$OBSOLETE_NAMES" > "$MERGED_SUMS"
info "merged sha256sums.txt:"
sed 's/^/      /' "$MERGED_SUMS"

# Self-check: the merge must be a superset of the non-darwin lines and must
# contain each darwin filename exactly once. Cheap, and it catches an awk
# mistake before it overwrites the published file.
while read -r _hash name; do
  [ -n "${name:-}" ] || continue
  n="$(awk -v f="$name" '{ nm=$2; sub(/^\*/,"",nm); if (nm==f) c++ } END { print c+0 }' "$MERGED_SUMS")"
  [ "$n" = "1" ] || die "merge self-check failed: '$name' appears $n time(s) in the merged sha256sums.txt (expected exactly 1)."
done < "$NEW_SUMS"
while read -r _hash name; do
  [ -n "${name:-}" ] || continue
  # A name this run deliberately superseded (the other tier's .dmg) is
  # SUPPOSED to be gone, so it is not a failure.
  if grep -Fqx "$name" "$OBSOLETE_NAMES"; then
    info "dropped the superseded line for $name"
    continue
  fi
  grep -Fq "$name" "$MERGED_SUMS" || die "merge self-check failed: '$name' was in the published sha256sums.txt and is missing from the merged one."
done < "$CURRENT_SUMS"
ok "merge self-check passed (every existing line kept, each darwin line present once)"

step "upload"
if [ "$DRY_RUN" -eq 1 ]; then
  warn "--dry-run: NOTHING is being uploaded."
  info "What a real run would do, in this order:"
  info "  gh release upload $TAG '$TARBALL' '$DMG' --repo $GH_REPO --clobber"
  info "  cp '$MERGED_SUMS' <tmp>/sha256sums.txt"
  info "  gh release upload $TAG <tmp>/sha256sums.txt --repo $GH_REPO --clobber"
  while read -r obsolete; do
    [ -n "${obsolete:-}" ] || continue
    info "  gh release delete-asset $TAG '$obsolete' --repo $GH_REPO --yes   # superseded by tier '$SIGN_TIER'"
  done < "$OBSOLETE_NAMES"
else
  # Artifacts FIRST, then the checksum file. This is the one place this script
  # deliberately reverses the obvious order: a sha256sums.txt that lists a
  # .dmg which is not attached yet sends every reader to a 404, whereas an
  # artifact whose line is not published yet fails closed — install.sh's
  # "no build for this platform in this version" path, which is honest and
  # actionable. The window is seconds either way; only one of them lies.
  gh release upload "$TAG" "$TARBALL" "$DMG" --repo "$GH_REPO" --clobber
  ok "uploaded $(basename "$TARBALL") and $DMG_NAME"

  cp "$MERGED_SUMS" "$TMP_DIR/sha256sums.txt"
  gh release upload "$TAG" "$TMP_DIR/sha256sums.txt" --repo "$GH_REPO" --clobber
  ok "uploaded the merged sha256sums.txt"

  # Read it back. A release nobody verified after upload is a release nobody
  # verified (RELEASE_PIPELINE.md §5.1 step 6, same principle).
  if gh release download "$TAG" --repo "$GH_REPO" --pattern sha256sums.txt --dir "$TMP_DIR/verify" >/dev/null 2>&1; then
    if diff -u "$MERGED_SUMS" "$TMP_DIR/verify/sha256sums.txt" >/dev/null; then
      ok "verified: the published sha256sums.txt matches what was merged locally"
    else
      warn "the published sha256sums.txt differs from the local merge — diff:"
      diff -u "$MERGED_SUMS" "$TMP_DIR/verify/sha256sums.txt" | sed 's/^/      /' || true
      die "refusing to report success: the release's sha256sums.txt is not what this run produced. Re-run the script."
    fi
  else
    warn "could not re-download sha256sums.txt to verify the upload. Check by hand: gh release view $TAG --repo $GH_REPO"
  fi

  # Only now — after the replacement is definitely published — remove the
  # other tier's .dmg, so the release never advertises two different disk
  # images for the same tag. Done last on purpose: a failed upload above
  # leaves the old asset in place rather than leaving the release with none.
  while read -r obsolete; do
    [ -n "${obsolete:-}" ] || continue
    if gh release view "$TAG" --repo "$GH_REPO" --json assets \
         -q '.assets[].name' 2>/dev/null | grep -Fqx "$obsolete"; then
      if gh release delete-asset "$TAG" "$obsolete" --repo "$GH_REPO" --yes; then
        ok "deleted the superseded asset $obsolete (this run produced $DMG_NAME)"
      else
        warn "could not delete the superseded asset $obsolete — remove it by hand:"
        warn "  gh release delete-asset $TAG '$obsolete' --repo $GH_REPO --yes"
      fi
    fi
  done < "$OBSOLETE_NAMES"
fi

# ===========================================================================
# 8. SUMMARY
# ===========================================================================

printf '\n%s========================================================%s\n' "$C_B" "$C_RST"
printf '%s dexel %s — macOS release summary%s\n' "$C_B" "$TAG" "$C_RST"
printf '%s========================================================%s\n' "$C_B" "$C_RST"
printf '  host       : macOS %s / %s\n' "$(sw_vers -productVersion 2>/dev/null || echo '?')" "$host_arch"
printf '  target     : %s/%s (CGO_ENABLED=1, native)\n' "$GOOS" "$GOARCH"
printf '  signing    : %s\n' "$SIGN_TIER"
printf '  tests      : go test -race ./...  PASSED\n'
printf '  built      : %s\n' "$TARBALL"
printf '               %s\n' "$DMG"
printf '               %s  (inside the .dmg)\n' "$APP_PATH"
printf '               %s  (the bundled runtime)\n' "$SIDECAR"
if [ "$DRY_RUN" -eq 1 ]; then
  printf '  uploaded   : %sNOTHING (--dry-run)%s\n' "$C_YEL" "$C_RST"
  printf '  checksums  : merged locally into %s\n' "$MERGED_SUMS"
else
  printf '  uploaded   : %s\n' "$(basename "$TARBALL")"
  printf '               %s\n' "$DMG_NAME"
  printf '               sha256sums.txt (merged: darwin lines added/replaced)\n'
  printf '  release    : https://github.com/%s/releases/tag/%s\n' "$GH_REPO" "$TAG"
fi

printf '\n%sThe two commands a Mac user runs now%s\n' "$C_B" "$C_RST"
printf '  1. The installer (it reads this release through the GitHub API, finds the\n'
printf '     darwin-arm64 archive this run just attached, and verifies it against\n'
printf '     the merged sha256sums.txt — no edit to install.sh was needed):\n\n'
printf '     curl -fsSL https://raw.githubusercontent.com/%s/main/install.sh | bash\n' "$GH_REPO"
printf '\n     While the repo is still private that needs a token:\n'
printf '     GITHUB_TOKEN=<token> ... | bash        (and later: curl -fsSL https://get.dexel.jwdlab.com/install.sh | sh)\n'
printf '\n  2. By hand, today, with no installer at all:\n\n'
printf '     curl -fsSLO https://github.com/%s/releases/download/%s/%s\n' "$GH_REPO" "$TAG" "$(basename "$TARBALL")"
printf '     curl -fsSLO https://github.com/%s/releases/download/%s/sha256sums.txt\n' "$GH_REPO" "$TAG"
printf '     grep %s shasum -a 256 -c -    # verify just this one line\n' "'$(basename "$TARBALL")' sha256sums.txt |"
printf '     tar -xzf %s\n' "$(basename "$TARBALL")"
printf '     install -m 0755 %s/dexel ~/.local/bin/dexel && dexel version\n' "$ARCHIVE_BASE"
printf '\n  The desktop app (a human download, not part of either path above):\n'
printf '     open %s   # then drag Dexel.app to /Applications\n' "$DMG_NAME"

if [ "$SIGN_TIER" = "unsigned" ]; then
  printf '\n%sUNSIGNED BUILD — the caveat, verbatim, for the release notes%s\n' "$C_YEL" "$C_RST"
  printf '  This .dmg is not signed or notarized, so Gatekeeper will refuse to open\n'
  printf '  Dexel.app on first launch ("Apple could not verify ... is free of malware").\n'
  printf '  The user must RIGHT-CLICK (or Control-click) Dexel.app in /Applications and\n'
  printf '  choose Open, then confirm once. Double-clicking will not offer that choice.\n'
  printf '  On macOS 15+ the path is System Settings > Privacy & Security > "Open Anyway"\n'
  printf '  after the first blocked attempt.\n'
  printf '  Removing the quarantine flag manually also works and is worth documenting\n'
  printf '  only for the owner, never for users:\n'
  printf '    xattr -dr com.apple.quarantine /Applications/Dexel.app\n'
  printf '  To ship without this caveat, see docs/plan/MAC-RELEASE.md §2-3 (tier 3).\n'
fi

printf '\n'
