#!/bin/sh
# install.sh — the one-line installer for dexel, your developer companion.
#
#   curl -fsSL https://raw.githubusercontent.com/jawwadzafar/dexel/main/install.sh | bash
#   curl -fsSL https://get.dexel.jwdlab.com/install.sh | sh          (later, same file)
#
# Stage 1 of docs/production-runtime/RELEASE_PIPELINE.md § 6: the ten
# steps of that contract, but resolving the release through the **GitHub
# API** instead of an R2 `latest/VERSION` + `checksums.txt` pair, because
# GitHub Releases is what exists today and R2 is not provisioned yet. When
# R2 lands, only `resolve_release` and `asset_url` change; every other step
# below is already the shape the pipeline doc asks for.
#
# Written for POSIX `sh` — no bashisms, so `| sh`, `| bash`, `| dash` and
# busybox `ash` all behave identically:
#   * no `local`, no arrays, no `[[`, no `+=`, no `pipefail`, no process
#     substitution, no `echo -n`;
#   * every line of real work lives in a function and the only top-level
#     statement is `main "$@"` on the very LAST line, so a connection that
#     dies mid-download can never execute a half-parsed script;
#   * `set -eu` (never `pipefail`, which `sh` lacks).
#
# Never uses sudo. Never writes outside $HOME. Never enables autostart and
# never starts the runtime — `dexel` and `dexel autostart enable` are the
# user's explicit, informed choices (ARCHITECTURE.md's consent rule).
#
# Options (flags, or the environment):
#   --dry-run                resolve + download + verify, then stop. No writes
#                            outside the temp dir. This is the mode CI runs.
#   --help                   this text
#   DEXEL_INSTALL_DIR=DIR    where the binary goes (default ~/.local/bin)
#   DEXEL_VERSION=vX.Y.Z     install this tag instead of the latest release
#   DEXEL_REPO=owner/name    resolve against a different repository
#   DEXEL_ARCHIVE=FILE       use an archive already on disk instead of
#                            downloading one. The checksum is still verified
#                            against the release's sha256sums.txt — this is an
#                            offline/air-gapped convenience, not a bypass.
#   GITHUB_TOKEN / GH_TOKEN  sent as a bearer token. Required only while the
#                            repository is private; ignored once it is public.
#   DEXEL_UNAME_S / _M       override `uname -s` / `uname -m` (testing)
#
# Exit codes, so a piped run is diagnosable from $? alone:
#   2 usage   3 unsupported platform   4 missing tool   5 no build for this
#   platform in this release   6 checksum mismatch   7 network/API failure
#   8 the installed binary failed its own version check

set -eu

# ---------------------------------------------------------------------------
# constants and state
# ---------------------------------------------------------------------------

REPO="${DEXEL_REPO:-jawwadzafar/dexel}"
API="https://api.github.com/repos/${REPO}"

E_USAGE=2
E_PLATFORM=3
E_TOOL=4
E_NOBUILD=5
E_CHECKSUM=6
E_NETWORK=7
E_VERIFY=8

OS=""            # linux | darwin
ARCH=""          # amd64 | arm64
TAG=""           # e.g. v0.1.0
BINDIR=""
STATEDIR=""
TMPD=""
TOKEN=""
HTTP_CLIENT=""   # curl | wget
SHA_TOOL=""      # sha256sum | shasum
DRY_RUN=0
STOPPED_RUNTIME=0

# ---------------------------------------------------------------------------
# output helpers — every line is prefixed, because in a `curl | sh` the
# user cannot tell our output from the shell's without one.
# ---------------------------------------------------------------------------

say()  { printf '%s\n' "$*"; }
info() { printf '  %s\n' "$*"; }
warn() { printf 'dexel: %s\n' "$*" >&2; }

# die CODE MESSAGE... — the only exit path for a failure.
die() {
    _die_code=$1
    shift
    printf '\ndexel install failed: %s\n' "$*" >&2
    exit "$_die_code"
}

usage() {
    # The header comment IS the help text. Under `curl | sh` there is no
    # script file to read, so fall back to the URL rather than printing
    # nothing at all.
    if [ -r "$0" ] && grep -q '^# install.sh' "$0" 2>/dev/null; then
        sed -n '2,60p' "$0" | sed 's/^# \{0,1\}//'
    else
        say "dexel installer — see https://github.com/${REPO}#install"
        say "options: --dry-run, --help; env: DEXEL_INSTALL_DIR, DEXEL_VERSION,"
        say "DEXEL_REPO, DEXEL_ARCHIVE, GH_TOKEN/GITHUB_TOKEN"
    fi
}

cleanup() {
    if [ -n "$TMPD" ] && [ -d "$TMPD" ]; then
        rm -rf "$TMPD"
    fi
}

# ---------------------------------------------------------------------------
# step 1 — detect OS and architecture
#
# Factored out of main and driven by DEXEL_UNAME_S/_M so the normalisation
# table and both of its error paths are testable on one machine.
# ---------------------------------------------------------------------------

detect_platform() {
    _uname_s="${DEXEL_UNAME_S:-$(uname -s)}"
    _uname_m="${DEXEL_UNAME_M:-$(uname -m)}"

    case "$_uname_s" in
        Linux)  OS=linux ;;
        Darwin) OS=darwin ;;
        MINGW*|MSYS*|CYGWIN*|Windows_NT)
            die "$E_PLATFORM" "this is Windows ($_uname_s). install.sh is for Linux and macOS;
on Windows run the PowerShell one-liner instead:

  irm https://raw.githubusercontent.com/${REPO}/main/install.ps1 | iex"
            ;;
        *)
            die "$E_PLATFORM" "unsupported operating system: $_uname_s
dexel publishes Linux and macOS builds (and Windows via install.ps1).
Build from source instead: https://github.com/${REPO}#building-from-source"
            ;;
    esac

    case "$_uname_m" in
        x86_64|amd64)          ARCH=amd64 ;;
        aarch64|arm64|armv8*)  ARCH=arm64 ;;
        *)
            die "$E_PLATFORM" "unsupported architecture: $_uname_m
dexel publishes amd64 (x86_64) and arm64 (aarch64) builds.
Build from source instead: https://github.com/${REPO}#building-from-source"
            ;;
    esac
}

# ---------------------------------------------------------------------------
# step 2 — check the tools we are about to use, and name the missing one
# ---------------------------------------------------------------------------

have() { command -v "$1" >/dev/null 2>&1; }

check_tools() {
    if have curl; then
        HTTP_CLIENT=curl
    elif have wget; then
        HTTP_CLIENT=wget
    else
        die "$E_TOOL" "need curl or wget to download anything. Install one and re-run."
    fi

    if have sha256sum; then
        SHA_TOOL=sha256sum
    elif have shasum; then
        SHA_TOOL=shasum
    else
        die "$E_TOOL" "need sha256sum or shasum to verify the download. dexel will not
install an unverified binary."
    fi

    for _t in tar mktemp awk sed grep; do
        have "$_t" || die "$E_TOOL" "need $_t, which is not on PATH."
    done
}

resolve_token() {
    # Either spelling; GH_TOKEN wins because that is the one `gh auth token`
    # feeds and the one a developer sets deliberately.
    if [ -n "${GH_TOKEN:-}" ]; then
        TOKEN="$GH_TOKEN"
    elif [ -n "${GITHUB_TOKEN:-}" ]; then
        TOKEN="$GITHUB_TOKEN"
    fi
}

# ---------------------------------------------------------------------------
# HTTPS, once, for everything
#
# The bearer token goes into a 0600 config file inside the temp dir rather
# than onto a command line, where `ps` would show it to every user on the
# box. HTTPS only; there is no plain-HTTP fallback anywhere in this script.
# ---------------------------------------------------------------------------

make_tempdir() {
    TMPD=$(mktemp -d 2>/dev/null || mktemp -d -t dexel) ||
        die "$E_TOOL" "could not create a temporary directory."
    trap cleanup EXIT
    trap 'cleanup; exit 130' INT
    trap 'cleanup; exit 143' TERM

    if [ "$HTTP_CLIENT" = curl ]; then
        {
            printf 'silent\nshow-error\nfail\nlocation\n'
            printf 'proto = "=https"\ntlsv1.2\n'
            printf 'retry = 2\nconnect-timeout = 20\n'
            if [ -n "$TOKEN" ]; then
                printf 'header = "Authorization: Bearer %s"\n' "$TOKEN"
            fi
        } > "$TMPD/curl.conf"
        chmod 600 "$TMPD/curl.conf"
    else
        {
            printf 'quiet = on\ntries = 3\ntimeout = 20\n'
            if [ -n "$TOKEN" ]; then
                printf 'header = Authorization: Bearer %s\n' "$TOKEN"
            fi
        } > "$TMPD/wgetrc"
        chmod 600 "$TMPD/wgetrc"
    fi
}

# http_get URL OUTFILE ACCEPT — returns non-zero on any HTTP or transport
# error (curl `fail`, wget's default), never on a 200 with an error body.
http_get() {
    if [ "$HTTP_CLIENT" = curl ]; then
        curl -K "$TMPD/curl.conf" -H "Accept: $3" -o "$2" "$1"
    else
        WGETRC="$TMPD/wgetrc" wget --https-only --header="Accept: $3" -O "$2" "$1"
    fi
}

token_hint() {
    if [ -n "$TOKEN" ]; then
        say "  A token was sent; check that it can read ${REPO}."
    else
        say "  ${REPO} may still be private. Export a token that can read it:"
        say "      GH_TOKEN=\"\$(gh auth token)\""
    fi
}

# ---------------------------------------------------------------------------
# step 3 — resolve the release: its tag, and an index of its assets
#
# No jq (see RELEASE_PIPELINE.md § 6 — shell JSON parsing is a fragility we
# refuse to *depend* on, and jq is not installed on a stock box either).
# tag_name comes out with sed. The asset list is split on the assets API URL
# prefix — which appears exactly once per asset and nowhere else in the
# document — so each resulting record holds one asset's id, name, digest and
# browser_download_url, whatever whitespace GitHub feels like emitting.
# ---------------------------------------------------------------------------

resolve_release() {
    if [ -n "${DEXEL_VERSION:-}" ]; then
        TAG="$DEXEL_VERSION"
        _rel_url="$API/releases/tags/$TAG"
        _rel_what="release $TAG"
    else
        _rel_url="$API/releases/latest"
        _rel_what="the latest release"
    fi

    say "==> resolving $_rel_what of ${REPO}"
    if ! http_get "$_rel_url" "$TMPD/release.json" "application/vnd.github+json"; then
        say ""
        say "Could not read $_rel_url"
        token_hint
        say "  (An unauthenticated GitHub API allows 60 requests per hour per IP.)"
        die "$E_NETWORK" "could not resolve $_rel_what."
    fi

    TAG=$(tr -d '\r' < "$TMPD/release.json" | tr ',' '\n' |
        sed -n 's/^[[:space:]]*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' |
        head -n 1)
    [ -n "$TAG" ] || die "$E_NETWORK" "no tag_name in the API response for $_rel_what."
    printf '%s' "$TAG" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+' ||
        die "$E_NETWORK" "resolved tag \"$TAG\" is not a vX.Y.Z release tag."

    # name <TAB> asset-api-url <TAB> browser-download-url <TAB> sha256-digest
    awk '
        function jstr(s, key,    re, rest) {
            re = "\"" key "\"[ \t]*:[ \t]*\""
            if (match(s, re) == 0) return ""
            rest = substr(s, RSTART + RLENGTH)
            if (match(rest, /"/) == 0) return ""
            return substr(rest, 1, RSTART - 1)
        }
        { doc = doc $0 "\n" }
        END {
            n = split(doc, part,
                /"url"[ \t]*:[ \t]*"https:\/\/api\.github\.com\/repos\/[^"]*\/releases\/assets\//)
            for (i = 2; i <= n; i++) {
                rec = part[i]
                if (match(rec, /^[0-9]+/) == 0) continue
                id = substr(rec, 1, RLENGTH)
                name = jstr(rec, "name")
                if (name == "") continue
                digest = jstr(rec, "digest")
                sub(/^sha256:/, "", digest)
                printf "%s\t%s/releases/assets/%s\t%s\t%s\n",
                    name, api, id, jstr(rec, "browser_download_url"), digest
            }
        }
    ' api="$API" "$TMPD/release.json" > "$TMPD/assets.tsv"

    [ -s "$TMPD/assets.tsv" ] ||
        die "$E_NOBUILD" "release $TAG has no downloadable assets at all."

    info "release   $TAG"
    info "assets    $(wc -l < "$TMPD/assets.tsv" | tr -d ' ')"
}

# asset_field NAME COLUMN — column 2 = api url, 3 = browser url, 4 = digest.
# Empty output means "this release has no such asset".
asset_field() {
    awk -F '\t' -v want="$1" -v col="$2" '$1 == want { print $col; exit }' "$TMPD/assets.tsv"
}

# asset_url NAME — the URL to fetch this asset from.
#
# Two hosting paths, because they are not interchangeable:
#   * no token  -> browser_download_url. CDN-backed, no API rate limit. This
#                  is the path every public install will take.
#   * token     -> the assets API URL with Accept: application/octet-stream.
#                  Required for a private repo: github.com/.../releases/
#                  download/... answers 404 to a bearer token, verified
#                  against this very repository.
asset_url() {
    if [ -n "$TOKEN" ]; then
        asset_field "$1" 2
    else
        _u=$(asset_field "$1" 3)
        if [ -n "$_u" ]; then
            printf '%s\n' "$_u"
        else
            asset_field "$1" 2
        fi
    fi
}

asset_accept() {
    if [ -n "$TOKEN" ]; then
        printf '%s' "application/octet-stream"
    else
        printf '%s' "*/*"
    fi
}

# ---------------------------------------------------------------------------
# macOS: honest until a darwin artifact exists
#
# This is a check against the RELEASE, not a hardcoded "no mac" — build-
# release.sh adds darwin/arm64 only on a darwin host, so the day a mac
# runner publishes `dexel-<tag>-darwin-arm64.tar.gz` this branch stops
# firing and the normal install path takes over with no edit here.
# ---------------------------------------------------------------------------

require_platform_asset() {
    ARCHIVE_NAME="dexel-${TAG}-${OS}-${ARCH}.tar.gz"
    if [ -n "$(asset_field "$ARCHIVE_NAME" 2)" ]; then
        return 0
    fi

    if [ "$OS" = darwin ]; then
        say ""
        say "macOS builds are not published yet — release $TAG ships no"
        say "$ARCHIVE_NAME (dexel's release runner is Linux; the macOS job is"
        say "a gated no-op until a mac runner is registered)."
        say ""
        say "Build from source instead — two commands, needs Go 1.27+:"
        say ""
        say "  git clone https://github.com/${REPO}.git && cd dexel/app"
        say "  go build -o \"\$HOME/.local/bin/dexel\" . && \"\$HOME/.local/bin/dexel\""
        say ""
        say "This installer will pick up the macOS build automatically once one"
        say "appears in a release — no new version of it is needed."
        exit "$E_NOBUILD"
    fi

    say ""
    say "Release $TAG contains:"
    cut -f1 "$TMPD/assets.tsv" | sed 's/^/    /'
    die "$E_NOBUILD" "no ${OS}-${ARCH} build in release $TAG (expected $ARCHIVE_NAME).
Build from source: https://github.com/${REPO}#building-from-source"
}

# ---------------------------------------------------------------------------
# steps 4 + 5 — download the archive and the checksum file
# ---------------------------------------------------------------------------

download() {
    ARCHIVE_PATH="$TMPD/$ARCHIVE_NAME"

    if [ -n "${DEXEL_ARCHIVE:-}" ]; then
        [ -f "$DEXEL_ARCHIVE" ] ||
            die "$E_USAGE" "DEXEL_ARCHIVE=$DEXEL_ARCHIVE is not a file."
        say "==> using local archive $DEXEL_ARCHIVE (checksum still verified)"
        cp "$DEXEL_ARCHIVE" "$ARCHIVE_PATH"
    else
        say "==> downloading $ARCHIVE_NAME"
        http_get "$(asset_url "$ARCHIVE_NAME")" "$ARCHIVE_PATH" "$(asset_accept)" ||
            die "$E_NETWORK" "download of $ARCHIVE_NAME failed."
    fi

    _sums_url=$(asset_url sha256sums.txt)
    [ -n "$_sums_url" ] ||
        die "$E_CHECKSUM" "release $TAG has no sha256sums.txt, so this download cannot be
verified. Refusing to install."
    say "==> downloading sha256sums.txt"
    http_get "$_sums_url" "$TMPD/sha256sums.txt" "$(asset_accept)" ||
        die "$E_NETWORK" "download of sha256sums.txt failed."
}

# ---------------------------------------------------------------------------
# step 6 — verify sha256. Hard fail, before anything is unpacked.
# ---------------------------------------------------------------------------

sha256_of() {
    if [ "$SHA_TOOL" = sha256sum ]; then
        sha256sum "$1" | cut -d' ' -f1
    else
        shasum -a 256 "$1" | cut -d' ' -f1
    fi
}

verify() {
    # `*name` is the binary-mode spelling both sha256sum and shasum emit.
    _want=$(awk -v f="$ARCHIVE_NAME" \
        '$2 == f || $2 == "*" f { print $1; exit }' "$TMPD/sha256sums.txt")
    [ -n "$_want" ] ||
        die "$E_CHECKSUM" "sha256sums.txt has no line for $ARCHIVE_NAME, so this download
cannot be verified. Refusing to install."

    _got=$(sha256_of "$ARCHIVE_PATH")
    if [ "$_want" != "$_got" ]; then
        say ""
        say "  expected  $_want   (from the release's sha256sums.txt)"
        say "  actual    $_got   ($ARCHIVE_NAME)"
        say ""
        say "  Nothing was unpacked and nothing was installed."
        die "$E_CHECKSUM" "sha256 mismatch on $ARCHIVE_NAME."
    fi

    # Cross-check against the digest the API itself reports for the asset.
    # These two are produced by different systems (build-release.sh writes
    # sha256sums.txt; GitHub hashes what it received) and must never
    # disagree; if they do, one of them is lying and we stop.
    _api_digest=$(asset_field "$ARCHIVE_NAME" 4)
    if [ -n "$_api_digest" ] && [ "$_api_digest" != "$_want" ]; then
        say ""
        say "  sha256sums.txt says  $_want"
        say "  the GitHub API says  $_api_digest"
        die "$E_CHECKSUM" "the release's checksum file and GitHub disagree about
$ARCHIVE_NAME. Refusing to install."
    fi

    say "==> sha256 verified"
    info "$_got"
}

# ---------------------------------------------------------------------------
# step 8 (idempotency) — a re-run is an upgrade in place
#
# `dexel status` exits 0 whether or not a runtime is running (it answers a
# question; not-running is not an error), so the running-ness comes from
# `status --json`. Stopping is a courtesy, not a precondition: on Linux and
# macOS replacing the file swaps the inode and the old process keeps its
# own image, so a stop that fails must not fail the install.
# ---------------------------------------------------------------------------

stop_running_runtime() {
    [ -x "$BINDIR/dexel" ] || return 0
    if ! "$BINDIR/dexel" status --json 2>/dev/null |
        grep -q '"running"[[:space:]]*:[[:space:]]*true'; then
        return 0
    fi
    say "==> a dexel runtime is running — stopping it before upgrading"
    if "$BINDIR/dexel" stop >/dev/null 2>&1; then
        STOPPED_RUNTIME=1
    else
        warn "could not stop the running runtime; upgrading anyway. Run
       \`dexel restart\` afterwards to pick up the new build."
    fi
}

# ---------------------------------------------------------------------------
# step 7 — unpack and install
# ---------------------------------------------------------------------------

install_binary() {
    mkdir -p "$TMPD/x"
    tar -xzf "$ARCHIVE_PATH" -C "$TMPD/x" ||
        die "$E_TOOL" "could not unpack $ARCHIVE_NAME."

    # build-release.sh stages each target as
    # dexel-<version>-<os>-<arch>/{dexel,README.md,LICENSE,...} and tars
    # that directory, so the binary is one level down. A future flat archive
    # would put it at the root; the find is the belt for both braces.
    _src=""
    for _cand in \
        "$TMPD/x/dexel-${TAG}-${OS}-${ARCH}/dexel" \
        "$TMPD/x/dexel"
    do
        if [ -f "$_cand" ]; then
            _src="$_cand"
            break
        fi
    done
    if [ -z "$_src" ]; then
        _src=$(find "$TMPD/x" -type f -name dexel 2>/dev/null | head -n 1)
    fi
    [ -n "$_src" ] || die "$E_TOOL" "no \`dexel\` binary inside $ARCHIVE_NAME."

    mkdir -p "$BINDIR" || die "$E_TOOL" "could not create $BINDIR."
    if have install; then
        install -m 0755 "$_src" "$BINDIR/dexel" ||
            die "$E_TOOL" "could not install to $BINDIR/dexel."
    else
        if cp "$_src" "$BINDIR/dexel"; then
            chmod 0755 "$BINDIR/dexel" ||
                die "$E_TOOL" "could not chmod $BINDIR/dexel."
        else
            die "$E_TOOL" "could not install to $BINDIR/dexel."
        fi
    fi
    say "==> installed $BINDIR/dexel"
}

# Step 8 of the contract: create the state dir and its logs dir, so a first
# `dexel` cannot fail on a missing directory and `dexel status` has
# somewhere to point (PLATFORM_NOTES.md § 1).
make_state_dirs() {
    if [ -n "${DEXEL_HOME:-}" ]; then
        STATEDIR="$DEXEL_HOME"
    elif [ "$OS" = darwin ]; then
        STATEDIR="$HOME/Library/Application Support/dexel"
    else
        STATEDIR="${XDG_CONFIG_HOME:-$HOME/.config}/dexel"
    fi
    mkdir -p "$STATEDIR/logs" ||
        warn "could not create $STATEDIR/logs — dexel will try again on first run."
}

# ---------------------------------------------------------------------------
# step 9 — PATH
#
# Stage 1 PRINTS the line for the detected shell rather than editing a
# dotfile. An installer that silently rewrites ~/.zshrc is exactly the kind
# of surprise the consent rule exists to prevent, and a `curl | sh` reader
# has no diff to review. Copy-paste is one keystroke more and zero
# surprise.
# ---------------------------------------------------------------------------

path_advice() {
    case ":$PATH:" in
        *":$BINDIR:"*)
            info "PATH      already includes $BINDIR"
            return 0
            ;;
    esac

    _shell_name=$(basename "${SHELL:-/bin/sh}")
    case "$_shell_name" in
        fish) _rc="$HOME/.config/fish/config.fish"
              _line="fish_add_path $BINDIR" ;;
        zsh)  _rc="$HOME/.zshrc"
              _line="export PATH=\"$BINDIR:\$PATH\"" ;;
        bash) _rc="$HOME/.bashrc"
              _line="export PATH=\"$BINDIR:\$PATH\"" ;;
        *)    _rc="$HOME/.profile"
              _line="export PATH=\"$BINDIR:\$PATH\"" ;;
    esac

    say ""
    say "$BINDIR is not on your PATH. Add it — this installer will not edit"
    say "your shell config for you:"
    say ""
    say "  echo '$_line' >> $_rc"
    say "  . $_rc"
    say ""
    say "Until then, run it by full path: $BINDIR/dexel"
}

# ---------------------------------------------------------------------------
# step 10 — verify the installed binary, then report
# ---------------------------------------------------------------------------

verify_installed() {
    INSTALLED_VERSION=$("$BINDIR/dexel" version 2>/dev/null || true)
    [ -n "$INSTALLED_VERSION" ] ||
        die "$E_VERIFY" "$BINDIR/dexel installed but \`dexel version\` produced nothing.
The archive may not match this platform ($OS-$ARCH)."
    case "$INSTALLED_VERSION" in
        *"$TAG"*) : ;;
        *) warn "\`dexel version\` says \"$INSTALLED_VERSION\" but $TAG was installed." ;;
    esac
}

report() {
    say ""
    say "$INSTALLED_VERSION"
    say "installed to $BINDIR/dexel"
    say ""
    if [ "$STOPPED_RUNTIME" = 1 ]; then
        say "The runtime that was running before this upgrade was stopped, and this"
        say "installer does not restart things for you. Start it again when you want it:"
        say ""
    fi
    say "Next:"
    say "  dexel                    start the runtime and open the game"
    say "  dexel status             is it running? what is it seeing?"
    say "  dexel stop               shut it down (closing the tab does not)"
    say "  dexel autostart enable   start dexel at login — NOT enabled, this is opt-in"
    say ""
    say "Autostart is off and nothing is running: this installer started no"
    say "processes and enabled no services."
    say ""
    say "Your keystrokes are counted, never read — counts and durations only,"
    say "enforced by build-failing structural tests. Your data stays in"
    say "$STATEDIR and upgrades never touch it."
    say ""
}

# ---------------------------------------------------------------------------
# main — the only top-level statement in this file is its call, on the last
# line, so a truncated download cannot run a partial installer.
# ---------------------------------------------------------------------------

main() {
    while [ $# -gt 0 ]; do
        case "$1" in
            --dry-run) DRY_RUN=1 ;;
            -h|--help) usage; return 0 ;;
            *) printf 'dexel install: unknown option %s\n' "$1" >&2
               printf 'try --help\n' >&2
               return "$E_USAGE" ;;
        esac
        shift
    done

    BINDIR="${DEXEL_INSTALL_DIR:-$HOME/.local/bin}"

    detect_platform                        # 1
    check_tools                            # 2
    resolve_token
    make_tempdir
    say "==> dexel installer — $OS-$ARCH"
    resolve_release                        # 3
    require_platform_asset                 # 3b: macOS/absent-build honesty
    download                               # 4, 5
    verify                                 # 6

    if [ "$DRY_RUN" = 1 ]; then
        say ""
        say "--dry-run: resolved, downloaded and verified $ARCHIVE_NAME."
        say "Nothing was installed."
        return 0
    fi

    stop_running_runtime                   # 8 (idempotent re-run)
    install_binary                         # 7
    make_state_dirs                        # 8
    verify_installed                       # 10
    path_advice                            # 9
    report                                 # 10
}

main "$@"
