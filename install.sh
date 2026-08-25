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
# Never uses sudo. Never writes outside $HOME. Never enables autostart —
# `dexel autostart enable` stays the user's explicit, informed choice
# (ARCHITECTURE.md's consent rule).
#
# It DOES start the runtime and open the game at the end, because "installed"
# and "running" are not the same thing and a one-command install that leaves
# you at a prompt has not finished the job. Starting a process you just asked
# for is not the consent question; *enabling autostart* is, and that is still
# off. `--no-start` opts out.
#
# ON LINUX IT ALSO DOES DESKTOP INTEGRATION, so dexel is in the app grid
# rather than only in a terminal:
#   * the launcher icon from the archive ->
#     $XDG_DATA_HOME/icons/hicolor/128x128/apps/dexel.png
#   * a launcher entry -> $XDG_DATA_HOME/applications/dexel.desktop, whose
#     Exec is `<install-dir>/dexel open`
#   * when a desktop session is detected, the optional GUI shell: the
#     release's AppImage, placed at <install-dir>/dexel-desktop.AppImage with
#     a small `dexel-desktop` shim beside it, which is the name `dexel open`
#     already looks for (ARCHITECTURE.md Decision 17). The .deb on the same
#     release is deliberately NOT used: dpkg needs root and this installer
#     does not sudo, ever. Without the shell, `dexel open` uses the browser,
#     which is a supported front door and not a consolation prize.
# All of it lives under $HOME, is idempotent, and is skippable.
#
# ---------------------------------------------------------------------------
# SOURCE-SELECTION LADDER — this script installs SUCCESSFULLY however it is
# run: from a fresh clone, offline, from a local archive, or the download-
# from-GitHub path it started life as. It picks the highest-confidence source
# available automatically, and each rung is a fallback for the one above it:
#
#   1. BUILD FROM SOURCE  — auto when this script is run from INSIDE the dexel
#      source tree (its own real directory holds app/main.go and an app/go.mod
#      that declares dexel's module path) AND a Go toolchain is on PATH. This
#      is the "clone it and run install.sh — it just works" path: no network
#      and no published release needed. The committed frontend bundle
#      (app/public/js/dexel.js) and the sprites (app/assets) are compiled into
#      the binary by `go build` via app/embed.go, so there is no npm step —
#      a plain `go build` produces the COMPLETE game in one file. The version
#      is stamped from `git describe` (or "dev" outside a git tree). Force it
#      with --from-source even when a release exists — the path a developer
#      testing their own uncommitted changes wants.
#
#   2. LOCAL ARCHIVE      — DEXEL_ARCHIVE=<path> to a .tar.gz already on disk.
#      Fully offline: if a sibling <archive>.sha256 sits beside it the checksum
#      is verified against that; otherwise it is installed with a printed
#      notice that it is an unverified local file you chose. (Add --from-release
#      to instead verify a local archive against the LIVE release's
#      sha256sums.txt — the strongest check, but that one needs the network.)
#
#   3. DOWNLOAD A RELEASE — the original path: resolve the latest (or pinned)
#      GitHub release, download this platform's archive, verify its sha256 and
#      the digest GitHub reports, install. Used when 1 and 2 do not apply, or
#      forced with --from-release.
#
# Run from a clone, offline, with no Go toolchain and no DEXEL_ARCHIVE, none
# of the three can proceed: the script says exactly that (install Go to build,
# or set DEXEL_ARCHIVE) and exits non-zero. It never silently does nothing.
#
# ON GIT BASH / MSYS2 / CYGWIN — a real Windows box whose `uname -s` answers
# MINGW*/MSYS*/CYGWIN* — this script does not just print the PowerShell
# one-liner and stop. It finds powershell.exe or pwsh.exe, downloads
# install.ps1 from this same repo + ref, and runs it, so `curl | bash` is
# one command that works there too. WSL is deliberately NOT this case: its
# `uname -s` answers Linux, so a WSL shell already takes the normal Linux
# path above (see the comment inside detect_platform for why that is the
# right call, not an oversight). If no PowerShell can be found, the exit-3
# message with the one-liner below is still the fallback.
#
# Options (flags, or the environment):
#   --from-source            build from the source tree this script lives in
#                            (needs `go` on PATH), even if a release exists.
#                            The default already does this automatically when
#                            run from a clone with a Go toolchain present.
#   --from-release           skip the source/local-archive rungs and always
#                            resolve + download a GitHub release. With
#                            DEXEL_ARCHIVE set, installs that local archive but
#                            verifies it against the live release's checksums.
#   --dry-run                resolve/build + verify, then stop. No writes
#                            outside the temp dir. This is the mode CI runs.
#   --no-start               install everything, start nothing. The report
#                            still tells you the command to run.
#   --no-desktop             skip ALL desktop integration (icon, .desktop
#                            entry, GUI shell). CLI + browser only.
#   --no-app                 keep the icon and the launcher entry, skip only
#                            the AppImage download (it is ~84 MB).
#   --app                    download the AppImage even with no detected
#                            desktop session (e.g. installing over ssh for a
#                            desktop you will log into later).
#   --help                   this text
#   DEXEL_INSTALL_DIR=DIR    where the binary goes (default ~/.local/bin)
#   DEXEL_NO_START=1         same as --no-start
#   DEXEL_NO_DESKTOP=1       same as --no-desktop
#   DEXEL_NO_APP=1           same as --no-app
#   DEXEL_APP=1              same as --app
#   DEXEL_VERSION=vX.Y.Z     install this tag instead of the latest release
#   DEXEL_REPO=owner/name    resolve against a different repository
#   DEXEL_ARCHIVE=FILE       install from a .tar.gz already on disk instead of
#                            downloading one — the offline rung of the ladder.
#                            Verified against a sibling FILE.sha256 if present;
#                            otherwise installed with an "unverified local
#                            file" notice. With --from-release it is verified
#                            against the live release's sha256sums.txt instead.
#   DEXEL_FROM_SOURCE=1      same as --from-source
#   DEXEL_FROM_RELEASE=1     same as --from-release
#   GITHUB_TOKEN / GH_TOKEN  sent as a bearer token. Required only while the
#                            repository is private; ignored once it is public.
#   DEXEL_UNAME_S / _M       override `uname -s` / `uname -m` (testing)
#
# Exit codes, so a piped run is diagnosable from $? alone:
#   2 usage   3 unsupported platform   4 missing tool (curl/wget/sha/tar, or
#   `go` when building from source)   5 no build for this platform in this
#   release   6 checksum mismatch   7 network/API failure (also: cloned but
#   no way to install — no Go, no archive, no network)   8 the installed
#   binary failed its own version check

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

SOURCE=""        # source | archive | release — chosen by choose_source()
FROM_SOURCE=0    # --from-source / DEXEL_FROM_SOURCE
FROM_RELEASE=0   # --from-release / DEXEL_FROM_RELEASE
IN_REPO=0        # this script's real dir IS the dexel source tree
REPO_DIR=""      # ...and this is that directory (the repo root)
VERSION=""       # the version string a source build stamps in
EXPECT_VERSION="" # what `dexel version` should echo back (TAG, or the built VERSION)
ICON_SRC=""      # the 128x128 PNG to install: from the archive, or the tree
SRC_BIN=""       # a freshly built binary awaiting install (source path)
ARCHIVE_NAME=""  # set on the archive/release paths
ARCHIVE_PATH=""

NO_START=0
NO_DESKTOP=0
NO_APP=0
FORCE_APP=0
HAS_SESSION=0       # a graphical session looks present ($DISPLAY/$WAYLAND_DISPLAY)
WANT_APP=0          # ...and we resolved a verifiable AppImage to install
DATADIR=""          # $XDG_DATA_HOME, or ~/.local/share
UNPACKED_DIR=""     # where install_binary found the binary (the icon is beside it)
APPIMAGE_NAME=""
APPIMAGE_PATH=""
APPIMAGE_DIGEST=""
ICON_INSTALLED=0
DESKTOP_ENTRY=""
SHIM_INSTALLED=0
LAUNCHED=none       # none | open | start

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
        sed -n '2,100p' "$0" | sed 's/^# \{0,1\}//'
    else
        say "dexel installer — see https://github.com/${REPO}#install"
        say "sources (auto-selected; flags force): --from-source, --from-release"
        say "options: --dry-run, --no-start, --no-desktop, --no-app, --app, --help"
        say "env: DEXEL_INSTALL_DIR, DEXEL_VERSION, DEXEL_REPO, DEXEL_ARCHIVE,"
        say "DEXEL_FROM_SOURCE, DEXEL_FROM_RELEASE, DEXEL_NO_START,"
        say "DEXEL_NO_DESKTOP, DEXEL_NO_APP, DEXEL_APP, GH_TOKEN/GITHUB_TOKEN"
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

    case "$_uname_s" in
        Linux)  OS=linux ;;
        Darwin) OS=darwin ;;
        MINGW*|MSYS*|CYGWIN*|Windows_NT)
            # Bash actually running ON Windows (Git Bash, plain MSYS2,
            # Cygwin) reports one of these. Hand off to delegate_windows
            # (called from main) instead of dying here, and return before
            # the arch case below gets a chance to reject an `uname -m`
            # spelling it does not recognise on a platform this script is
            # not even going to install onto — install.ps1 detects the
            # architecture its own way, from real Windows environment
            # variables, not from `uname -m`.
            #
            # WSL is deliberately NOT matched here. WSL's `uname -s` answers
            # "Linux" — it runs a real Linux kernel/userspace, not an
            # emulation layer — so it already takes the Linux branch above
            # with no special-casing needed. That is the right call, not an
            # oversight: a WSL user overwhelmingly wants dexel *inside* WSL
            # (their $PATH, their terminal, their files), not a redirect out
            # to a Windows-side PowerShell install. The unambiguous way to
            # tell WSL apart from a bare Windows shell would be
            # `grep -qi microsoft /proc/version`, but there is nothing to do
            # differently once told apart — the Linux branch already does
            # the right thing for WSL — so that check is not added: dead
            # code that only a comment would explain is worse than the
            # comment alone.
            OS=windows
            return 0
            ;;
        *)
            die "$E_PLATFORM" "unsupported operating system: $_uname_s
dexel publishes Linux and macOS builds (and Windows via install.ps1).
Build from source instead: https://github.com/${REPO}#building-from-source"
            ;;
    esac

    _uname_m="${DEXEL_UNAME_M:-$(uname -m)}"
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

# pick_http_client — sets HTTP_CLIENT, or returns 1 and sets nothing.
# Factored out of check_tools so delegate_windows (below) can ask the same
# question without also demanding sha256sum/tar/etc., none of which the
# Windows-delegation path uses.
pick_http_client() {
    if have curl; then
        HTTP_CLIENT=curl
    elif have wget; then
        HTTP_CLIENT=wget
    else
        return 1
    fi
}

check_tools() {
    pick_http_client ||
        die "$E_TOOL" "need curl or wget to download anything. Install one and re-run."

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
# the source-selection ladder — am I inside the dexel source tree, and if so
# can I build it? (see the SOURCE-SELECTION LADDER block at the top)
# ---------------------------------------------------------------------------

# resolve_script_dir — this script's OWN directory, symlinks resolved, or a
# non-zero return when there is nothing to resolve (a `curl | sh` has no
# on-disk path for itself, so $0 is the shell's name, not a file).
#
# $0 is turned into an absolute path first: absolute already, relative-with-
# slash (./install.sh, sub/install.sh) against $PWD, and a bare name that IS a
# file in $PWD (`sh install.sh` sets $0 to "install.sh"). A bare name that is
# NOT such a file is the piped case — return 1. Then symlinks are chased with
# readlink where it exists (POSIX has no `readlink -f`), capped so a symlink
# cycle cannot spin forever. `cd -P` gives the physical directory.
resolve_script_dir() {
    _src=$0
    case "$_src" in
        /*)  : ;;
        */*) _src="$PWD/$_src" ;;
        *)
            if [ -f "$PWD/$_src" ]; then
                _src="$PWD/$_src"
            else
                return 1
            fi
            ;;
    esac

    if have readlink; then
        _hops=0
        while [ -h "$_src" ] && [ "$_hops" -lt 40 ]; do
            _dir=$(cd -P "$(dirname "$_src")" 2>/dev/null && pwd) || return 1
            _link=$(readlink "$_src") || return 1
            case "$_link" in
                /*) _src="$_link" ;;
                *)  _src="$_dir/$_link" ;;
            esac
            _hops=$((_hops + 1))
        done
    fi

    _dir=$(cd -P "$(dirname "$_src")" 2>/dev/null && pwd) || return 1
    printf '%s\n' "$_dir"
}

# detect_repo — set IN_REPO=1 and REPO_DIR only when this script's real
# directory actually IS the dexel source tree. "A Go repo" is not enough: the
# directory must hold app/main.go AND an app/go.mod that declares dexel's own
# module path, so a copy of install.sh dropped into some other project can
# never be mistaken for the real thing. A fork keeps that module path (it is
# in the file, not the remote), so DEXEL_REPO forks build from source too.
detect_repo() {
    _d=$(resolve_script_dir) || return 0
    if [ -f "$_d/app/main.go" ] &&
       [ -f "$_d/app/go.mod" ] &&
       grep -q '^module github\.com/jawwadzafar/dexel/app$' "$_d/app/go.mod" 2>/dev/null
    then
        IN_REPO=1
        REPO_DIR="$_d"
    fi
}

# choose_source — walk the ladder and set SOURCE to source | archive | release.
# Forcing flags short-circuit the auto choice; --from-source also validates its
# two preconditions loudly rather than silently falling through to a download.
choose_source() {
    if [ "$FROM_SOURCE" = 1 ]; then
        if [ "$IN_REPO" != 1 ]; then
            die "$E_USAGE" "--from-source needs to run from inside the dexel source tree, but this
copy of install.sh is not sitting beside a dexel app/ (or was piped in with no
file on disk). Clone the repo and run ./install.sh --from-source from its root."
        fi
        have go ||
            die "$E_TOOL" "--from-source needs a Go toolchain, but \`go\` is not on PATH.
Install Go 1.27+ (https://go.dev/dl/) and re-run, or drop --from-source to
download a release instead."
        SOURCE=source
        return 0
    fi

    if [ "$FROM_RELEASE" = 1 ]; then
        SOURCE=release
        return 0
    fi

    # Auto: highest-confidence rung that can actually proceed.
    if [ "$IN_REPO" = 1 ] && have go; then
        SOURCE=source
        return 0
    fi
    if [ -n "${DEXEL_ARCHIVE:-}" ]; then
        SOURCE=archive
        return 0
    fi
    SOURCE=release
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
}

# configure_http — write the 0600 curl/wget config the network path uses.
# Split out of make_tempdir so the source and local-archive rungs, which
# touch no network, can have a temp dir without demanding an HTTP client
# (they never call pick_http_client, so HTTP_CLIENT is empty there).
configure_http() {
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
# Windows delegation (Git Bash / MSYS2 / Cygwin) — install.sh's exit-3
# platform check for MINGW*/MSYS*/CYGWIN* used to just print the PowerShell
# one-liner and stop (see detect_platform). Now it calls here instead: find
# powershell.exe/pwsh.exe, fetch install.ps1, run it, and finish with its
# exit code, so `curl | bash` is one command that works on a stock Windows
# box with only Git Bash installed. Reaching this function at all already
# means detect_platform saw MINGW*/MSYS*/CYGWIN* — it is never called for
# WSL, which detect_platform's comment explains.
# ---------------------------------------------------------------------------

# find_powershell — the first working PowerShell, checked in the order a
# modern-first install should prefer it. Git Bash does not put System32 on
# PATH by default, which is why the two absolute fallbacks exist; MSYS2/Git
# Bash and Cygwin mount the C: drive at different points, so both are tried.
find_powershell() {
    for _cand in pwsh pwsh.exe powershell powershell.exe; do
        if have "$_cand"; then
            printf '%s\n' "$_cand"
            return 0
        fi
    done
    for _cand in \
        /c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe \
        /cygdrive/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe
    do
        if [ -x "$_cand" ]; then
            printf '%s\n' "$_cand"
            return 0
        fi
    done
    return 1
}

# to_windows_path PATH — a Windows-shaped path for a POSIX-shaped one, for
# anything powershell.exe has to read back as a path: the -File argument
# itself, and DEXEL_INSTALL_DIR / DEXEL_ARCHIVE / DEXEL_HOME if the caller
# set one.
#
# `cygpath -w`, where present, is authoritative and unambiguous, so prefer
# it. Without it, MSYS2 (Git Bash's own shell) is documented to
# auto-convert an absolute-looking POSIX argument or environment value when
# the program being run is a real Windows binary rather than another MSYS
# one — that heuristic is the only option left on a cygpath-less box, so the
# path is passed through unconverted and left for MSYS2 to translate.
# Cygwin's bash does NOT do this auto-conversion, but every Cygwin install
# ships cygpath, so it is the first branch, not this one, that fires there.
to_windows_path() {
    if have cygpath; then
        cygpath -w "$1"
    else
        printf '%s\n' "$1"
    fi
}

delegate_windows() {
    _psbin=$(find_powershell) ||
        die "$E_PLATFORM" "this is Windows ($_uname_s), and neither pwsh.exe nor powershell.exe
could be found on PATH or at the usual Git-Bash/Cygwin location, so
install.sh cannot delegate to install.ps1 automatically from here.

Run the PowerShell one-liner yourself instead:

  irm https://raw.githubusercontent.com/${REPO}/main/install.ps1 | iex"

    pick_http_client ||
        die "$E_TOOL" "need curl or wget to download install.ps1. Install one, or run the
PowerShell one-liner yourself:

  irm https://raw.githubusercontent.com/${REPO}/main/install.ps1 | iex"
    resolve_token
    make_tempdir
    configure_http

    # Same $REPO, same hardcoded "main" that this script's own die messages
    # already point at elsewhere (see detect_platform, and the fallback
    # above): a script read from stdin via `curl | sh` has no way to know
    # the URL it was itself fetched from, so "the same source" means the
    # same repo + ref this file already treats as canonical everywhere else
    # — including a DEXEL_REPO override for a fork, which applies here too.
    _ps1_url="https://raw.githubusercontent.com/${REPO}/main/install.ps1"
    _ps1_path="$TMPD/install.ps1"

    say "==> Windows detected ($_uname_s) — delegating to install.ps1 via $_psbin"
    say "==> downloading install.ps1"
    # Honesty check: install.ps1 has no checksum sidecar of its own — only
    # the release ARCHIVE is sha256-verified, by both scripts. Fetching this
    # file over HTTPS from a pinned raw.githubusercontent.com URL is the
    # SAME trust level this very install.sh arrived under via `curl | sh`;
    # this is extending that trust one hop, not introducing a weaker one.
    http_get "$_ps1_url" "$_ps1_path" "*/*" ||
        die "$E_NETWORK" "download of install.ps1 failed."

    _win_ps1=$(to_windows_path "$_ps1_path")

    # Flags this script already parsed, translated to the env vars
    # install.ps1 reads — a piped `| iex` cannot take arguments, so
    # install.ps1 is env-var-only, exactly like this script's own env
    # equivalents. DEXEL_VERSION, DEXEL_REPO, GH_TOKEN/GITHUB_TOKEN and
    # DEXEL_NO_PATH need no translation here: if the caller set them, they
    # are already in this process's environment, every child process
    # inherits them unchanged, and install.ps1 reads those exact names too.
    if [ "$NO_START" = 1 ]; then DEXEL_NO_START=1; export DEXEL_NO_START; fi
    if [ "$NO_DESKTOP" = 1 ]; then DEXEL_NO_DESKTOP=1; export DEXEL_NO_DESKTOP; fi
    if [ "$DRY_RUN" = 1 ]; then DEXEL_DRY_RUN=1; export DEXEL_DRY_RUN; fi
    if [ "$NO_APP" = 1 ] || [ "$FORCE_APP" = 1 ]; then
        warn "--no-app/--app (the Linux AppImage desktop shell) have no Windows
       equivalent; install.ps1 has no such shell to skip or force. Ignoring."
    fi

    # Paths, which — unlike the flags above — have to cross the POSIX/
    # Windows boundary translated, not just inherited untouched.
    if [ -n "${DEXEL_INSTALL_DIR:-}" ]; then
        DEXEL_INSTALL_DIR=$(to_windows_path "$DEXEL_INSTALL_DIR")
        export DEXEL_INSTALL_DIR
    fi
    if [ -n "${DEXEL_ARCHIVE:-}" ]; then
        DEXEL_ARCHIVE=$(to_windows_path "$DEXEL_ARCHIVE")
        export DEXEL_ARCHIVE
    fi
    if [ -n "${DEXEL_HOME:-}" ]; then
        DEXEL_HOME=$(to_windows_path "$DEXEL_HOME")
        export DEXEL_HOME
    fi

    # Deliberately not the shell's `exec` builtin: replacing this process's
    # image would skip the EXIT trap that removes $TMPD (install.ps1 has
    # already been read off disk by then, but the temp dir itself would
    # leak). Running it as a normal foreground command keeps that cleanup,
    # and set -eu means a nonzero exit right here still ends install.sh
    # with that SAME code and no extra "dexel install failed" wrapper of
    # our own — install.ps1 already printed its own diagnosis, and doubling
    # it would only confuse whoever is reading the output.
    "$_psbin" -NoProfile -ExecutionPolicy Bypass -File "$_win_ps1"
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
        # The honest-failure case: we are standing in the source tree, so the
        # network is not the only door — name the two that do not need it.
        if [ "$IN_REPO" = 1 ]; then
            say ""
            say "  You are inside the dexel source tree but could not reach GitHub, and"
            say "  there was no Go toolchain to build from source and no DEXEL_ARCHIVE to"
            say "  install from. Any ONE of these finishes the install with no network:"
            say "      * install Go 1.27+ (https://go.dev/dl/), then re-run — it will build"
            say "      * DEXEL_ARCHIVE=/path/to/dexel-*.tar.gz ./install.sh"
        fi
        die "$E_NETWORK" "could not resolve $_rel_what."
    fi

    TAG=$(tr -d '\r' < "$TMPD/release.json" | tr ',' '\n' |
        sed -n 's/^[[:space:]]*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' |
        head -n 1)
    [ -n "$TAG" ] || die "$E_NETWORK" "no tag_name in the API response for $_rel_what."
    printf '%s' "$TAG" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+' ||
        die "$E_NETWORK" "resolved tag \"$TAG\" is not a vX.Y.Z release tag."

    # name <TAB> asset-api-url <TAB> browser-download-url <TAB> sha256-digest
    #      <TAB> size-in-bytes
    awk '
        function jstr(s, key,    re, rest) {
            re = "\"" key "\"[ \t]*:[ \t]*\""
            if (match(s, re) == 0) return ""
            rest = substr(s, RSTART + RLENGTH)
            if (match(rest, /"/) == 0) return ""
            return substr(rest, 1, RSTART - 1)
        }
        # jnum is jstr for an unquoted JSON number. Only "size" is read this
        # way, and within one asset record it appears after "name" and inside
        # no sub-object (the nested "uploader" has no size field), so the
        # first match belongs to this asset.
        function jnum(s, key,    re, rest) {
            re = "\"" key "\"[ \t]*:[ \t]*"
            if (match(s, re) == 0) return ""
            rest = substr(s, RSTART + RLENGTH)
            if (match(rest, /^[0-9]+/) == 0) return ""
            return substr(rest, 1, RLENGTH)
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
                printf "%s\t%s/releases/assets/%s\t%s\t%s\t%s\n",
                    name, api, id, jstr(rec, "browser_download_url"), digest,
                    jnum(rec, "size")
            }
        }
    ' api="$API" "$TMPD/release.json" > "$TMPD/assets.tsv"

    [ -s "$TMPD/assets.tsv" ] ||
        die "$E_NOBUILD" "release $TAG has no downloadable assets at all."

    info "release   $TAG"
    info "assets    $(wc -l < "$TMPD/assets.tsv" | tr -d ' ')"
}

# asset_field NAME COLUMN — column 2 = api url, 3 = browser url, 4 = digest,
# 5 = size in bytes. Empty output means "this release has no such asset".
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
# desktop integration (Linux)
#
# Three separate things, deliberately, because they cost wildly different
# amounts and fail for different reasons:
#
#   1. the ICON      a ~1 KB PNG already inside the archive we verified
#   2. the ENTRY     a ~400 byte .desktop file we write ourselves
#   3. the SHELL     the release's ~84 MB AppImage, plus a `dexel-desktop`
#                    shim so `dexel open` finds it (ARCHITECTURE.md
#                    Decision 17 looks for that name on PATH, then in BinDir)
#
# 1 and 2 are free and always done on Linux; 3 is a real download and is
# only done when there is a graphical session to run it in. Every one of
# them is best-effort AFTER the binary is in place: a desktop environment
# that will not take a launcher is not a reason to fail an install of a CLI
# that works perfectly well from a terminal.
#
# The .deb on the same release is never used. dpkg needs root, and this
# installer does not sudo — which is the whole reason the AppImage is the
# no-sudo path for the GUI shell.
# ---------------------------------------------------------------------------

# detect_session — is there a graphical session to open a window into?
#
# $DISPLAY covers X11 and XWayland; $WAYLAND_DISPLAY covers a pure Wayland
# session that never started XWayland. Neither is a guarantee (a forwarded
# $DISPLAY over ssh sets it too) — which is exactly why this only chooses a
# DEFAULT that --app and --no-app override.
detect_session() {
    if [ -n "${DISPLAY:-}" ] || [ -n "${WAYLAND_DISPLAY:-}" ]; then
        HAS_SESSION=1
    fi
}

human_mb() {
    if [ -z "$1" ]; then
        printf 'size unknown'
        return 0
    fi
    awk -v b="$1" 'BEGIN { printf "%.0f MB", b / 1048576 }'
}

# find_appimage TOKEN1 TOKEN2 — the release's .AppImage for this arch.
#
# Matched by arch token rather than by a constructed name: Tauri names its
# bundles from tauri.conf.json's productName and its own arch spelling
# (`Dexel_0.1.0_amd64.AppImage`), which is neither the tag nor the Go arch
# this script otherwise speaks. An AppImage whose arch we cannot confirm is
# skipped rather than guessed at — a wrong-arch binary is not a launcher
# problem, it is an Exec format error.
find_appimage() {
    awk -F '\t' -v a="$1" -v b="$2" '
        $1 ~ /\.AppImage$/ && (index($1, a) > 0 || index($1, b) > 0) { print $1; exit }
    ' "$TMPD/assets.tsv"
}

# decide_desktop — resolve WANT_APP before anything is downloaded, so an
# 84 MB decision is announced and reversible rather than discovered.
decide_desktop() {
    DATADIR="${XDG_DATA_HOME:-$HOME/.local/share}"

    if [ "$NO_DESKTOP" = 1 ]; then
        info "desktop   skipped entirely (--no-desktop)"
        return 0
    fi
    if [ "$OS" != linux ]; then
        return 0
    fi
    if [ "$NO_APP" = 1 ]; then
        info "shell     skipped (--no-app); \`dexel open\` will use your browser"
        return 0
    fi
    if [ "$HAS_SESSION" = 0 ] && [ "$FORCE_APP" = 0 ]; then
        info "shell     skipped (no \$DISPLAY or \$WAYLAND_DISPLAY; --app forces it)"
        return 0
    fi

    case "$ARCH" in
        amd64) APPIMAGE_NAME=$(find_appimage amd64 x86_64) ;;
        arm64) APPIMAGE_NAME=$(find_appimage arm64 aarch64) ;;
    esac
    if [ -z "$APPIMAGE_NAME" ]; then
        info "shell     release $TAG ships no $ARCH AppImage; \`dexel open\` will use your browser"
        return 0
    fi

    APPIMAGE_DIGEST=$(asset_field "$APPIMAGE_NAME" 4)
    if [ -z "$APPIMAGE_DIGEST" ]; then
        warn "$APPIMAGE_NAME has no sha256 digest in release $TAG, and this
       installer does not install unverified binaries. Skipping the desktop
       shell — \`dexel open\` will use your browser, which works."
        return 0
    fi

    WANT_APP=1
    info "shell     $APPIMAGE_NAME ($(human_mb "$(asset_field "$APPIMAGE_NAME" 5)"))"
}

download_appimage() {
    if [ "$WANT_APP" != 1 ]; then
        return 0
    fi
    APPIMAGE_PATH="$TMPD/$APPIMAGE_NAME"
    say "==> downloading $APPIMAGE_NAME — the optional desktop shell"
    http_get "$(asset_url "$APPIMAGE_NAME")" "$APPIMAGE_PATH" "$(asset_accept)" ||
        die "$E_NETWORK" "download of $APPIMAGE_NAME failed."
}

# verify_appimage — same refusal, one fewer witness.
#
# The archive is verified TWICE (sha256sums.txt, cross-checked against the
# digest GitHub reports). The AppImage can only be verified ONCE, against
# GitHub's digest, because build-release.sh's sha256sums.txt covers only the
# archives that script builds itself — the Tauri bundles come from a
# different release job and are absent from it. That was checked against the
# live v0.1.0 release, not assumed: it lists four archives and no bundles.
#
# One witness is why the shell is optional and the CLI is not. It is still a
# hard failure, and it happens BEFORE anything is installed, so a tampered
# AppImage leaves the machine exactly as it was. If a future release does
# list the AppImage in sha256sums.txt, that line becomes the second opinion
# automatically, with no edit here.
verify_appimage() {
    if [ "$WANT_APP" != 1 ]; then
        return 0
    fi
    _sums_want=$(awk -v f="$APPIMAGE_NAME" \
        '$2 == f || $2 == "*" f { print $1; exit }' "$TMPD/sha256sums.txt")
    if [ -n "$_sums_want" ] && [ "$_sums_want" != "$APPIMAGE_DIGEST" ]; then
        say ""
        say "  sha256sums.txt says  $_sums_want"
        say "  the GitHub API says  $APPIMAGE_DIGEST"
        die "$E_CHECKSUM" "the release's checksum file and GitHub disagree about
$APPIMAGE_NAME. Refusing to install."
    fi

    _got=$(sha256_of "$APPIMAGE_PATH")
    if [ "$APPIMAGE_DIGEST" != "$_got" ]; then
        say ""
        say "  expected  $APPIMAGE_DIGEST   (the digest GitHub reports for this asset)"
        say "  actual    $_got   ($APPIMAGE_NAME)"
        say ""
        say "  Nothing was unpacked and nothing was installed."
        die "$E_CHECKSUM" "sha256 mismatch on $APPIMAGE_NAME."
    fi
    say "==> sha256 verified (desktop shell)"
    info "$_got"
}

# install_desktop_app — the AppImage, plus the shim that makes `dexel open`
# find it. Runs only on already-verified bytes.
install_desktop_app() {
    if [ "$WANT_APP" != 1 ]; then
        return 0
    fi
    _img="$BINDIR/dexel-desktop.AppImage"
    if have install; then
        if ! install -m 0755 "$APPIMAGE_PATH" "$_img"; then
            warn "could not install $_img; skipping the desktop shell."
            return 0
        fi
    else
        if ! cp "$APPIMAGE_PATH" "$_img"; then
            warn "could not install $_img; skipping the desktop shell."
            return 0
        fi
        chmod 0755 "$_img" || true
    fi
    say "==> installed $_img"

    # The shim's path is injected as a single-quoted literal, so nothing in
    # it is re-expanded when the shim runs.
    _shim="$BINDIR/dexel-desktop"
    # Two quoted heredocs with one printf between them, rather than one
    # unquoted heredoc: the shim's whole point is to contain literal `$@`
    # and `$APPIMAGE`, and an unquoted heredoc would expand both HERE, at
    # install time, into nothing.
    if {
        cat <<'SHIM_HEAD'
#!/bin/sh
# dexel-desktop — written by dexel's install.sh. Safe to delete.
#
# `dexel open` looks for an executable literally named `dexel-desktop` (on
# PATH, then in the bin dir) and hands it the runtime URL. The GUI shell
# dexel publishes for Linux is an AppImage — one file, different name — so
# this is the adapter between the two.
set -eu
SHIM_HEAD
        printf 'APPIMAGE=%s\n' "'$_img'"
        cat <<'SHIM'
if [ ! -x "$APPIMAGE" ]; then
    echo "dexel-desktop: $APPIMAGE is missing or not executable." >&2
    echo "dexel-desktop: re-run dexel's installer, or just use the browser." >&2
    exit 127
fi
# An AppImage mounts itself with FUSE. Containers, minimal installs and some
# hardened distros have no /dev/fuse and no fusermount, and there the mount
# fails with a libfuse message instead of starting. The runtime's own
# --appimage-extract-and-run unpacks to a temp dir and runs from there:
# slower to start, works anywhere. `exec` either way, so `dexel open`'s
# detached child is the shell itself and nothing double-launches.
if [ -e /dev/fuse ] && { command -v fusermount3 || command -v fusermount; } >/dev/null 2>&1; then
    exec "$APPIMAGE" "$@"
fi
exec "$APPIMAGE" --appimage-extract-and-run "$@"
SHIM
    } > "$_shim"; then
        chmod 0755 "$_shim" || true
        SHIM_INSTALLED=1
        say "==> installed $_shim"
        info "the name \`dexel open\` looks for, so the window is now the default"
    else
        warn "could not write $_shim; \`dexel open\` will use your browser."
    fi
}

# install_icon — the PNG from the archive into the user's hicolor theme.
#
# No gtk-update-icon-cache: hicolor under $XDG_DATA_HOME has no index.theme
# unless something else put one there, and the tool fails on a directory
# without one. GTK and Qt both read the directory directly, so the cache is
# an optimisation we do not need and cannot honestly claim to have built.
install_icon() {
    # ICON_SRC is the archive's staged dexel.png on the download/local-archive
    # rungs, and desktop/src-tauri/icons/128x128.png on the source rung — the
    # same 128x128 PNG either way (build-release.sh copies that very file into
    # the archive).
    if [ -z "$ICON_SRC" ] || [ ! -f "$ICON_SRC" ]; then
        info "icon      not available for this install; the launcher will use"
        info "          your theme's fallback icon"
        return 0
    fi
    _icon_dir="$DATADIR/icons/hicolor/128x128/apps"
    if ! mkdir -p "$_icon_dir"; then
        warn "could not create $_icon_dir; skipping the icon."
        return 0
    fi
    if have install; then
        if ! install -m 0644 "$ICON_SRC" "$_icon_dir/dexel.png"; then
            warn "could not write $_icon_dir/dexel.png; skipping the icon."
            return 0
        fi
    else
        if ! cp "$ICON_SRC" "$_icon_dir/dexel.png"; then
            warn "could not write $_icon_dir/dexel.png; skipping the icon."
            return 0
        fi
        chmod 0644 "$_icon_dir/dexel.png" || true
    fi
    ICON_INSTALLED=1
    say "==> installed $_icon_dir/dexel.png"
}

# write_desktop_file — the launcher entry.
#
# Exec is `dexel open`, never the AppImage directly: `open` starts the
# runtime if it is not running and then shows the UI, falling back from the
# shell to the browser on its own. Pointing the tile at the AppImage would
# produce a window with nothing behind it whenever the runtime was down.
#
# The path is written to the temp dir and moved into place, so a desktop
# environment watching the directory never sees a half-written entry.
write_desktop_file() {
    _appdir="$DATADIR/applications"
    if ! mkdir -p "$_appdir"; then
        warn "could not create $_appdir; skipping the launcher entry."
        return 0
    fi
    _entry="$_appdir/dexel.desktop"
    _tmp_entry="$TMPD/dexel.desktop"
    {
        say "[Desktop Entry]"
        say "Type=Application"
        say "Version=1.0"
        say "Name=Dexel"
        say "GenericName=Developer Companion"
        say "Comment=A cozy pixel-art companion that works the day alongside you"
        say "Exec=\"$BINDIR/dexel\" open"
        say "TryExec=$BINDIR/dexel"
        say "Icon=dexel"
        say "Terminal=false"
        say "Categories=Game;"
        say "Keywords=dexel;companion;focus;typing;pixel;"
        say "StartupNotify=false"
        say "X-Dexel-Installed-By=install.sh"
    } > "$_tmp_entry"

    if ! mv "$_tmp_entry" "$_entry"; then
        warn "could not write $_entry; dexel will not appear in your app grid."
        return 0
    fi
    chmod 0644 "$_entry" || true
    DESKTOP_ENTRY="$_entry"
    say "==> installed $_entry"

    # Best-effort, and genuinely optional: GNOME, KDE and most other
    # environments notice a new .desktop file on their own. This only
    # refreshes the MIME cache for environments that read it.
    if have update-desktop-database; then
        if ! update-desktop-database "$_appdir" >/dev/null 2>&1; then
            info "update-desktop-database reported a problem; the entry is installed anyway"
        fi
    fi
}

install_desktop_entry() {
    if [ "$NO_DESKTOP" = 1 ]; then
        return 0
    fi
    if [ "$OS" != linux ]; then
        return 0
    fi
    # decide_desktop sets DATADIR on the release rung; the source and local-
    # archive rungs never call it, so establish the same default here. Set
    # either way, it is the one value both use.
    DATADIR="${XDG_DATA_HOME:-$HOME/.local/share}"
    install_icon
    write_desktop_file
}

# ---------------------------------------------------------------------------
# just start
#
# The one thing this installer's older self refused to do, and the one thing
# it was wrong about. Starting the runtime the user just asked for is not the
# consent question — `dexel autostart enable`, which makes dexel start on
# every login forever, is, and that is still untouched and still off.
#
# A failure here does not fail the install: the binary is on disk and works,
# and `dexel open` failing because there is no browser to launch is a
# different problem from a bad install.
# ---------------------------------------------------------------------------

launch() {
    if [ "$NO_START" = 1 ]; then
        return 0
    fi
    say ""
    if [ "$HAS_SESSION" = 1 ]; then
        say "==> starting dexel and opening the game"
        if "$BINDIR/dexel" open; then
            LAUNCHED=open
            return 0
        fi
    else
        say "==> starting the dexel runtime (no desktop session, so nothing is opened)"
        if "$BINDIR/dexel" start; then
            LAUNCHED=start
            return 0
        fi
    fi
    warn "dexel is installed, but starting it did not finish cleanly.
       Check it yourself: $BINDIR/dexel status"
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
    # Unlike this script's older self, the restart is not left to the user:
    # launch() below starts the new build. STOPPED_RUNTIME only changes what
    # the report says about it.
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

    # The launcher icon rides in the same directory as the binary
    # (scripts/build-release.sh stages `dexel.png` beside it), so remember
    # where that was rather than unpacking the archive a second time.
    UNPACKED_DIR=$(dirname "$_src")
    ICON_SRC="$UNPACKED_DIR/dexel.png"

    install_binary_from "$_src"
}

# install_binary_from SRC — copy one already-verified (or freshly built)
# `dexel` binary into BINDIR with mode 0755. The single place the binary
# actually lands, shared by every rung of the ladder.
install_binary_from() {
    _bin_src=$1
    mkdir -p "$BINDIR" || die "$E_TOOL" "could not create $BINDIR."
    if have install; then
        install -m 0755 "$_bin_src" "$BINDIR/dexel" ||
            die "$E_TOOL" "could not install to $BINDIR/dexel."
    else
        if cp "$_bin_src" "$BINDIR/dexel"; then
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
The binary may not match this platform ($OS-$ARCH)."
    # EXPECT_VERSION is the tag on the download rung and the built version on
    # the source rung; it is empty on the local-archive rung (whose version we
    # cannot know ahead of time), and then there is nothing to cross-check.
    if [ -n "$EXPECT_VERSION" ]; then
        case "$INSTALLED_VERSION" in
            *"$EXPECT_VERSION"*) : ;;
            *) warn "\`dexel version\` says \"$INSTALLED_VERSION\" but $EXPECT_VERSION was installed." ;;
        esac
    fi
}

report() {
    say ""
    say "$INSTALLED_VERSION"
    say "installed to $BINDIR/dexel"
    if [ "$SHIM_INSTALLED" = 1 ]; then
        say "desktop shell at $BINDIR/dexel-desktop.AppImage"
    fi
    if [ -n "$DESKTOP_ENTRY" ]; then
        if [ "$ICON_INSTALLED" = 1 ]; then
            say "in your app grid as \"Dexel\", with its own icon"
        else
            say "in your app grid as \"Dexel\""
        fi
    fi
    say ""

    case "$LAUNCHED" in
        open)
            say "dexel is RUNNING and the game is open. That is the install finished,"
            say "not a side effect."
            ;;
        start)
            say "The dexel runtime is RUNNING. Nothing was opened because no desktop"
            say "session was detected — \`dexel open\` shows the game when you have one."
            ;;
        none)
            if [ "$NO_START" = 1 ]; then
                say "Nothing was started (--no-start). Run \`dexel\` when you want it."
            else
                say "Nothing is running. Run \`dexel\` to start it."
            fi
            ;;
    esac
    if [ "$STOPPED_RUNTIME" = 1 ] && [ "$LAUNCHED" = none ]; then
        say ""
        say "The runtime that was running before this upgrade was stopped and not"
        say "restarted. Run \`dexel\` to bring the new build up."
    fi
    say ""
    say "Commands:"
    say "  dexel                    start the runtime and open the game"
    say "  dexel status             is it running? what is it seeing?"
    say "  dexel stop               shut it down (closing the window does not)"
    say "  dexel autostart enable   start dexel at login — NOT enabled, this is opt-in"
    say ""
    if [ "$LAUNCHED" = none ]; then
        say "Autostart is OFF and nothing is running: this installer enabled no"
        say "services and registered no login items. Making dexel come back on every"
        say "login is a separate, explicit \`dexel autostart enable\`."
    else
        say "Autostart is OFF. This installer started dexel because you just asked for"
        say "dexel; it enabled no services and registered no login items. Making it come"
        say "back on every login is a separate, explicit \`dexel autostart enable\`."
    fi
    if [ "$OS" = darwin ]; then
        say ""
        say "On macOS this installed the CLI only. The Dexel.app window is built and"
        say "signed by scripts/mac-release.sh and ships as a .dmg; when a release"
        say "carries one, drag Dexel.app into /Applications and \`dexel open\` finds"
        say "it there on its own (ARCHITECTURE.md Decision 17). Until then \`dexel"
        say "open\` uses your browser, which is a supported front door."
    fi
    say ""
    say "Uninstall — one command, and it is the exact reversal of the above:"
    say ""
    say "  dexel uninstall           stop, disable autostart, remove every file"
    say "                            this installer wrote. Your save STAYS."
    say "  dexel uninstall --purge   ...and delete the save too (asks twice)"
    say ""
    say "It prints every path it removed and every path it kept, and running it"
    say "twice is harmless. Add --yes to skip the prompts in a script."
    say ""
    say "By hand instead — or if you deleted the binary before reading this:"
    say "  dexel stop && dexel autostart disable"
    say "  rm -f $BINDIR/dexel"
    if [ "$SHIM_INSTALLED" = 1 ]; then
        say "  rm -f $BINDIR/dexel-desktop $BINDIR/dexel-desktop.AppImage"
    fi
    if [ -n "$DESKTOP_ENTRY" ]; then
        say "  rm -f $DESKTOP_ENTRY"
    fi
    if [ "$ICON_INSTALLED" = 1 ]; then
        say "  rm -f $DATADIR/icons/hicolor/128x128/apps/dexel.png"
    fi
    say ""
    say "Your keystrokes are counted, never read — counts and durations only,"
    say "enforced by build-failing structural tests. Your data stays in"
    say "$STATEDIR and upgrades never touch it."
    say ""
}

# ---------------------------------------------------------------------------
# rung 1 — build from the source tree this script lives in
#
# The "clone it and run install.sh — it just works" path. No network and no
# published release: `go build` compiles app/, and app/embed.go bakes the
# committed frontend bundle (app/public) and the sprites (app/assets) INTO the
# binary, so one plain build yields the complete game with no npm step. The
# icon comes straight out of the tree.
# ---------------------------------------------------------------------------

# derive_source_version — the same rule scripts/build-release.sh uses:
# `git describe --tags --always --dirty` in a git checkout, and "dev" outside
# one (a source tarball with no .git). Whatever this returns is stamped into
# the binary via -ldflags -X main.version=, so `dexel version` echoes it back.
derive_source_version() {
    if have git && git -C "$REPO_DIR" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
        _v=$(git -C "$REPO_DIR" describe --tags --always --dirty 2>/dev/null || true)
        if [ -n "$_v" ]; then
            printf '%s' "$_v"
            return 0
        fi
    fi
    printf '%s' "dev"
}

do_source_install() {
    have go || die "$E_TOOL" "building from source needs \`go\` on PATH."
    have mktemp || die "$E_TOOL" "need mktemp, which is not on PATH."
    make_tempdir

    VERSION=$(derive_source_version)
    EXPECT_VERSION="$VERSION"
    say "==> dexel installer — building from source ($OS-$ARCH)"
    info "source    $REPO_DIR"
    info "version   $VERSION"
    info "toolchain $(command -v go)"

    # CGO: darwin's activity provider is cgo (app/internal/activity/
    # provider_darwin.go), so a mac build needs CGO_ENABLED=1 and a C compiler,
    # exactly like scripts/build-release.sh's darwin row. Everywhere else the
    # tree is pure Go and builds with CGO off, needing no C toolchain — which
    # is what keeps the common "clone and go" path dependency-free.
    _cgo=0
    if [ "$OS" = darwin ]; then _cgo=1; fi

    SRC_BIN="$TMPD/dexel"
    say "==> compiling dexel — no network, no release, no npm needed"
    info "the frontend bundle (app/public/js/dexel.js) and sprites (app/assets)"
    info "are committed and embedded by go build (app/embed.go)"
    if ! (
        cd "$REPO_DIR/app" &&
        CGO_ENABLED="$_cgo" go build -trimpath \
            -ldflags "-s -w -X main.version=$VERSION" -o "$SRC_BIN" .
    ); then
        die "$E_TOOL" "\`go build\` failed — see the compiler output above."
    fi
    [ -f "$SRC_BIN" ] ||
        die "$E_TOOL" "\`go build\` reported success but produced no binary."
    say "==> built $SRC_BIN"

    # The launcher icon, straight from the tree (build-release.sh copies this
    # exact file into the release archive as dexel.png).
    ICON_SRC="$REPO_DIR/desktop/src-tauri/icons/128x128.png"

    if [ "$DRY_RUN" = 1 ]; then
        say ""
        say "--dry-run: built dexel from source to a temp dir and verified it runs:"
        info "$("$SRC_BIN" version 2>/dev/null || echo '(dexel version produced nothing)')"
        say "Nothing was installed."
        return 0
    fi

    stop_running_runtime
    install_binary_from "$SRC_BIN"
    finish_common
}

# ---------------------------------------------------------------------------
# rung 2 — install from a local archive (DEXEL_ARCHIVE), fully offline
#
# Verified against a sibling <archive>.sha256 when one exists; otherwise
# installed with a printed "unverified local file" notice. (The stronger
# "verify a local archive against the LIVE release's sha256sums.txt" flow is
# the --from-release path's DEXEL_ARCHIVE branch, in download() — that one
# needs the network, so it is a separate, opted-into thing.)
# ---------------------------------------------------------------------------

verify_local_archive() {
    _f=$1
    _sum="$_f.sha256"
    if [ -f "$_sum" ]; then
        if have sha256sum; then
            SHA_TOOL=sha256sum
        elif have shasum; then
            SHA_TOOL=shasum
        else
            die "$E_TOOL" "found $(basename "$_sum") beside the archive but no sha256sum or
shasum to check it with."
        fi
        _want=$(awk 'NF { print $1; exit }' "$_sum")
        [ -n "$_want" ] ||
            die "$E_CHECKSUM" "$(basename "$_sum") is empty; cannot verify $ARCHIVE_NAME."
        _got=$(sha256_of "$_f")
        if [ "$_want" != "$_got" ]; then
            say ""
            say "  expected  $_want   (from $(basename "$_sum"))"
            say "  actual    $_got   ($ARCHIVE_NAME)"
            say ""
            say "  Nothing was unpacked and nothing was installed."
            die "$E_CHECKSUM" "sha256 mismatch on $ARCHIVE_NAME."
        fi
        say "==> sha256 verified against $(basename "$_sum")"
        info "$_got"
    else
        warn "no $(basename "$_f").sha256 beside the archive — installing it UNVERIFIED.
       This is a local file you pointed at, not a download, which is the only
       reason it is allowed; put a matching .sha256 next to it to have this
       checked, or use --from-release to verify against the live release."
    fi
}

do_archive_install() {
    have tar || die "$E_TOOL" "need tar to unpack an archive, which is not on PATH."
    have mktemp || die "$E_TOOL" "need mktemp, which is not on PATH."
    _arch="${DEXEL_ARCHIVE:-}"
    [ -n "$_arch" ] || die "$E_USAGE" "the local-archive rung needs DEXEL_ARCHIVE set to a file."
    [ -f "$_arch" ] || die "$E_USAGE" "DEXEL_ARCHIVE=$_arch is not a file."
    make_tempdir

    ARCHIVE_NAME=$(basename "$_arch")
    ARCHIVE_PATH="$_arch"          # unpack it where it lies; no copy needed
    EXPECT_VERSION=""              # a local archive's version is not known up front
    say "==> dexel installer — installing from local archive ($OS-$ARCH)"
    info "archive   $_arch"
    verify_local_archive "$_arch"

    if [ "$DRY_RUN" = 1 ]; then
        mkdir -p "$TMPD/x"
        tar -xzf "$ARCHIVE_PATH" -C "$TMPD/x" ||
            die "$E_TOOL" "could not unpack $ARCHIVE_NAME."
        _found=$(find "$TMPD/x" -type f -name dexel 2>/dev/null | head -n 1)
        [ -n "$_found" ] ||
            die "$E_TOOL" "no \`dexel\` binary inside $ARCHIVE_NAME."
        say ""
        say "--dry-run: verified and unpacked $ARCHIVE_NAME. Nothing was installed."
        return 0
    fi

    stop_running_runtime
    install_binary                # unpacks ARCHIVE_PATH, sets UNPACKED_DIR + ICON_SRC
    finish_common
}

# ---------------------------------------------------------------------------
# rung 3 — download a release from GitHub (the original path)
# ---------------------------------------------------------------------------

do_release_install() {
    check_tools
    resolve_token
    make_tempdir
    configure_http
    say "==> dexel installer — $OS-$ARCH"
    resolve_release                        # 3
    require_platform_asset                 # 3b: macOS/absent-build honesty
    EXPECT_VERSION="$TAG"
    decide_desktop                         # 3c: what desktop integration to do
    download                               # 4, 5
    verify                                 # 6
    download_appimage                      # 4, 5 (optional shell)
    verify_appimage                        # 6   (optional shell)

    # Everything above this line only reads and verifies. --dry-run stops
    # here, which is why it is a complete test of the network, the release
    # and every checksum without touching the machine.
    if [ "$DRY_RUN" = 1 ]; then
        say ""
        say "--dry-run: resolved, downloaded and verified $ARCHIVE_NAME."
        if [ "$WANT_APP" = 1 ]; then
            say "            ...and $APPIMAGE_NAME."
        fi
        say "Nothing was installed."
        return 0
    fi

    stop_running_runtime                   # 8 (idempotent re-run)
    install_binary                         # 7
    install_desktop_app                    # 7b: the AppImage + its shim
    install_desktop_entry                  # 7c: the icon + the launcher
    make_state_dirs                        # 8
    verify_installed                       # 10
    path_advice                            # 9
    launch                                 # 11: just start
    report                                 # 10
}

# finish_common — the shared install tail for the source and local-archive
# rungs (the release rung inlines the same steps plus its AppImage shell,
# which only a release carries). By this point the binary is already in place.
finish_common() {
    install_desktop_entry                  # 7c: the icon + the launcher (Linux)
    make_state_dirs                        # 8
    verify_installed                       # 10
    path_advice                            # 9
    launch                                 # 11: just start
    report                                 # 10
}

# ---------------------------------------------------------------------------
# main — the only top-level statement in this file is its call, on the last
# line, so a truncated download cannot run a partial installer.
# ---------------------------------------------------------------------------

main() {
    while [ $# -gt 0 ]; do
        case "$1" in
            --from-source) FROM_SOURCE=1 ;;
            --from-release) FROM_RELEASE=1 ;;
            --dry-run) DRY_RUN=1 ;;
            --no-start) NO_START=1 ;;
            --no-desktop) NO_DESKTOP=1 ;;
            --no-app) NO_APP=1 ;;
            --app) FORCE_APP=1 ;;
            -h|--help) usage; return 0 ;;
            *) printf 'dexel install: unknown option %s\n' "$1" >&2
               printf 'try --help\n' >&2
               return "$E_USAGE" ;;
        esac
        shift
    done

    BINDIR="${DEXEL_INSTALL_DIR:-$HOME/.local/bin}"

    # Environment equivalents of the flags, for a `curl | sh` that cannot
    # pass arguments. A flag already set stays set.
    # `[ x ] && VAR=1` would be shorter and would also EXIT under `set -e`
    # the moment the test was false, because a failed AND-list is a failed
    # command. if/fi is the only spelling that is correct here.
    if [ "${DEXEL_FROM_SOURCE:-}" = 1 ]; then FROM_SOURCE=1; fi
    if [ "${DEXEL_FROM_RELEASE:-}" = 1 ]; then FROM_RELEASE=1; fi
    if [ "${DEXEL_NO_START:-}" = 1 ]; then NO_START=1; fi
    if [ "${DEXEL_NO_DESKTOP:-}" = 1 ]; then NO_DESKTOP=1; fi
    if [ "${DEXEL_NO_APP:-}" = 1 ]; then NO_APP=1; fi
    if [ "${DEXEL_APP:-}" = 1 ]; then FORCE_APP=1; fi

    if [ "$FROM_SOURCE" = 1 ] && [ "$FROM_RELEASE" = 1 ]; then
        die "$E_USAGE" "--from-source and --from-release are mutually exclusive; pick one."
    fi

    detect_platform                        # 1
    if [ "$OS" = windows ]; then
        # Git Bash / MSYS2 / Cygwin: hand off to install.ps1 and stop here,
        # BEFORE the source-selection ladder — Windows installs via install.ps1,
        # which has its own acquisition logic; the source/archive rungs below
        # are Linux/macOS shell territory. A successful delegation falls through
        # to `return 0`; a failed one already ended the script via set -eu
        # inside delegate_windows.
        delegate_windows
        return 0
    fi

    detect_session
    detect_repo                            # am I inside the dexel source tree?
    choose_source                          # -> SOURCE = source | archive | release

    # The ladder: the highest-confidence source that can actually proceed.
    # Each do_* function verifies before it writes, honours --dry-run, and ends
    # by installing the binary, wiring desktop integration, and starting dexel.
    case "$SOURCE" in
        source)  do_source_install ;;      # rung 1: build the tree we live in
        archive) do_archive_install ;;     # rung 2: a local .tar.gz, offline
        release) do_release_install ;;     # rung 3: download from GitHub
        *)       die "$E_USAGE" "internal error: no install source was chosen." ;;
    esac
}

main "$@"
