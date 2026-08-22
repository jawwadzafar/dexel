#!/usr/bin/env bash
#
# dexel — run the Go test suite under the race detector, on a host where
# `cc` is not `gcc`.
#
# SF-3 (docs/plan/REVIEW-2026-08-22.md): every -race step in CI guards
# with `command -v cc`, and this repo's only runner DOES have a `cc` — a
# zig shim at ~/.local/bin/cc — so the guard passes and the toolchain
# install is skipped. But cgo does not look for `cc`: it looks for
# $CC, defaulting to **gcc** on Linux and clang on darwin. With no gcc on
# PATH the whole suite then fails at once with
#
#     cgo: C compiler "gcc" not found: exec: "gcc": executable
#          file not found in $PATH
#
# and `sudo -n apt-get install` cannot rescue it on this box either
# ("interactive authentication is required"). The fix is one environment
# variable, so it lives here, in a script both a human and a workflow can
# call, instead of being re-derived in three YAML files:
#
#     scripts/test-race.sh                 # the whole module
#     scripts/test-race.sh ./internal/...  # a subset
#     CC=/path/to/clang scripts/test-race.sh   # explicit override wins
#
# Note for whoever owns .github/workflows/{build,release}.yml: the
# `go test -race` steps there should either call this script or export
# `CC` themselves. Probing for `cc` and then invoking a tool that wants
# `gcc` is the actual bug, and it is in the YAML, not in Go.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GO="${GO:-go}"

die() { printf 'test-race: %s\n' "$*" >&2; exit 1; }

# An explicit CC always wins — this script's job is to find one when the
# caller has not, never to second-guess a caller who has.
if [ -z "${CC:-}" ]; then
  for cand in gcc clang cc; do
    if command -v "$cand" >/dev/null 2>&1; then
      CC="$cand"
      break
    fi
  done
fi
[ -n "${CC:-}" ] || die "no C compiler found (looked for gcc, clang, cc). -race needs cgo, and cgo needs a real compiler+linker."

# Prove the compiler actually works before handing it to a full test run:
# a broken shim produces the same "cannot find gcc"-shaped wall of
# failures across every package, several minutes later.
if ! printf 'int main(void){return 0;}\n' | "$CC" -x c - -o /dev/null 2>/dev/null; then
  die "CC=$CC cannot compile a trivial C program — fix or override CC before running -race."
fi

printf 'test-race: CC=%s (%s)\n' "$CC" "$(command -v "$CC")" >&2
export CC
export CGO_ENABLED=1

targets=("$@")
[ "${#targets[@]}" -gt 0 ] || targets=("./...")

cd "$REPO_ROOT/app"
exec "$GO" test -race "${targets[@]}"
