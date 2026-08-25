# Dexel — release pipeline, hosting and the install script

Companion to `ARCHITECTURE.md`. How a git tag becomes
`curl -fsSL https://get.dexel.jwdlab.com/install.sh | sh`.

---

## 1. What exists today (do not rebuild it)

`.github/workflows/release.yml`, triggered by `push: tags: ["v*"]` or
`workflow_dispatch`, on `[self-hosted, darkmirror]`:

1. wipes the workspace, checks out with `fetch-depth: 0` (needs tags for
   `git describe`);
2. `npm ci` → `npm run typecheck` → `npm run build` in `app/frontend`, then
   **asserts `app/public/js/dexel.js` and its map are byte-identical to a fresh
   build** — post-EMBED-1 this is the last chance to catch a stale bundle,
   because it gets `go:embed`ed into every binary;
3. `go vet` and `go test -race` with `CGO_ENABLED=1`;
4. resolves the version (tag name, else `git describe --tags --always --dirty`);
5. `bash scripts/build-release.sh` → `dist/` with one archive per target plus
   `dist/sha256sums.txt`;
6. `softprops/action-gh-release@v2` publishes the archives + checksums.

`scripts/build-release.sh` builds linux/{amd64,arm64} and windows/{amd64,arm64}
at `CGO_ENABLED=0`, adds `darwin/arm64` **only on a darwin host**, stages
`dexel[.exe]` + `README.md` + `LICENSE` + `NOTICE` +
`THIRD-PARTY-LICENSES.md` per target, tars/zips it, and emits one
`sha256sums.txt` covering every archive from that run. It also pre-flights the
`go:embed` inputs (`app/public/index.html`, `app/public/js/dexel.js`,
`app/assets/room_back.png`) and fails loudly rather than shipping a binary with
an incomplete embedded game.

The `release-macos` job is a deliberate no-op with warnings until a `mac`-labelled
runner is registered.

**This is a good pipeline.** Nothing below replaces it; everything below is
additive.

---

## 2. What has to change

| # | Change | Where |
|---|---|---|
| C1 | stamp a real semver into the binary | `scripts/build-release.sh`: add `-ldflags "-X main.version=$VERSION"` to the `go build` |
| C2 | publish to R2 as well as GitHub Releases | new `scripts/publish-r2.sh` + a new `publish` job |
| C3 | generate `latest/manifest.json` **from the bucket** | new `manifest` job, runs after every publish |
| C4 | refuse to overwrite a published object | inside `publish`, before upload |
| C5 | serve `install.sh` from `get.dexel.jwdlab.com` | new `scripts/install.sh` + a `deploy-installer` workflow |
| C6 | let a *later* macOS build join an *existing* version | falls out of C3 — the manifest is derived, not authored |

---

## 3. Bucket layout

Two Cloudflare R2 buckets, because a custom domain maps to a bucket **root**,
not to a prefix.

### `dexel-downloads` → `downloads.dexel.jwdlab.com`

```
releases/
  v1.4.0/
    dexel-v1.4.0-linux-amd64.tar.gz
    dexel-v1.4.0-linux-arm64.tar.gz
    dexel-v1.4.0-windows-amd64.zip
    dexel-v1.4.0-windows-arm64.zip
    dexel-v1.4.0-darwin-arm64.tar.gz      # may arrive LATER than the others
    checksums.txt                          # sha256sum format, one line per file
  v1.3.0/
    ...
latest/
  manifest.json                            # the only mutable release document
  VERSION                                  # plain text, e.g. "v1.4.0\n"
```

Filenames keep the version (ARCHITECTURE FORK B) and match
`scripts/build-release.sh`'s existing naming exactly, so the script needs no
rename.

**`checksums.txt` is `sha256sums.txt` renamed on upload** — same
`<hash>␠␠<filename>` format `sha256sum` produces, so `sha256sum -c` and a
two-field `awk` both work. Kept per-version (immutable) rather than global.

### `dexel-get` → `get.dexel.jwdlab.com`

```
install.sh          # also served at /  (index document)
install.ps1         # phase 2, Windows
```

### `dexel.jwdlab.com`

Landing page. Cloudflare Pages or a third bucket with an `index.html`;
out of scope here beyond "it is a different origin and it links to
`get.` and `downloads.`".

### Cache headers

| Object | `Cache-Control` |
|---|---|
| `releases/**` (immutable by rule) | `public, max-age=31536000, immutable` |
| `latest/manifest.json`, `latest/VERSION` | `public, max-age=60` |
| `install.sh` | `public, max-age=300` |

A one-minute TTL on `latest/` is the difference between "I just released and the
installer still fetches the old version" and a support thread.

---

## 4. Upload tooling and secrets

**Decided: `rclone`.** A single static binary to pin on the self-hosted runner
(no Python runtime, unlike aws-cli v2), a first-class `s3` backend with
`provider = Cloudflare`, `--immutable` as a real flag, and `rclone lsf` as a
cheap "does this prefix already have objects?" query — which is precisely what
the immutability gate needs. `wrangler` was rejected: it drags in Node and its
R2 object commands are the least ergonomic of the three for bulk upload.
*(Alternative if the owner prefers: `aws s3 cp --endpoint-url` behaves
identically for our purposes; only `scripts/publish-r2.sh` would change.)*

Configured entirely by environment, no config file on disk:

```
RCLONE_CONFIG_R2_TYPE=s3
RCLONE_CONFIG_R2_PROVIDER=Cloudflare
RCLONE_CONFIG_R2_ENDPOINT=https://${R2_ACCOUNT_ID}.r2.cloudflarestorage.com
RCLONE_CONFIG_R2_ACCESS_KEY_ID=${R2_ACCESS_KEY_ID}
RCLONE_CONFIG_R2_SECRET_ACCESS_KEY=${R2_SECRET_ACCESS_KEY}
RCLONE_CONFIG_R2_REGION=auto
RCLONE_CONFIG_R2_NO_CHECK_BUCKET=true
```

### GitHub secrets (repo scope)

| Name | Kind | Value |
|---|---|---|
| `R2_ACCOUNT_ID` | secret | Cloudflare account id |
| `R2_ACCESS_KEY_ID` | secret | R2 API token access key |
| `R2_SECRET_ACCESS_KEY` | secret | R2 API token secret |

### GitHub variables (repo scope, not secret)

| Name | Value |
|---|---|
| `R2_BUCKET_DOWNLOADS` | `dexel-downloads` |
| `R2_BUCKET_GET` | `dexel-get` |
| `DOWNLOADS_BASE_URL` | `https://downloads.dexel.jwdlab.com` |
| `R2_PUBLISH_ENABLED` | `true` — the dormancy gate |

`R2_PUBLISH_ENABLED` follows the pattern `desktop.yml` already uses
(`DESKTOP_LINUX_RUNNER`, `MAC_RUNNER`): the publish job is **skipped cleanly**
until the owner has actually created the buckets, instead of failing every
release with an auth error. The R2 API token is scoped to **Object Read & Write
on these two buckets only** — never an account-wide token.

---

## 5. The workflow changes

### 5.1 `release.yml` — add two jobs

```
release          (exists, unchanged except C1)  ──┐
release-macos    (exists, gated no-op)         ──┤
                                                 ├─> publish  ──> manifest
```

**`publish`** — `needs: [release]`, `if: vars.R2_PUBLISH_ENABLED == 'true'`:

1. re-download this run's `dist/` (or keep it — same self-hosted runner, so
   `actions/upload-artifact` + `download-artifact` between jobs is the honest
   way and survives a runner change later);
2. `cp dist/sha256sums.txt dist/checksums.txt`;
3. **immutability gate (C4):** for every file about to be uploaded,
   `rclone lsf r2:$BUCKET/releases/$VERSION/<file>` — if it exists, **fail the
   job** with
   `::error::releases/$VERSION/<file> is already published. Published objects are
   immutable — bump the tag.`
   Note the granularity: the gate is **per object, not per version prefix**.
   That is deliberate (C6) — it lets a macOS build land into an existing
   `v1.4.0/` later without ever overwriting anything.
   `checksums.txt` is the one exception: a later platform must be able to append
   its line, so it is uploaded as a **merge** (download existing, append missing
   lines, sort, re-upload) and the gate skips it. Merging is append-only and a
   changed hash for an existing filename is a hard failure.
4. `rclone copyto` each archive with `--header-upload "Cache-Control: public,
   max-age=31536000, immutable"`;
5. `rclone copyto` the merged `checksums.txt`;
6. re-verify by downloading each uploaded object's hash from R2 and comparing to
   `checksums.txt`. A release nobody verified after upload is a release nobody
   verified.

**`manifest`** — `needs: [publish]`:

1. `rclone lsjson r2:$BUCKET/releases/$VERSION/` → the artifacts that **actually
   exist**;
2. fetch `checksums.txt` for their hashes;
3. emit `manifest.json` per `ARCHITECTURE.md` Decision 19 — one entry per
   existing artifact, keyed `<goos>-<goarch>` derived from the filename,
   **omitting** every platform with no object;
4. validate: `schema == 1`, `version` matches the tag, every `url` returns
   `200` to a `HEAD`, every `sha256` is 64 hex chars, at least one artifact
   present;
5. upload `latest/manifest.json` and `latest/VERSION` with `max-age=60` —
   **only if this version is newer than the current `latest/VERSION`**, so
   re-running the workflow on an old tag cannot un-release a newer one.

**Why derive the manifest from the bucket instead of from `dist/`:** the macOS
job is gated (`release.yml`'s `release-macos`) and Windows arm64 has never run on
hardware. If the manifest were authored by whichever job happened to build, then
publishing `darwin-arm64` a week later would require rewriting an immutable
document. Deriving it means the manifest is always an honest statement of what is
downloadable *right now*, and adding a platform is a re-run of one job.

### 5.2 `deploy-installer.yml` — new, tiny

Trigger: `push` to `main` touching `scripts/install.sh`, plus
`workflow_dispatch`. Steps: `shellcheck scripts/install.sh` → run it in
`--dry-run` mode against the live manifest → `rclone copyto` it to
`r2:$R2_BUCKET_GET/install.sh` with `max-age=300`.

Separate from `release.yml` on purpose: fixing a bug in the installer must not
require cutting a release, and cutting a release must not silently change the
installer.

### 5.3 What stays on GitHub Releases

**Everything.** Archives and `sha256sums.txt` continue to be attached by
`softprops/action-gh-release@v2`, with `generate_release_notes: true`.

* GitHub Releases = the **human** channel: release notes, a browsable history, a
  fallback if R2 is misconfigured, and the thing a GitHub reader expects to find.
* R2 = the **machine** channel and the **canonical** one for install and update:
  `manifest.json` exists only there, and `install.sh` / `dexel update` only ever
  read `downloads.dexel.jwdlab.com`.

Both are produced from the same `dist/`, so they cannot disagree about bytes.
Recommendation stands as the owner framed it: **R2 canonical for the installer.**

---

## 6-STAGE-1. `install.sh` + `install.ps1` at the repo root — SHIPPED

**Amendment, superseding § 6's hosting and version-resolution assumptions
for as long as R2 does not exist.** Everything else in § 6 — the ten steps,
the consent rule, the exit-code discipline, the "no jq, no python, no sudo,
ever" rule — is unchanged and is what shipped.

The problem with implementing § 6 as written: it resolves a version from
`https://downloads.dexel.jwdlab.com/latest/VERSION` and a hash from
`checksums.txt` beside it, and **neither object exists**. R2 is not
provisioned, `publish` and `manifest` (C2/C3) are not written, and the
domains are not mapped. An installer written against that contract could not
be run, let alone tested. So stage 1 resolves the release from the thing
that does exist and is already the pipeline's output: **GitHub Releases, via
the GitHub API.**

### What changed from § 6

| § 6 as written (stage 2) | stage 1, shipped |
|---|---|
| `scripts/install.sh` | **`install.sh` and `install.ps1` at the repo ROOT** — the `curl \| bash` one-liner is a URL a human reads, and `raw.githubusercontent.com/.../main/install.sh` is shorter and more obviously the real thing than `.../main/scripts/install.sh`. It is also where every installer a developer has ever piped lives |
| version from `latest/VERSION` | `GET /repos/jawwadzafar/dexel/releases/latest` → `tag_name`, still validated against `^v[0-9]+\.[0-9]+\.[0-9]+`. `DEXEL_VERSION` pins a tag and switches to `/releases/tags/<tag>` |
| hash from `checksums.txt` | the release's own `sha256sums.txt` asset, which `build-release.sh` already emits, parsed with the same `awk '$2 == f \|\| $2 == "*" f'` rule. Plus a **cross-check against the `digest` GitHub reports for the same asset**: two independent producers, and a disagreement is a hard failure |
| artifact URL by convention | resolved from the release's asset list, so "no build for this platform in this release" is a *fact about the release* rather than a 404 guess. `install.sh` parses that list with `sed`/`awk` only (§ 6's no-jq rule); `install.ps1` uses `ConvertFrom-Json`, which is built into PowerShell |
| §§ 3-5 R2 buckets, `manifest`, `deploy-installer.yml` | untouched and still the plan. When they land, `resolve_release` and `asset_url` are the only two functions that change |
| `install.ps1` is "phase 2" | shipped in the same pass. Windows is half of the published artifacts, and a Windows user with no installer has no story at all |
| "installs one binary", "starts no processes" | **superseded — see § 6-STAGE-1-DESKTOP below.** The scripts now also install an icon and a launcher entry, optionally the Linux GUI shell, and they start dexel at the end. `sudo`, elevation and *login autostart* are still never touched |
| step 9 appends to the detected rc inside `# >>> dexel >>>` markers | **prints** the export line for the detected shell (`$SHELL` → bashrc/zshrc/fish) instead. A `curl \| sh` reader has no diff to review, and an installer that silently rewrites `~/.zshrc` is exactly the surprise the consent rule exists to prevent. Windows still writes the **user** PATH (HKCU, via `SetEnvironmentVariable(..., 'User')`), because there is no "paste this line" equivalent there and the write is a single reversible registry value |

### Private-repo reality, and why it is in the shipped script

The repository is private today and public later, and the installer has to
work in both worlds without a second version of itself:

* `GITHUB_TOKEN` / `GH_TOKEN`, when set, is sent as a bearer token. That is
  what makes a private-repo install (and the end-to-end test of it) possible
  at all. It goes into a `0600` curl config file inside the temp dir, never
  onto a command line where `ps` would show it.
* **`https://github.com/<o>/<r>/releases/download/<tag>/<name>` answers 404
  to a bearer token on a private repo** — verified against this repository,
  not assumed. So the token path must use the assets API URL
  (`/releases/assets/<id>`) with `Accept: application/octet-stream`, which
  is why the scripts resolve asset *ids* rather than constructing download
  URLs. Anonymous requests use `browser_download_url` instead: CDN-backed,
  no API rate limit, and the path every public install will take.

### macOS

`build-release.sh` adds `darwin/arm64` only on a darwin host and
`release-macos` is a gated no-op, so no release contains a darwin archive
today. Rather than hardcode "no mac", both installers **ask the release**:
absent a `dexel-<tag>-darwin-<arch>.tar.gz` asset they print the honest
message and the two build-from-source commands, and exit 5. The day a mac
runner publishes that asset, the normal install path takes over with no edit
to either script — which is the same "omit the key, derive the truth"
property § 7 wants from the manifest.

### 6-STAGE-1-DESKTOP. One command installs everything, and it just starts

**Amendment to § 6's step 7 and step 10.** § 6 said "installs one binary" and
"starts no processes". Both were defensible and both were wrong about what a
one-command install owes the user: a binary on `$PATH` that you then have to
know to run, from a terminal, with no icon anywhere, is not an installed
desktop application. The consent rule this was justified by is about
*persistence* — a thing that comes back on every login forever — not about
running the program the user just asked for. That distinction is now drawn
where it belongs.

What was added, per OS:

| | Linux (`install.sh`) | Windows (`install.ps1`) | macOS |
|---|---|---|---|
| icon | `$XDG_DATA_HOME/icons/hicolor/128x128/apps/dexel.png` | `dexel.ico` next to `dexel.exe` (a `.lnk` stores an icon *path*, not pixels) | — |
| launcher | `$XDG_DATA_HOME/applications/dexel.desktop`, `Exec="<bindir>/dexel" open`, `TryExec`, `Icon=dexel`, `Categories=Game;`. `update-desktop-database` best-effort | `Dexel.lnk` in `[Environment]::GetFolderPath('Programs')` — the **per-user** Start Menu, so no elevation — via `WScript.Shell`, targeting `dexel.exe` with `Arguments = open` | — |
| GUI shell | the release's **AppImage** at `<bindir>/dexel-desktop.AppImage`, plus a `dexel-desktop` shim | none published yet; the shortcut targets `dexel open`, which uses the browser | `Dexel.app` from `mac-release.sh`'s `.dmg`, dragged to `/Applications` by the user |
| start | `dexel open` with a desktop session, `dexel start` without | `dexel open` | `dexel open` |

The parts that are decisions rather than plumbing:

* **The icon ships INSIDE the release archive**, not as a seventh release
  asset. `build-release.sh` stages `desktop/src-tauri/icons/128x128.png` as
  `dexel.png` (and `icon.ico` as `dexel.ico` for the windows targets) beside
  the binary. So the installer needs no second download and no second
  checksum to do desktop integration — the icon is covered by the archive's
  existing sha256 like everything else in it. An archive from before this
  change simply has no `dexel.png`, and the installer says so and writes the
  entry anyway with the theme's fallback icon.
* **The AppImage, not the `.deb`.** Both are on the release. `dpkg -i` needs
  root, and this installer does not `sudo`, ever — so the AppImage *is* the
  no-sudo path to a real window. It is only fetched when `$DISPLAY` or
  `$WAYLAND_DISPLAY` says there is a session to open into (`--app` forces,
  `--no-app` skips), and its ~84 MB size is printed before the download
  starts, not discovered during it.
* **`dexel-desktop` is a shim, not a rename.** `dexel open` looks for an
  executable named exactly `dexel-desktop` on `PATH` and then in `BinDir`
  (ARCHITECTURE.md Decision 17). An AppImage is one file with a different
  name, so the installer writes a four-line `sh` shim with that name which
  `exec`s the AppImage. The shim also picks `--appimage-extract-and-run` when
  `/dev/fuse` or `fusermount` is missing, because an AppImage on a FUSE-less
  box otherwise fails with a libfuse message instead of starting.
* **The AppImage is verified against ONE witness, and that is why it is
  optional.** `build-release.sh`'s `sha256sums.txt` covers only the archives
  that script builds — the Tauri bundles come from a different release job and
  are *absent* from it (checked against the live `v0.1.0` release: it lists
  four archives and no bundles). So the AppImage is verified against the
  `digest` GitHub reports for the asset. Still a hard exit 6 on a mismatch,
  still **before anything is installed**, so a tampered AppImage leaves the
  machine exactly as it was. If a future release *does* list it in
  `sha256sums.txt`, that line becomes the second opinion with no code change.
  A release reporting no digest at all gets the browser front door rather than
  an unverified binary.
* **Ordering.** Every download and every checksum now happens before the
  first write, which is what makes `--dry-run` a complete test of the release
  — including the AppImage — while touching nothing.
* **Login autostart is untouched.** `dexel autostart enable` is still the only
  thing that makes dexel come back on its own, and the installer's report says
  so explicitly.

Opt-outs, because "installs everything" must be a default and not a
mandate: `--no-start` / `--no-desktop` / `--no-app` / `--app` as flags on
Linux, and `DEXEL_NO_START` / `DEXEL_NO_DESKTOP` / `DEXEL_NO_APP` /
`DEXEL_APP` as environment variables on both platforms (a `| iex` one-liner
cannot pass parameters).

### Testing

Both scripts were run end to end against the live private `v0.1.0` release
(`GH_TOKEN="$(gh auth token)"`, `DEXEL_INSTALL_DIR` pointed at a temp dir):
resolve → download → verify → install → `dexel version`. The
checksum-mismatch path was exercised with a byte appended to a real archive
and hard-fails with exit 6 before unpacking anything. `install.sh` is
`shellcheck -s sh` clean and runs under `dash`, `bash` and busybox `ash`,
including piped-on-stdin with no script file. `install.ps1` parses clean and
is PSScriptAnalyzer-error-free, and every one of its functions was exercised
on PowerShell 7 against the real release. `--dry-run` /
`$env:DEXEL_DRY_RUN=1` stops after verification and is the mode a future
`deploy-installer.yml` should run in CI.

The desktop-integration and start behaviour was gated the same way, on Linux,
against the same live release, with a throwaway `HOME` and `XDG_DATA_HOME`:

* a full run installs the binary, writes `dexel.desktop` (which
  `desktop-file-validate` accepts) and starts the runtime — confirmed
  `"running": true` with a real pid and port via `dexel status --json`, then
  stopped again.
* with a session present, `Dexel_0.1.0_amd64.AppImage` (84 MB) downloads,
  verifies against GitHub's digest, and lands with its `dexel-desktop` shim;
  the shim is itself `shellcheck -s sh` clean, and `dexel-desktop
  --appimage-offset` returns the squashfs offset and exit 0, which proves the
  shim resolves and `exec`s a working AppImage runtime. **Rendering the actual
  window is NOT verified** — that needs a human looking at a screen.
* a second identical run leaves all four artifacts byte-for-byte identical
  (binary, AppImage, shim, `.desktop`), and `update-desktop-database` is
  observably reached (it leaves a `mimeinfo.cache`).
* the icon path was exercised against a locally rebuilt archive that carries
  `dexel.png`: it lands at
  `$XDG_DATA_HOME/icons/hicolor/128x128/apps/dexel.png` byte-identical to
  `desktop/src-tauri/icons/128x128.png`.
* both tamper paths hard-fail with exit 6 and zero files written: a byte
  appended to the archive, and a digest mismatch on the AppImage.
* `install.ps1` is pure ASCII, parses clean on PowerShell 7.4, and has zero
  PSScriptAnalyzer **errors**. Its new helpers were unit-tested by
  dot-sourcing. **The `WScript.Shell` shortcut creation itself is unverified**
  — there is no Windows host in this environment.

**Amendment — `install.sh` now delegates to `install.ps1` on Git Bash/MSYS2/
Cygwin instead of just printing the PowerShell one-liner.** `uname -s`
answering `MINGW*`/`MSYS*`/`CYGWIN*` used to be an immediate exit 3 with
instructions; it now locates `powershell.exe`/`pwsh.exe` (PATH, then the
usual Git-Bash/Cygwin absolute path), downloads `install.ps1` from the same
`$REPO` + `main` ref this file already treats as canonical, converts the
temp path with `cygpath -w` when available, and runs it with
`-NoProfile -ExecutionPolicy Bypass -File`, ending with its exit code. This
makes `curl \| bash` genuinely one command everywhere bash exists, including
a stock Windows box with only Git Bash installed — the exit-3 message is
now the fallback for when no PowerShell can be found, not the default for
being on Windows at all. WSL is untouched: its `uname -s` answers `Linux`,
so it already took (and still takes) the normal Linux branch, which is the
right outcome for a WSL user, not a gap. install.ps1 has no checksum sidecar
of its own, so fetching it is the same `curl \| sh` trust level this file
already accepts, extended one hop, not a new weaker one. Verified: `--dry-run`
against the live release (unchanged, Linux path); `shellcheck -s sh` clean;
parses under `dash -n`/`bash -n`/`busybox ash -n`; the delegation path itself
was simulated with a stub `powershell.exe` + stub `cygpath` on `PATH` and
`DEXEL_UNAME_S=MINGW64_NT-...` (no Windows host exists in this environment)
— confirmed it downloads `install.ps1` for real, converts the path, invokes
the stub with the documented flags and env, and propagates both a `0` and a
`7` stub exit code unchanged.

---

## 6. `scripts/install.sh`

> **Superseded for stage 1 by § 6-STAGE-1 above** on hosting and version
> resolution; the ten steps below are otherwise what shipped.

**Source of truth: `scripts/install.sh` in this repo** (the repo already keeps
`build-release.sh`, `build-sidecar.sh`, `build.sh`, `visual-check.py` there — one
convention, not a new `deploy/` tree). Deployed to
`r2:dexel-get/install.sh` by `deploy-installer.yml`. Served at
`https://get.dexel.jwdlab.com/install.sh` and, via the bucket's index document,
at `https://get.dexel.jwdlab.com/`.

POSIX `sh` — not bash. `set -eu` (never `pipefail`, which `sh` lacks). No `jq`,
no `python`, no `sudo`, ever.

### The ten steps, exactly

1. **Detect OS + arch.** `uname -s` → `linux` | `darwin`; `uname -m` →
   `x86_64|amd64 → amd64`, `aarch64|arm64 → arm64`. Anything else exits 3 naming
   what it saw and pointing at README § Building from source. Windows is not
   covered by `install.sh` — `install.ps1` is phase 2 and the manifest already
   carries the artifacts.
2. **Check tools.** `curl` or `wget`; `tar`; `sha256sum` or `shasum -a 256`
   (macOS has the latter, not the former); `mktemp`; `install` or `cp`+`chmod`.
   Missing → exit 4 naming the tool.
3. **Resolve the version.** `$DEXEL_VERSION` if set, else fetch
   `https://downloads.dexel.jwdlab.com/latest/VERSION` and read one line.
   Validate it against `^v[0-9]+\.[0-9]+\.[0-9]+`.
4. **Resolve the artifact and its hash** — *without parsing JSON.* URL by
   convention:
   `$DOWNLOADS/releases/$VERSION/dexel-$VERSION-$OS-$ARCH.tar.gz`; hash from
   `$DOWNLOADS/releases/$VERSION/checksums.txt` via
   `awk -v f="$file" '$2==f || $2=="*"f {print $1}'`. A missing line means **no
   build for this platform in this version** → exit 5, saying exactly that.
   *This is the deliberate division of labour:* `install.sh` uses
   `VERSION` + `checksums.txt` because shell JSON parsing is a fragility we
   refuse to ship; `dexel update` uses `manifest.json` because Go parses JSON
   properly. Both files are generated by the same `manifest` job from the same
   bucket listing, and that job asserts they agree — one source, two encodings
   for two consumers.
5. **Download** into `mktemp -d` (trapped `rm -rf` on EXIT/INT/TERM), with
   `curl -fsSL --proto '=https' --tlsv1.2` (or `wget --https-only`). HTTPS only,
   no plain-HTTP fallback, ever.
6. **Verify sha256** against step 4's value. Mismatch → print both hashes and
   exit 6 **without unpacking anything**.
7. **Unpack and install.** `tar -xzf` into the temp dir,
   `install -m 0755 <dir>/dexel "$BINDIR/dexel"` where
   `BINDIR=${DEXEL_INSTALL_DIR:-$HOME/.local/bin}`, `mkdir -p` first.
8. **Create directories.** `StateDir` and `StateDir/logs` per
   `PLATFORM_NOTES.md` §1 — the installer creates them so a first
   `dexel start` cannot fail on a missing directory, and so `dexel status` has
   somewhere to point.
9. **PATH.** If `$BINDIR` is not in `$PATH`, append an export line to the
   *detected* rc (`$HOME/.zshrc`, `$HOME/.bashrc`, or
   `$HOME/.config/fish/config.fish`) wrapped in a marker comment
   (`# >>> dexel >>>` / `# <<< dexel <<<`) so re-running is idempotent and
   removal is greppable. Print the exact file and line changed, and tell the user
   to restart the shell or `source` it. Never touch `/etc`, never touch a file
   outside `$HOME`, and skip entirely under `--no-path`.
10. **Verify and report.** Run `"$BINDIR/dexel" version`, assert it prints
    `$VERSION`, then print:

```
dexel v1.4.0 installed to /home/you/.local/bin/dexel

Next:
  dexel start              start the background runtime
  dexel open               open the window (browser if the desktop app isn't installed)
  dexel status             is it running? what is it seeing?
  dexel pause / resume     stop and restart tracking
  dexel autostart enable   start dexel at login  (NOT enabled — this is opt-in)
  dexel update             upgrade in place

Your data lives in /home/you/.config/dexel — updates never touch it.
```

**Autostart is not enabled and the runtime is not started.** The script says so
in that block. `dexel autostart enable` is the explicit consent, exactly as the
owner specified.

Flags/env: `--dry-run` (steps 1-6, no writes — the same mode
`deploy-installer.yml` runs in CI), `DEXEL_VERSION` (pin), `DEXEL_INSTALL_DIR`
(relocate), `--no-path`. Distinct exit codes 3-6 per failure class so a piped run
is diagnosable from `$?` alone.

---

## 7. Platforms that do not exist yet

**Decision: omit the key.** Recorded in `ARCHITECTURE.md` Decision 19 and
implemented in the `manifest` job by deriving from the bucket listing.

Consequences, each of them good:

* `manifest.json` for `v1.4.0` on the day it is cut contains
  `linux-amd64`, `linux-arm64`, `windows-amd64`, `windows-arm64` — and **not**
  `darwin-arm64`, because `release.yml`'s `release-macos` job is a gated no-op.
* `install.sh` on a Mac exits 5 with "no darwin-arm64 build in v1.4.0 yet — see
  README § Building from source". True, actionable, not a crash.
* `dexel update` on a Mac says the same.
* When the owner registers their Mac (F3-design FORK 1), the flipped
  `release-macos` job builds `darwin/arm64` natively, uploads it into the
  **existing** `releases/v1.4.0/` prefix (allowed — the gate is per object),
  appends its line to `checksums.txt`, and re-runs `manifest`. The manifest gains
  `darwin-arm64` and the same `v1.4.0` becomes installable on macOS with no new
  tag.

A `null` url or a `"status": "pending"` placeholder was rejected: it is a value
every consumer must special-case, and the first consumer to forget crashes on a
Mac — the product's primary platform.

---

## 8. Security posture of the channel

* HTTPS only, TLS 1.2+, no HTTP fallback, no cross-host redirect following in
  either `install.sh` or `dexel update`.
* sha256 verified **before** anything is unpacked or executed, in both.
* Published objects are immutable; only `latest/manifest.json`, `latest/VERSION`
  and `install.sh` are ever overwritten, and only by CI.
* The R2 API token is scoped to the two buckets, Object Read & Write only. It
  lives only in GitHub secrets and is never echoed (`rclone` config comes from
  env, so no config file is written to the runner's disk).
* `install.sh` needs no privilege. If it ever needs `sudo`, that is a design
  regression.
* **Signing is deferred** (`docs/plan/F3-design.md` §6 — the certificates are an
  owner purchase). The manifest reserves a `signatures` key so adding minisign or
  cosign later is additive: publish `<artifact>.sig` next to each artifact, add
  `"signature"` per artifact entry, teach the updater to verify it when present.
  Until then, sha256-over-HTTPS from an immutable bucket is the honest ceiling
  and the download page should say so rather than implying more.
* `curl | sh` is what the owner asked for and what every comparable dev tool
  ships. Mitigations that cost nothing: the script is short, readable, in the
  repo, deployed by CI from `main`, and the docs also show
  `curl -fsSL ... -o install.sh && less install.sh && sh install.sh`.

---

## 9. OWNER-ACTION CHECKLIST

Nothing in §§3-8 works until these are done. None of them are code.

**DNS / Cloudflare**

- [ ] `jwdlab.com` is on Cloudflare (nameservers delegated).
- [ ] Create R2 bucket **`dexel-downloads`**, region auto.
- [ ] Create R2 bucket **`dexel-get`**, region auto.
- [ ] Attach custom domain **`downloads.dexel.jwdlab.com`** to `dexel-downloads`
      (R2 → bucket → Settings → Public access → Custom domain). This creates the
      CNAME and the certificate automatically.
- [ ] Attach custom domain **`get.dexel.jwdlab.com`** to `dexel-get`.
- [ ] On `dexel-get`, set the **index document** to `install.sh` so
      `https://get.dexel.jwdlab.com/` works as well as `/install.sh`.
- [ ] Decide `dexel.jwdlab.com`: Cloudflare Pages (recommended) or a third
      bucket with `index.html`.
- [ ] Do **not** enable public `r2.dev` development URLs — the custom domains are
      the only public surface.

**Credentials**

- [ ] R2 → Manage API tokens → create a token, permission **Object Read & Write**,
      scoped to **only** `dexel-downloads` and `dexel-get`.
- [ ] Note the Account ID, Access Key ID, Secret Access Key (shown once).

**GitHub repo → Settings → Secrets and variables → Actions**

- [ ] Secret `R2_ACCOUNT_ID`
- [ ] Secret `R2_ACCESS_KEY_ID`
- [ ] Secret `R2_SECRET_ACCESS_KEY`
- [ ] Variable `R2_BUCKET_DOWNLOADS` = `dexel-downloads`
- [ ] Variable `R2_BUCKET_GET` = `dexel-get`
- [ ] Variable `DOWNLOADS_BASE_URL` = `https://downloads.dexel.jwdlab.com`
- [ ] Variable `R2_PUBLISH_ENABLED` = `true` (leave unset until the buckets exist
      — the publish job then skips cleanly instead of failing)

**Runner**

- [ ] Install `rclone` on `jwdlab-runner` (single static binary; pin the version)
      or accept a download-and-verify step at the top of the publish job.
- [ ] Register the owner's Mac as a self-hosted runner with label **`mac`** to
      unblock `darwin-arm64` (F3-design FORK 1). Optional for the first release —
      §7 makes a mac-less release honest rather than broken.

**Smoke test, once, by hand**

- [ ] `echo ok > healthcheck.txt`, upload it to `dexel-downloads`, and confirm
      `curl -fsSL https://downloads.dexel.jwdlab.com/healthcheck.txt` prints `ok`.
      This proves DNS + certificate + public access before any CI runs, and is the
      exit criterion for the infra step in `MIGRATION_PLAN.md`.
- [ ] Delete `healthcheck.txt` afterwards.

**Deferred, named so it does not creep**

- [ ] Apple Developer ID certificate + notarization (macOS `.dmg`).
- [ ] Authenticode certificate (Windows `.msi`).
- [ ] minisign/cosign artifact signatures + the `signatures` manifest key.
- [ ] Homebrew tap / winget / AUR — all downstream consumers of the same manifest.
