# Cutting the macOS half of a dexel release

**Audience:** the owner, sitting at the Mac.
**Script:** `scripts/mac-release.sh` (run it; don't retype what it does).
**Status:** written and verified on Linux; **never yet run on a Mac.** The
"Verified where" table at the bottom says exactly which parts of it have been
executed and which are waiting for their first real run.

---

## Why this document exists

`.github/workflows/release.yml` builds and publishes linux/amd64,
linux/arm64, windows/amd64 and windows/arm64 from one Linux self-hosted
runner, then attaches them plus a `sha256sums.txt` to the GitHub Release for
the tag. It **cannot** build darwin: `app/internal/activity/provider_darwin.go`
is cgo — Cocoa/CoreGraphics through Objective-C — and
`activity.NewDarwinProvider` has no non-cgo definition, so a `CGO_ENABLED=0`
darwin build does not degrade into a blind provider, it fails to link. The
Tauri `.dmg` is worse: only a Mac can make one at all.

So the workflow's `release-macos` job is a deliberate, warning-printing no-op,
and `scripts/mac-release.sh` is the hand-run replacement for it. It produces
the three things the Linux runner cannot and attaches them to the release that
already exists:

| artifact | what it is |
|---|---|
| `dexel-<TAG>-darwin-arm64.tar.gz` | the CLI/runtime archive, in `scripts/build-release.sh`'s exact layout |
| `dexel-<TAG>-darwin-arm64.dmg` (or `…-unsigned.dmg`) | the Tauri desktop bundle |
| `sha256sums.txt` | the release's existing checksum file with the darwin lines merged in |

`install.sh` needs **no change** for any of this. It resolves the release
through the GitHub API and looks for `dexel-<TAG>-<os>-<arch>.tar.gz`; the day
this script uploads the darwin archive, its "macOS builds are not published
yet" branch stops firing on its own.

---

## 1. Prerequisites, with one-line installs

The script checks every one of these before it builds anything and prints the
exact fix for whichever is missing. This table is here so you can do them all
up front instead of one error at a time.

| # | Requirement | Check | Install |
|---|---|---|---|
| 1 | macOS on Apple Silicon | `uname -s; uname -m` → `Darwin` / `arm64` | — (v1 is arm64-only; see § 8) |
| 2 | Xcode Command Line Tools | `xcode-select -p` | `xcode-select --install` |
| 3 | Go ≥ the version in `app/go.mod` (1.27) | `go env GOVERSION` | `brew install go` |
| 4 | Rust toolchain | `cargo --version` | `curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs \| sh` then `source "$HOME/.cargo/env"` |
| 5 | tauri-cli v2 | `cargo tauri --version` | `cargo install tauri-cli --locked` (or pin: `--version '^2.0.0' --locked`) |
| 6 | GitHub CLI, authenticated | `gh auth status` | `brew install gh && gh auth login` |
| 7 | `shasum` | `shasum -a 256 /dev/null` | ships with macOS |

Notes that have already cost time on this project:

* **`cargo` on a fresh shell.** rustup installs to `~/.cargo/bin`, which a
  non-login shell often does not have on `PATH`. `source "$HOME/.cargo/env"`
  is the whole fix, and the script's error message says so.
* **A Rosetta terminal reports `x86_64`.** If check 1 fails on an Apple
  Silicon Mac, you are in a Rosetta shell — use a native Terminal, or
  `arch -arm64 bash scripts/mac-release.sh`.
* **`gh` needs the `repo` scope** to upload release assets. If it can
  authenticate but not see the repo, the script tells you to run
  `gh auth switch` / `gh auth refresh -h github.com -s repo`.
* **No `PKG_CONFIG=` or `GDK_BACKEND=x11` here.** Those are the Linux
  webkit2gtk workarounds from `docs/plan/TAURI-FIRST-BUILD.md`. macOS uses the
  system WKWebView and needs neither.

---

## 2. The three tiers

The tier is chosen by *which environment variables are set* — there is no flag
for it. Set nothing and you get tier 1.

| tier | env | result | artifact name |
|---|---|---|---|
| **1 — unsigned** | none | builds, uploads, warns | `dexel-<TAG>-darwin-arm64-unsigned.dmg` |
| **2 — signed** | `APPLE_SIGNING_IDENTITY` | code-signed, **not** notarized | `dexel-<TAG>-darwin-arm64.dmg` |
| **3 — signed + notarized** | `APPLE_SIGNING_IDENTITY` + `APPLE_ID` + `APPLE_PASSWORD` + `APPLE_TEAM_ID` | signed, notarized, ticket stapled, both verified | `dexel-<TAG>-darwin-arm64.dmg` |

**Tier 1 is a real, shippable tier** — it is what you have today, and it is
honest as long as the caveat travels with the download. The `-unsigned` suffix
in the filename is deliberate: nobody can mistake it for a signed build, and
the script prints the exact right-click-to-open paragraph for the release
notes at the end of the run.

**Tier 2 is the least useful tier.** A signed but un-notarized `.dmg` still
trips Gatekeeper on someone else's Mac, so it buys you nothing a user can see.
Treat it as a step on the way to tier 3, not a destination.

**Tier 3 is the one that makes the caveat go away.** Notarization is a network
round-trip to Apple during bundling and adds minutes to the build.

### Switching tiers is safe to re-run

Because the `.dmg` changes *name* between tiers, re-running with signing turned
on would normally leave the old unsigned asset and its checksum line sitting on
the release forever. The script handles this: whichever `.dmg` name this run
did **not** produce is treated as superseded — its line is dropped from the
merged `sha256sums.txt`, and the asset itself is deleted from the release
after the new upload succeeds (in that order, so a failed upload never leaves
the release with no disk image at all).

---

## 3. Getting a Developer ID certificate (tier 2 and 3)

Do this once. It is an Apple purchase and an Apple web flow; no code is
involved.

1. **Enrol in the Apple Developer Program** — <https://developer.apple.com/programs/>
   $99/year, individual or organisation. Notarization is not available on a
   free account.
2. **Create the certificate.** Xcode is the least painful route:
   *Xcode → Settings → Accounts → (your Apple ID) → Manage Certificates → `+` →
   **Developer ID Application***. It lands in your login keychain.
   (Web route: developer.apple.com → Certificates, Identifiers & Profiles →
   Certificates → `+` → Developer ID Application → upload a CSR from Keychain
   Access → download the `.cer` → double-click to import.)
3. **Read back its exact name:**
   ```bash
   security find-identity -v -p codesigning
   ```
   Copy the quoted string verbatim — including the team id in parentheses:
   ```
   Developer ID Application: Jawwad Zafar (ABCDE12345)
   ```
   That string is `APPLE_SIGNING_IDENTITY`. The script checks it against the
   keychain **before** building, so a typo costs seconds instead of a full
   build.
4. **Note your Team ID** — the ten characters in those parentheses, also shown
   at developer.apple.com → Membership. That is `APPLE_TEAM_ID`.
5. **Create an app-specific password** (tier 3 only) — this is what
   `APPLE_PASSWORD` must be, and it is **never** your Apple ID password:
   1. sign in at <https://appleid.apple.com>;
   2. *Sign-In and Security → App-Specific Passwords → Generate…*;
   3. name it something like `dexel-notarize`;
   4. copy the `xxxx-xxxx-xxxx-xxxx` value — it is shown once.

   `APPLE_ID` is the email address of that Apple ID.

**Keep these out of the repo and out of your shell history.** Put them in a
file only you can read and source it for the release, e.g.:

```bash
# ~/.dexel-release-env   (chmod 600, NOT in the repo)
export APPLE_SIGNING_IDENTITY="Developer ID Application: Jawwad Zafar (ABCDE12345)"
export APPLE_ID="you@example.com"
export APPLE_TEAM_ID="ABCDE12345"
export APPLE_PASSWORD="abcd-efgh-ijkl-mnop"
```

---

## 4. The run

The tag must already have a published release — this script *adds* darwin
assets to `release.yml`'s output, it never creates a release. So the order is:
push the tag, let CI publish linux/windows, then come to the Mac.

```bash
cd ~/where/dexel/lives
git fetch --tags
git checkout v0.2.0          # HEAD must BE the tag; the script enforces this
git pull                     # (on a branch; a detached tag checkout needs no pull)

# tier 1 — unsigned
bash scripts/mac-release.sh v0.2.0

# tier 3 — signed + notarized
source ~/.dexel-release-env
bash scripts/mac-release.sh v0.2.0

# see everything it would do, upload nothing
bash scripts/mac-release.sh v0.2.0 --dry-run
```

With no `TAG` argument it uses the latest published release tag from
`jawwadzafar/dexel`. `--help` prints the full option list.

### What it does, in order

1. **Prerequisites** — every check in § 1, each fatal with its own fix.
2. **Tag** — resolves `TAG`, confirms the release exists, and refuses to build
   if `HEAD` is not that tag or the tree is dirty under `app/`, `desktop/` or
   `scripts/`. That refusal is the point: an archive named `v0.2.0` whose
   contents are not `v0.2.0` produces a published checksum for source that
   exists nowhere in git. Override deliberately with
   `DEXEL_ALLOW_TAG_MISMATCH=1`.
3. **Signing tier** — decided *before* building, because tauri-bundler reads
   those variables at bundle time. Verifies the identity is really in the
   keychain.
4. **CLI binary** — `CGO_ENABLED=1 go build -trimpath -ldflags "-s -w -X
   main.version=$TAG"` from `app/`, byte-for-byte the same recipe
   `build-release.sh` uses. Then it runs `./dexel version` and **asserts** the
   output starts with `dexel <TAG> (` — if the ldflag silently didn't take,
   the run stops there.
5. **Tests** — `go test -race ./...` from `app/`, with `CGO_ENABLED=1`. Note
   this calls `go test` directly rather than `scripts/test-race.sh`: that
   script exists to solve a *Linux* problem (the runner has no `gcc`, so it
   probes and exports `CC`). On macOS cgo's default compiler is the clang the
   CLT check already proved works, so there is nothing to probe.
6. **Package** — stages `dexel` + `README.md` + `LICENSE` +
   `THIRD-PARTY-LICENSES.md` into `dist/dexel-<TAG>-darwin-arm64/` and tars it.
   No `public/` or `assets/` directory: since EMBED-1 both are `go:embed`ed
   into the binary. The script pre-flights `app/public/index.html`,
   `app/public/js/dexel.js` and `app/assets/room_back.png` for exactly that
   reason — whatever is missing from those trees at build time is missing from
   the binary permanently and invisibly.
7. **Sidecar** — `scripts/build-sidecar.sh`, with `DEXEL_RELEASE_VERSION=$TAG`
   so the runtime *inside* the app bundle reports the tag too. It asserts the
   result is at `desktop/src-tauri/binaries/Dexel-aarch64-apple-darwin` (the
   exact name `bundle.externalBin` resolves) and that it reports `$TAG`.
8. **Desktop bundle** — `cargo tauri build` in `desktop/src-tauri`, then it
   finds the `.app` and `.dmg` by glob (their names come from `productName` +
   `tauri.conf.json`'s own `version`, which are not this script's business) and
   copies the `.dmg` into `dist/` under the release naming convention.
9. **Signature verification** — `codesign --verify --deep --strict` on the
   `.app`, `spctl -a -t open --context context:primary-signature` and
   `xcrun stapler validate` on the `.dmg`. In tier 3 a failure of either is
   **fatal**; in tier 2 they are warnings, because a signed-not-notarized
   build is *expected* to be rejected. Tier 1 confirms the bundle really is
   unsigned rather than just assuming it.
10. **Checksums** — see § 5.
11. **Upload** — artifacts first, then the merged `sha256sums.txt`, then the
    superseded `.dmg` is deleted. Everything uses `--clobber`, so a second run
    replaces rather than duplicates. Afterwards it re-downloads
    `sha256sums.txt` and diffs it against the local merge; a mismatch is
    fatal, because a release nobody verified after upload is a release nobody
    verified.
12. **Summary** — what was built, signed and uploaded, the two commands a Mac
    user now runs, and (in tier 1) the Gatekeeper caveat as ready-to-paste
    prose for the release notes.

`--dry-run` does 1–10 and prints exactly the `gh` commands step 11 would have
run.

---

## 5. How `sha256sums.txt` is merged

This is the step that goes wrong quietly, so it is worth understanding.

The release already has a `sha256sums.txt` covering the four linux/windows
archives, written by CI. The darwin lines must be **added** to it without
losing those, and a second run must produce the same file rather than one with
the darwin lines twice. So:

1. download the release's current `sha256sums.txt` (missing → start empty,
   with a warning);
2. compute the darwin lines with `shasum -a 256`, in the same
   `<hex>␠␠<name>` format `sha256sum -c` reads back and `install.sh`'s
   two-field `awk` matches;
3. drop every existing line whose **filename** matches a line being added, or
   matches the other tier's `.dmg` name;
4. keep all remaining lines **in their original order**, then append the
   darwin lines;
5. self-check: each darwin filename appears exactly once, and no line that was
   published has silently vanished (other than a deliberately superseded one).
   A failure here stops the run before anything is uploaded.

Consequences worth knowing:

* Re-running the script is safe and produces an identical file.
* Rebuilding the same tag replaces the darwin hashes in place.
* Turning signing on removes the unsigned `.dmg`'s line **and** its asset.
* A `<hash> *name` line (what `sha256sum -b` writes) is recognised as the same
  file as `<hash>  name`, so an oddly-generated file from the past cannot
  produce a duplicate.

The GitHub Release is the only thing this script touches. The R2 bucket layout
in `docs/production-runtime/RELEASE_PIPELINE.md` §3 (where the same file is
served as `checksums.txt`) is a separate, later channel.

---

## 6. If something goes wrong

| Symptom | Cause and fix |
|---|---|
| `no .dmg found under …/bundle/dmg/` (the `.app` built fine) | tauri-bundler drives Finder over AppleScript to lay out the disk image, which needs a real logged-in GUI session. Run it from a Terminal on the Mac's own desktop, not over ssh. Also check nothing is still mounted: `hdiutil info \| grep -i dexel`. |
| `cargo tauri: command not found` | `cargo install tauri-cli --locked` |
| Notarization hangs for minutes | Normal. It is an upload plus Apple-side scan. |
| Notarization *fails* | Read the `cargo tauri build` output — Apple returns a JSON log URL. Almost always a hardened-runtime or entitlement problem, not this script. |
| `stapler could not validate a ticket` | The submission was accepted but the ticket never landed. Re-run; if it repeats, the notarization itself failed earlier in the log. |
| `HEAD is not at tag …` | `git fetch --tags && git checkout <tag>`, or `DEXEL_ALLOW_TAG_MISMATCH=1` if you genuinely mean it. |
| `there is no release '<tag>'` | Push the tag first and let `release.yml` publish the linux/windows half. |
| `the binary reports 'dexel dev (…)'` | The `-X main.version` ldflag did not take. Do not ship it; the script already refused. |

---

## 7. When CI takes over

Nothing in this document is permanent. `release.yml` already has the
`release-macos` job written and gated; registering this Mac as a self-hosted
runner with the label **`mac`** is what activates it:

1. GitHub → repo → Settings → Actions → Runners → New self-hosted runner →
   macOS/arm64, and give it the label `mac`.
2. In `.github/workflows/release.yml`, change that job's
   `runs-on: [self-hosted, darkmirror]` to `[self-hosted, mac]` and delete the
   "Skip: no macOS runner registered" step.
3. Have the job run this same script — it is written to be callable from CI
   unchanged: `bash scripts/mac-release.sh "$GITHUB_REF_NAME"`, with the Apple
   variables supplied as repository secrets and `GH_TOKEN` for `gh`.

That last point is the reason the signing tiers are environment-driven rather
than flag-driven: the CI job and the hand run differ only in which secrets
exist. See also `docs/production-runtime/RELEASE_PIPELINE.md`'s owner checklist entry
("Register the owner's Mac as a self-hosted runner with label `mac`").

---

## 8. Adding an Intel build later

v1 is **arm64 only**, on purpose: it is the only Mac this project has, and an
unbuildable target is worse than an absent one (`install.sh` says "no build
for this platform in this release", which is true and actionable).

When an Intel build is wanted, there are two honest routes:

* **A separate `darwin-amd64` archive.** Add `x86_64-apple-darwin` /
  `GOARCH=amd64` to a second pass of the same script. The Go side
  cross-compiles on an arm64 Mac (`GOARCH=amd64 CGO_ENABLED=1` with the
  Apple SDK's clang, which targets both), producing
  `dexel-<TAG>-darwin-amd64.tar.gz`; `install.sh` picks it up by name with no
  edit. `scripts/build-sidecar.sh` already lists the `x86_64-apple-darwin`
  triple, so the sidecar side is a one-word change.
* **A universal binary.** Build both arches and `lipo -create -output dexel
  dexel-arm64 dexel-amd64`, then ship one archive per the naming you choose
  (`…-darwin-universal.tar.gz` would need a matching `install.sh` change, so
  the two-archive route above is the cheaper one). For the `.dmg`, Tauri
  supports `cargo tauri build --target universal-apple-darwin`, which needs
  both Rust targets (`rustup target add x86_64-apple-darwin`) and both sidecar
  binaries present.

Either way, test it on an actual Intel Mac (or under Rosetta) before
publishing. "It compiled" is not the same claim as "it runs".

---

## Verified where

Honesty about this document's own provenance, because the script was authored
on Linux and the whole macOS path is by definition unexecuted here.

| Part | Status |
|---|---|
| `bash -n` and `shellcheck` (default, and `-o all` at severity ≥ warning) on `scripts/mac-release.sh` | **clean, verified on Linux** |
| Archive layout / filename / `tar` invocation vs `scripts/build-release.sh` | **verified**: the script's own packaging lines were extracted and run against the same staged binary; tar member list, order, modes and extracted trees are byte-identical to `build-release.sh`'s output |
| `go build` flags (`-trimpath`, `-s -w`, `-X main.version`) | **verified** identical to `build-release.sh`'s, and executed — the `dexel version` assertion was proved to pass and to fail correctly |
| `sha256sums.txt` merge (append, replace, idempotent re-run, tier switch both ways, empty input, `*name` form, `sha256sum -c` readback) | **verified on Linux** against the real published `v0.1.0` checksum file |
| Go version comparison, argument parsing, tag resolution and defaulting, the provenance gate, every failure exit | **verified on Linux** (15-case version table; each failure path executed) |
| `gh` invocation syntax (`release view/download/upload --clobber/delete-asset --yes`) | **verified** against `gh --help` and, for the read-only calls, against the live repo. No asset was uploaded, replaced or deleted. |
| Whole-script control flow, all three tiers, `--dry-run` and the upload path | **verified by simulation**: the mac-only tools (`sw_vers`, `xcode-select`, `clang`, `security`, `codesign`, `spctl`, `xcrun stapler`, `cargo tauri`) were stubbed and `gh`'s mutating calls were print-only |
| The actual darwin/arm64 cgo build, `cargo tauri build`, the real `.dmg`, real code-signing, real notarization and stapling, and a real `gh release upload` | **NOT verified — mac-only.** First run on the Mac is the first real test. Use `--dry-run` for it. |

The first person to run this on the Mac should update this table and note
anything that differed.
