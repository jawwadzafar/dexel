// cmd_update.go — `dexel update [--check] [-y/--yes] [--force]`, the
// self-updater (docs/production-runtime/ARCHITECTURE.md §8,
// MIGRATION_PLAN.md §PR-7 — the outstanding half of that PR).
//
// It is the Go mirror of install.sh's RELEASE-RESOLUTION + trust model,
// because GitHub Releases is what exists today and R2 is not provisioned
// yet (install.sh's own header says the same). The shape:
//
//	resolve the latest release via the GitHub API for jawwadzafar/dexel
//	  -> compare its tag to this binary's main.version
//	  -> if not newer: "already up to date", exit 0 (THE FREEZE REALITY)
//	  -> if newer: download THIS os/arch's archive, verify its sha256
//	     against sha256sums.txt AND cross-check GitHub's reported digest
//	     (the same two-witness bar install.sh holds — refuse on either
//	     mismatch, before anything on disk is touched)
//	  -> unpack the binary, run it once to prove it is a working dexel
//	  -> the REPLACE-AND-RESTART dance (swapBinary): you cannot overwrite
//	     a running/open binary in place, so write the new one beside the
//	     target on the SAME filesystem, rename(old -> old+".old") then
//	     rename(new -> target) (atomic on one fs), preserving mode
//	  -> if a runtime was running, stop it and start the new one (the same
//	     stop->start dance `dexel restart` uses)
//	  -> clean up the ".old" on success. The state dir is NEVER touched:
//	     upgrades preserve the save and config, always.
//
// GRACEFUL DEGRADATION is most of what runs today, and is a feature, not
// an afterthought: the repo is still private and the version is frozen at
// v0.1.0, so the paths that actually execute are "up to date" (latest ==
// current), "couldn't reach the release feed" (private repo with no
// token, or a 404), and "offline". None of them crash, none of them
// leave a half-replaced binary, and each says exactly what to do next.
//
// PRIVACY: this verb talks to GitHub ONLY, and only for release metadata
// and release bytes. It sends no user data — no save, no config, no
// activity, nothing from the state dir. The content-free boundary
// (feature-build-and-verify) is unaffected: there is no wire-protocol or
// persistence change here.
package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/jawwadzafar/dexel/app/internal/lifecycle"
)

// Exit codes, deliberately aligned with install.sh's scheme (see its
// header) so a scripted `dexel update` is diagnosable from $? alone and a
// reader who knows the installer already knows these:
//
//	0 success (applied, OR already up to date, OR --check completed)
//	1 a local failure (filesystem, the swap, or a failed restart)
//	2 usage (bad flags) — the CLI-wide convention (see cli.go)
//	5 no build for THIS os/arch in the resolved release
//	6 checksum / digest mismatch (nothing was replaced)
//	7 could not reach the release feed (private repo / offline / 404)
//	8 the downloaded binary failed its own version check (nothing replaced)
const (
	updateOK       = 0
	updateFailure  = 1
	updateUsage    = 2
	updateNoBuild  = 5
	updateChecksum = 6
	updateNetwork  = 7
	updateVerify   = 8
)

// updateRepo is the repository release metadata is resolved against. The
// same owner/name install.sh defaults to (DEXEL_REPO there); pinned here
// rather than read from the environment because a self-updater that could
// be pointed at an arbitrary repo by an env var is a supply-chain hole,
// not a convenience.
const updateRepo = "jawwadzafar/dexel"

// defaultGitHubAPI is the API root. A field on updater (not a constant at
// the call site) so cmd_update_test.go can point the whole resolve →
// download → verify pipeline at an httptest server without a network.
const defaultGitHubAPI = "https://api.github.com"

// checksumsAsset is the release asset install.sh and build-release.sh
// both name: one file covering every archive in the release.
const checksumsAsset = "sha256sums.txt"

// ------------------------------------------------------------------ types

// ghAsset is the subset of a GitHub release asset this verb reads. The
// two URLs are NOT interchangeable (see assetURL): browser_download_url
// is the CDN path a public download takes, and `url` is the assets API
// path a bearer token must use because github.com/.../releases/download/
// answers 404 to a token on a private repo (install.sh's asset_url proves
// this against this very repository).
type ghAsset struct {
	Name               string `json:"name"`
	URL                string `json:"url"`
	BrowserDownloadURL string `json:"browser_download_url"`
	// Digest is GitHub's own hash of the bytes it stored, "sha256:<hex>".
	// It is the SECOND witness: sha256sums.txt (written by build-release.sh)
	// and this (computed by GitHub on receipt) are produced by different
	// systems and must never disagree.
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

// ghRelease is the subset of GET /releases/latest this verb reads.
type ghRelease struct {
	TagName    string    `json:"tag_name"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
	Assets     []ghAsset `json:"assets"`
}

// updater bundles everything the release logic needs, resolved once, so
// the pure decision code below can be driven from a test with no network
// and no real binary on disk.
type updater struct {
	repo      string
	apiBase   string
	current   string // this binary's main.version
	goos      string
	goarch    string
	token     string
	client    *http.Client
	out       io.Writer
	errOut    io.Writer
	self      string // the target binary (os.Executable), the swap target
	verifyRun func(path string) error
}

// ------------------------------------------------------------ entry point

// cmdUpdate is the whole verb.
func cmdUpdate(args []string) int {
	fs := flag.NewFlagSet("dexel update", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	check := fs.Bool("check", false, "report whether a newer release exists, then stop — change nothing")
	yes := fs.Bool("yes", false, "apply without asking (the scripting path); an explicit flag is itself the consent")
	y := fs.Bool("y", false, "alias for --yes")
	force := fs.Bool("force", false, "reinstall even when the latest release is not newer (or to update a `dev` build)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: dexel update [--check] [-y|--yes] [--force]")
		fmt.Fprintln(os.Stderr, "\nUpdate dexel to the latest release, in place, preserving your save and config.")
		fmt.Fprintln(os.Stderr, "\nFlags:")
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr, "\nA private repository needs a token that can read it:")
		fmt.Fprintln(os.Stderr, "    GH_TOKEN=\"$(gh auth token)\" dexel update")
		fmt.Fprintln(os.Stderr, "\nExit codes: 0 ok/up-to-date  5 no build for this os/arch  6 checksum mismatch")
		fmt.Fprintln(os.Stderr, "            7 release feed unreachable  8 downloaded binary failed its check")
	}
	if err := fs.Parse(args); err != nil {
		// `-h`/`--help` is a successful request for the usage flag already
		// printed, not an error — exit 0, the same as the ExitOnError verbs
		// (logs, status, uninstall). Any other parse error is a usage error.
		if errors.Is(err, flag.ErrHelp) {
			return updateOK
		}
		return updateUsage
	}
	assumeYes := *yes || *y

	env, code := cliEnvOrReport()
	if env == nil {
		return code
	}

	u := &updater{
		repo:    updateRepo,
		apiBase: defaultGitHubAPI,
		current: version,
		goos:    runtime.GOOS,
		goarch:  runtime.GOARCH,
		token:   resolveUpdateToken(os.Getenv),
		client:  &http.Client{},
		out:     env.out,
		errOut:  env.errOut,
		self:    env.self,
	}
	u.verifyRun = u.runVersionCheck

	return u.run(env, *check, assumeYes, *force)
}

// run is cmdUpdate minus flag parsing and environment resolution — the
// part a test can call directly with a stubbed updater.
func (u *updater) run(env *cliEnv, check, assumeYes, force bool) int {
	// 1. Resolve the latest release. This is the step that degrades
	//    gracefully: private-repo/offline/404 all land here, and all of
	//    them are non-zero-but-not-a-crash with a clear next step.
	rel, err := u.latestRelease()
	if err != nil {
		u.reportResolveFailure(err)
		return updateNetwork
	}
	latest := rel.TagName

	// 2. Compare. A `dev` build has no version to compare and cannot be
	//    trusted to be older or newer than anything (it may be AHEAD of
	//    the latest release), so it is refused unless --force.
	if isDevVersion(u.current) && !force {
		fmt.Fprintf(u.out, "dexel update: this is a development build (version %q), so there is no released\n", u.current)
		fmt.Fprintf(u.out, "version to compare against %s. Re-run with --force to install %s over it.\n", latest, latest)
		return updateOK
	}

	newer, cmpErr := isNewerVersion(latest, u.current)
	if cmpErr != nil && !force {
		fmt.Fprintf(u.errOut, "dexel update: could not compare the running version (%q) with the latest release (%q): %v\n", u.current, latest, cmpErr)
		fmt.Fprintln(u.errOut, "dexel update: re-run with --force to install it anyway.")
		return updateFailure
	}

	// 3. Not newer (the freeze reality: latest == current == v0.1.0) —
	//    unless --force asked for a reinstall/downgrade.
	if !newer && !force {
		if cmpErr == nil && !sameVersion(latest, u.current) {
			// latest is OLDER than what is running (a local build ahead of
			// the feed). Still "up to date" from the user's point of view.
			fmt.Fprintf(u.out, "dexel is already up to date (%s; the latest release is %s)\n", u.current, latest)
		} else {
			fmt.Fprintf(u.out, "dexel is already up to date (%s)\n", u.current)
		}
		return updateOK
	}

	// 4. --check stops here, having changed nothing.
	if check {
		if newer {
			fmt.Fprintf(u.out, "an update is available: %s (you have %s)\n", latest, u.current)
			fmt.Fprintln(u.out, "run `dexel update` to install it")
		} else {
			// --check --force on an up-to-date binary: honest about both.
			fmt.Fprintf(u.out, "the latest release is %s; you have %s (--force would reinstall it)\n", latest, u.current)
		}
		return updateOK
	}

	// 5. Consent, mirroring `dexel uninstall`'s idiom (cmd_uninstall.go):
	//    --yes is consent; a terminal is asked; a non-tty without --yes
	//    REFUSES rather than guessing, so a piped update that forgot the
	//    flag does not silently swap a binary out from under a service.
	switch resolveConsent(assumeYes, stdinIsTerminal()) {
	case consentImpossible:
		fmt.Fprintln(u.errOut, "dexel update: needs a terminal to confirm, and stdin is not one.")
		fmt.Fprintln(u.errOut, "dexel update: pass --yes to update without being asked (the scripting path), or run it from a terminal.")
		return updateUsage
	case consentAsk:
		fmt.Fprintf(u.out, "This will replace dexel %s with %s and restart it if it is running.\n", u.current, latest)
		fmt.Fprintln(u.out, "Your save and config are not touched. Continue? [y/N] ")
		if !answerIsYes(readAnswer(os.Stdin)) {
			fmt.Fprintln(u.out, "cancelled; nothing was changed")
			return updateOK
		}
	case consentGranted:
		// no question — but say what is about to happen anyway.
		fmt.Fprintf(u.out, "updating dexel %s -> %s\n", u.current, latest)
	}

	return u.apply(env, rel)
}

// apply performs the download → verify → unpack → swap → restart, in that
// order, and never advances past a failed step: nothing on disk is
// touched until AFTER both checksum witnesses agree and the new binary
// has proven it runs.
func (u *updater) apply(env *cliEnv, rel *ghRelease) int {
	archiveName := platformAssetName(u.goos, u.goarch, rel.TagName)

	asset, ok := findAsset(rel.Assets, archiveName)
	if !ok {
		fmt.Fprintf(u.errOut, "dexel update: release %s has no build for this platform (expected %s).\n", rel.TagName, archiveName)
		fmt.Fprintln(u.errOut, "dexel update: this release ships:")
		for _, a := range rel.Assets {
			fmt.Fprintf(u.errOut, "    %s\n", a.Name)
		}
		return updateNoBuild
	}
	sums, ok := findAsset(rel.Assets, checksumsAsset)
	if !ok {
		fmt.Fprintf(u.errOut, "dexel update: release %s has no %s, so the download cannot be verified. Refusing.\n", rel.TagName, checksumsAsset)
		return updateChecksum
	}

	// Everything downloaded lives in a temp dir that is removed on the way
	// out; the ONE file that must sit on the target's filesystem (the new
	// binary, for an atomic rename) is created in the target's own
	// directory further down.
	work, err := os.MkdirTemp("", "dexel-update-*")
	if err != nil {
		fmt.Fprintf(u.errOut, "dexel update: create a work directory: %v\n", err)
		return updateFailure
	}
	defer func() { _ = os.RemoveAll(work) }()

	archivePath := filepath.Join(work, archiveName)
	fmt.Fprintf(u.out, "downloading %s...\n", archiveName)
	if err := u.download(assetURL(asset, u.token), archivePath); err != nil {
		fmt.Fprintf(u.errOut, "dexel update: download %s: %v\n", archiveName, err)
		return updateNetwork
	}
	sumsPath := filepath.Join(work, checksumsAsset)
	if err := u.download(assetURL(sums, u.token), sumsPath); err != nil {
		fmt.Fprintf(u.errOut, "dexel update: download %s: %v\n", checksumsAsset, err)
		return updateNetwork
	}

	// Two witnesses, before anything is unpacked (install.sh's verify()).
	got, err := sha256OfFile(archivePath)
	if err != nil {
		fmt.Fprintf(u.errOut, "dexel update: hash %s: %v\n", archiveName, err)
		return updateFailure
	}
	sumsContent, err := os.ReadFile(sumsPath)
	if err != nil {
		fmt.Fprintf(u.errOut, "dexel update: read %s: %v\n", checksumsAsset, err)
		return updateFailure
	}
	if err := verifyChecksum(archiveName, got, string(sumsContent), asset.Digest); err != nil {
		fmt.Fprintln(u.errOut, "dexel update:", err)
		fmt.Fprintln(u.errOut, "dexel update: nothing was unpacked and nothing was replaced.")
		return updateChecksum
	}
	fmt.Fprintln(u.out, "sha256 verified (checksum file and GitHub agree)")

	// Unpack the binary to a temp file NEXT TO the target, so the rename
	// that installs it is on one filesystem and therefore atomic.
	binBase := binaryBaseName(u.goos)
	newBin, err := os.CreateTemp(filepath.Dir(u.self), ".dexel.new-*")
	if err != nil {
		fmt.Fprintf(u.errOut, "dexel update: create the staging file beside %s: %v\n", u.self, err)
		return updateFailure
	}
	newBinPath := newBin.Name()
	_ = newBin.Close()
	// From here on any early return must remove the staging file, or a
	// failed update would litter the bin dir with .dexel.new-* droppings.
	extractOK := false
	defer func() {
		if !extractOK {
			_ = os.Remove(newBinPath)
		}
	}()

	if err := extractBinary(archivePath, u.goos, binBase, newBinPath); err != nil {
		fmt.Fprintf(u.errOut, "dexel update: unpack %s from %s: %v\n", binBase, archiveName, err)
		return updateFailure
	}

	// Execute-and-verify (ARCHITECTURE.md §8): prove the freshly unpacked
	// bytes are a working dexel BEFORE they become the installed binary.
	// A corrupt or wrong-arch binary that passed the archive checksum
	// (it cannot, but defence in depth is cheap) is caught here, still
	// before the swap.
	if err := u.verifyRun(newBinPath); err != nil {
		fmt.Fprintf(u.errOut, "dexel update: the downloaded binary failed its own version check: %v\n", err)
		fmt.Fprintln(u.errOut, "dexel update: nothing was replaced.")
		return updateVerify
	}

	// Preserve the mode of the binary being replaced.
	mode := os.FileMode(0o755)
	if fi, err := os.Stat(u.self); err == nil {
		mode = fi.Mode().Perm()
	}

	// Was a runtime running BEFORE the swap? Decide now, so the restart
	// after the swap only starts a runtime that was actually up — an
	// update must not launch a daemon the user had stopped.
	wasRunning := false
	if st, derr := lifecycle.Query(env.stateDir, env.client); derr == nil {
		wasRunning = st.Running
	}

	// THE SWAP. After this returns nil the new binary IS the target; the
	// old one is preserved at target+".old" (see swapBinary).
	oldPath, err := swapBinary(u.self, newBinPath, mode)
	if err != nil {
		fmt.Fprintf(u.errOut, "dexel update: %v\n", err)
		return updateFailure
	}
	extractOK = true // the staging file was consumed by the rename
	fmt.Fprintf(u.out, "installed %s at %s\n", rel.TagName, u.self)

	// Replace-and-RESTART: stop the old runtime and start the new one,
	// the exact stop->start dance `dexel restart` uses (cmd_lifecycle.go).
	// Only if it was running; the state dir is never touched either way.
	restartCode := updateOK
	if wasRunning {
		fmt.Fprintln(u.out, "restarting the running runtime on the new version...")
		if rc := env.stop(); rc != 0 {
			fmt.Fprintln(u.errOut, "dexel update: could not stop the old runtime; start it yourself with `dexel start`")
			restartCode = updateFailure
		} else if rc := env.start(nil); rc != 0 {
			fmt.Fprintln(u.errOut, "dexel update: the new runtime did not start; run `dexel start` to retry")
			restartCode = updateFailure
		}
	} else {
		fmt.Fprintln(u.out, "no runtime was running; start dexel when you like with `dexel start` (or just `dexel`)")
	}

	// Clean up the .old on success. On unix os.Remove succeeds even though
	// THIS process is still mapped from that inode; on Windows the running
	// image cannot be unlinked, so a failure here is expected and benign —
	// reported once, never fatal, and never enough to fail the update.
	if err := os.Remove(oldPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(u.errOut, "dexel update: could not remove the old binary at %s (%v) — safe to delete by hand\n", oldPath, err)
	}

	if restartCode != updateOK {
		return restartCode
	}
	fmt.Fprintf(u.out, "dexel is now %s\n", rel.TagName)
	return updateOK
}

// ------------------------------------------------------- release metadata

// latestRelease resolves GET /repos/<repo>/releases/latest.
func (u *updater) latestRelease() (*ghRelease, error) {
	url := fmt.Sprintf("%s/repos/%s/releases/latest", strings.TrimRight(u.apiBase, "/"), u.repo)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	u.setHeaders(req, "application/vnd.github+json")
	resp, err := u.client.Do(req)
	if err != nil {
		// A transport error is "offline" (DNS, refused, TLS, timeout).
		return nil, &resolveError{transport: true, err: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, &resolveError{status: resp.StatusCode, body: strings.TrimSpace(string(body))}
	}
	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("decode release JSON: %w", err)
	}
	if rel.TagName == "" {
		return nil, errors.New("the API response had no tag_name")
	}
	return &rel, nil
}

// resolveError distinguishes the graceful-degradation cases so the
// message can name the right fix (a token, or a connection).
type resolveError struct {
	transport bool
	status    int
	body      string
	err       error
}

func (e *resolveError) Error() string {
	if e.transport {
		return "network: " + e.err.Error()
	}
	return fmt.Sprintf("HTTP %d", e.status)
}

// reportResolveFailure turns a resolveError into the user-facing,
// no-crash message for each degradation case.
func (u *updater) reportResolveFailure(err error) {
	var re *resolveError
	if !errors.As(err, &re) {
		fmt.Fprintf(u.errOut, "dexel update: could not resolve the latest release: %v\n", err)
		return
	}
	switch {
	case re.transport:
		fmt.Fprintf(u.errOut, "dexel update: could not reach GitHub — are you online? (%v)\n", re.err)
	case re.status == http.StatusNotFound:
		fmt.Fprintf(u.errOut, "dexel update: couldn't reach the release feed for %s (HTTP 404).\n", u.repo)
		u.privateRepoHint()
	case re.status == http.StatusUnauthorized || re.status == http.StatusForbidden:
		fmt.Fprintf(u.errOut, "dexel update: GitHub refused the request (HTTP %d).\n", re.status)
		u.privateRepoHint()
	default:
		fmt.Fprintf(u.errOut, "dexel update: couldn't read the release feed for %s (HTTP %d).\n", u.repo, re.status)
		u.privateRepoHint()
	}
}

func (u *updater) privateRepoHint() {
	if u.token != "" {
		fmt.Fprintln(u.errOut, "dexel update: a token was sent — check that it can read "+u.repo+".")
	} else {
		fmt.Fprintln(u.errOut, "dexel update: is the repo public yet? If it is still private, export a token that can read it:")
		fmt.Fprintln(u.errOut, "    GH_TOKEN=\"$(gh auth token)\" dexel update")
	}
}

// download streams url to dst, failing on any non-200 status.
func (u *updater) download(url, dst string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	// The assets API needs octet-stream; the browser URL is happy with */*.
	accept := "*/*"
	if u.token != "" {
		accept = "application/octet-stream"
	}
	u.setHeaders(req, accept)
	resp, err := u.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func (u *updater) setHeaders(req *http.Request, accept string) {
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", "dexel-updater")
	if u.token != "" {
		req.Header.Set("Authorization", "Bearer "+u.token)
	}
}

// runVersionCheck is the default verifyRun: run the freshly unpacked
// binary with `version` and require a clean exit and a "dexel " line.
func (u *updater) runVersionCheck(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("running `%s version`: %v (output: %s)", filepath.Base(path), err, strings.TrimSpace(string(out)))
	}
	if !strings.HasPrefix(strings.TrimSpace(string(out)), "dexel ") {
		return fmt.Errorf("`version` printed %q, which is not a dexel version line", strings.TrimSpace(string(out)))
	}
	return nil
}

// ---------------------------------------------------------- pure helpers

// resolveUpdateToken reads GH_TOKEN, then GITHUB_TOKEN — the same order
// and same reasoning as install.sh's resolve_token (GH_TOKEN wins because
// it is what `gh auth token` feeds and what a developer sets deliberately).
func resolveUpdateToken(getenv func(string) string) string {
	if t := getenv("GH_TOKEN"); t != "" {
		return t
	}
	return getenv("GITHUB_TOKEN")
}

// isDevVersion reports the un-stamped build (`go build`/`go run .` leaves
// main.version as "dev"; anything without a leading v-digit is treated
// the same, defensively).
func isDevVersion(v string) bool {
	if !strings.HasPrefix(v, "v") {
		return true
	}
	_, err := parseSemver(v)
	return err != nil
}

// semver is a parsed vMAJOR.MINOR.PATCH; pre-release/build metadata after
// the first '-' or '+' is dropped, which is enough to order releases (a
// -rc is treated as its release, deliberately conservative for a tool
// that would rather offer an update than miss one).
type semver struct{ major, minor, patch int }

func parseSemver(s string) (semver, error) {
	raw := strings.TrimSpace(s)
	raw = strings.TrimPrefix(raw, "v")
	if raw == "" {
		return semver{}, errors.New("empty version")
	}
	// drop pre-release / build metadata
	if i := strings.IndexAny(raw, "-+"); i >= 0 {
		raw = raw[:i]
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return semver{}, fmt.Errorf("%q is not MAJOR.MINOR.PATCH", s)
	}
	var out semver
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return semver{}, fmt.Errorf("%q has a non-numeric component %q", s, p)
		}
		switch i {
		case 0:
			out.major = n
		case 1:
			out.minor = n
		case 2:
			out.patch = n
		}
	}
	return out, nil
}

// compareSemver returns -1 if a<b, 0 if equal, +1 if a>b.
func compareSemver(a, b semver) int {
	switch {
	case a.major != b.major:
		return sign(a.major - b.major)
	case a.minor != b.minor:
		return sign(a.minor - b.minor)
	case a.patch != b.patch:
		return sign(a.patch - b.patch)
	default:
		return 0
	}
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	default:
		return 0
	}
}

// isNewerVersion reports whether latest is strictly newer than current.
func isNewerVersion(latest, current string) (bool, error) {
	l, err := parseSemver(latest)
	if err != nil {
		return false, fmt.Errorf("latest tag: %w", err)
	}
	c, err := parseSemver(current)
	if err != nil {
		return false, fmt.Errorf("current version: %w", err)
	}
	return compareSemver(l, c) > 0, nil
}

// sameVersion reports MAJOR.MINOR.PATCH equality (ignoring pre-release
// metadata), used only to word the "up to date" line precisely.
func sameVersion(a, b string) bool {
	pa, ea := parseSemver(a)
	pb, eb := parseSemver(b)
	if ea != nil || eb != nil {
		return a == b
	}
	return compareSemver(pa, pb) == 0
}

// platformAssetName is the per-arch archive name build-release.sh writes
// and install.sh's require_platform_asset constructs:
// dexel-<tag>-<os>-<arch>.tar.gz (.zip on windows).
func platformAssetName(goos, goarch, tag string) string {
	return fmt.Sprintf("dexel-%s-%s-%s%s", tag, goos, goarch, archiveExt(goos))
}

func archiveExt(goos string) string {
	if goos == "windows" {
		return ".zip"
	}
	return ".tar.gz"
}

// binaryBaseName is what the binary is called INSIDE the archive
// (build-release.sh: "dexel" or "dexel.exe").
func binaryBaseName(goos string) string {
	if goos == "windows" {
		return "dexel.exe"
	}
	return "dexel"
}

// findAsset looks up an asset by exact name.
func findAsset(assets []ghAsset, name string) (ghAsset, bool) {
	for _, a := range assets {
		if a.Name == name {
			return a, true
		}
	}
	return ghAsset{}, false
}

// assetURL is install.sh's asset_url: with a token, the assets API URL
// (a bearer token gets a 404 from the browser download host on a private
// repo); without one, the CDN-backed browser_download_url, falling back
// to the API URL if the browser URL is somehow absent.
func assetURL(a ghAsset, token string) string {
	if token != "" {
		return a.URL
	}
	if a.BrowserDownloadURL != "" {
		return a.BrowserDownloadURL
	}
	return a.URL
}

// sha256OfFile hashes a file, hex-encoded lower-case (the spelling
// sha256sums.txt uses).
func sha256OfFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// checksumFor finds the sha256 for name in an sha256sums.txt body. Both
// `name` and the binary-mode `*name` spelling are accepted, matching
// install.sh's verify().
func checksumFor(sumsContent, name string) (string, bool) {
	for _, line := range strings.Split(sumsContent, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 {
			continue
		}
		f := fields[1]
		if f == name || f == "*"+name {
			return fields[0], true
		}
	}
	return "", false
}

// verifyChecksum is install.sh's verify() as a pure function: the
// download's own sha256 (got) must match sha256sums.txt's line for it,
// AND — the second witness — GitHub's reported digest, if present, must
// agree with that line. Any disagreement is a hard refusal.
func verifyChecksum(archiveName, got, sumsContent, apiDigest string) error {
	want, ok := checksumFor(sumsContent, archiveName)
	if !ok {
		return fmt.Errorf("%s has no line for %s, so the download cannot be verified", checksumsAsset, archiveName)
	}
	if !strings.EqualFold(want, got) {
		return fmt.Errorf("sha256 mismatch on %s:\n    expected  %s   (from %s)\n    actual    %s", archiveName, want, checksumsAsset, got)
	}
	// GitHub's per-asset digest is "sha256:<hex>". Absent on some older
	// releases; when present it must agree with the checksum file.
	d := strings.TrimSpace(apiDigest)
	d = strings.TrimPrefix(d, "sha256:")
	if d != "" && !strings.EqualFold(d, want) {
		return fmt.Errorf("%s and GitHub disagree about %s:\n    %s says  %s\n    GitHub says   %s", checksumsAsset, archiveName, checksumsAsset, want, d)
	}
	return nil
}

// extractBinary pulls the single binary named base out of the archive at
// archivePath and writes it to dst (.tar.gz for unix/darwin, .zip for
// windows). It writes ONLY that one file, so archive path traversal is a
// non-issue: entry paths are used to MATCH, never to build the output path.
func extractBinary(archivePath, goos, base, dst string) error {
	if goos == "windows" {
		return extractBinaryFromZip(archivePath, base, dst)
	}
	return extractBinaryFromTarGz(archivePath, base, dst)
}

func extractBinaryFromTarGz(archivePath, base, dst string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if filepath.Base(hdr.Name) != base {
			continue
		}
		return writeExtracted(dst, tr)
	}
	return fmt.Errorf("no %s inside the archive", base)
}

func extractBinaryFromZip(archivePath, base, dst string) error {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = zr.Close() }()
	for _, zf := range zr.File {
		if zf.FileInfo().IsDir() {
			continue
		}
		if filepath.Base(zf.Name) != base {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		defer func() { _ = rc.Close() }()
		return writeExtracted(dst, rc)
	}
	return fmt.Errorf("no %s inside the archive", base)
}

// writeExtracted copies the matched entry over the (already created) dst
// staging file, at 0700 so the execute-and-verify step can run it.
func writeExtracted(dst string, r io.Reader) error {
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_TRUNC, 0o700)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// swapBinary is the replace dance ARCHITECTURE.md §8 and the task spell
// out: a running binary cannot be overwritten in place (ETXTBSY on Linux;
// a running .exe is locked on Windows), so the new bytes are renamed INTO
// place after the old ones are renamed ASIDE — both on one filesystem, so
// each rename is atomic. It returns the path the old binary was preserved
// at (target+".old") so the caller can remove it after a successful
// restart.
//
// Failure leaves nothing half-done: if the second rename fails, the first
// is rolled back so the target is exactly what it was.
func swapBinary(target, newBin string, mode os.FileMode) (oldPath string, err error) {
	if err := os.Chmod(newBin, mode); err != nil {
		return "", fmt.Errorf("set mode on the new binary: %w", err)
	}
	oldPath = target + ".old"
	// A leftover .old from a previous interrupted update would make the
	// move-aside fail on Windows (rename onto an existing file), so clear
	// it first. Absent is the normal case.
	if err := os.Remove(oldPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("clear a stale %s: %w", oldPath, err)
	}
	if err := os.Rename(target, oldPath); err != nil {
		return "", fmt.Errorf("move the current binary aside: %w", err)
	}
	if err := os.Rename(newBin, target); err != nil {
		// Roll back so the machine is exactly as it was.
		if rbErr := os.Rename(oldPath, target); rbErr != nil {
			return oldPath, fmt.Errorf("install the new binary failed (%v) AND rollback failed (%v) — your working binary is at %s", err, rbErr, oldPath)
		}
		return "", fmt.Errorf("install the new binary: %w (rolled back; nothing changed)", err)
	}
	return oldPath, nil
}
