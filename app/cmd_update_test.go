package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jawwadzafar/dexel/app/internal/lifecycle"
)

// TestVersionCompare pins the newer/older/equal/malformed cases the whole
// "up to date vs update available" decision turns on — including THE
// FREEZE REALITY (latest == current == v0.1.0 is "not newer").
func TestVersionCompare(t *testing.T) {
	cases := []struct {
		latest, current string
		wantNewer       bool
		wantErr         bool
	}{
		{"v0.2.0", "v0.1.0", true, false},      // patch/minor bump
		{"v0.1.1", "v0.1.0", true, false},      // patch bump
		{"v1.0.0", "v0.9.9", true, false},      // major bump
		{"v0.1.0", "v0.1.0", false, false},     // THE FREEZE: equal
		{"v0.1.0", "v0.2.0", false, false},     // latest is older
		{"v0.1.0", "v0.1.1", false, false},     // latest is older (patch)
		{"0.2.0", "0.1.0", true, false},        // no leading v still parses
		{"v0.2.0-rc1", "v0.1.0", true, false},  // pre-release metadata dropped
		{"v0.1.0-rc1", "v0.1.0", false, false}, // rc of same version is not newer
		{"vX.Y.Z", "v0.1.0", false, true},      // malformed latest
		{"v0.2.0", "dev", false, true},         // malformed current (a dev build)
		{"v1.2", "v0.1.0", false, true},        // too few components
	}
	for _, tc := range cases {
		t.Run(tc.latest+"_vs_"+tc.current, func(t *testing.T) {
			got, err := isNewerVersion(tc.latest, tc.current)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("isNewerVersion(%q,%q) = %v, want an error", tc.latest, tc.current, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("isNewerVersion(%q,%q) unexpected error: %v", tc.latest, tc.current, err)
			}
			if got != tc.wantNewer {
				t.Fatalf("isNewerVersion(%q,%q) = %v, want %v", tc.latest, tc.current, got, tc.wantNewer)
			}
		})
	}
}

// TestIsDevVersion pins that an un-stamped build is recognised so the
// updater can refuse to compare it without --force.
func TestIsDevVersion(t *testing.T) {
	for _, v := range []string{"dev", "", "unknown", "v1.2", "vX.Y.Z"} {
		if !isDevVersion(v) {
			t.Errorf("isDevVersion(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"v0.1.0", "v9.9.9", "v0.2.0-rc1"} {
		if isDevVersion(v) {
			t.Errorf("isDevVersion(%q) = true, want false", v)
		}
	}
}

// TestPlatformAssetName pins the per-os/arch archive name build-release.sh
// writes and install.sh's require_platform_asset constructs.
func TestPlatformAssetName(t *testing.T) {
	cases := []struct {
		goos, goarch, want string
	}{
		{"linux", "amd64", "dexel-v0.1.0-linux-amd64.tar.gz"},
		{"linux", "arm64", "dexel-v0.1.0-linux-arm64.tar.gz"},
		{"darwin", "arm64", "dexel-v0.1.0-darwin-arm64.tar.gz"},
		{"darwin", "amd64", "dexel-v0.1.0-darwin-amd64.tar.gz"},
		{"windows", "amd64", "dexel-v0.1.0-windows-amd64.zip"},
	}
	for _, tc := range cases {
		if got := platformAssetName(tc.goos, tc.goarch, "v0.1.0"); got != tc.want {
			t.Errorf("platformAssetName(%q,%q) = %q, want %q", tc.goos, tc.goarch, got, tc.want)
		}
	}
	if got := binaryBaseName("windows"); got != "dexel.exe" {
		t.Errorf("binaryBaseName(windows) = %q", got)
	}
	if got := binaryBaseName("linux"); got != "dexel" {
		t.Errorf("binaryBaseName(linux) = %q", got)
	}
}

// TestAssetSelectionAndURL: the right asset is found by name, and the URL
// chosen depends on whether a token is present — the assets API URL with a
// token (required for a private repo), the CDN browser URL without one
// (install.sh's asset_url).
func TestAssetSelectionAndURL(t *testing.T) {
	assets := []ghAsset{
		{Name: "dexel-v0.1.0-linux-amd64.tar.gz", URL: "https://api/assets/1", BrowserDownloadURL: "https://cdn/1"},
		{Name: "sha256sums.txt", URL: "https://api/assets/2", BrowserDownloadURL: "https://cdn/2"},
	}
	a, ok := findAsset(assets, "dexel-v0.1.0-linux-amd64.tar.gz")
	if !ok {
		t.Fatal("findAsset did not find the archive")
	}
	if _, ok := findAsset(assets, "dexel-v0.1.0-windows-amd64.zip"); ok {
		t.Fatal("findAsset found an asset that is not in the release")
	}
	if got := assetURL(a, "sometoken"); got != "https://api/assets/1" {
		t.Errorf("with a token, assetURL = %q, want the API URL", got)
	}
	if got := assetURL(a, ""); got != "https://cdn/1" {
		t.Errorf("without a token, assetURL = %q, want the browser URL", got)
	}
	// Fallback: no browser URL means use the API URL even without a token.
	noBrowser := ghAsset{URL: "https://api/assets/9"}
	if got := assetURL(noBrowser, ""); got != "https://api/assets/9" {
		t.Errorf("assetURL fallback = %q, want the API URL", got)
	}
}

// TestChecksumFor pins parsing of an sha256sums.txt body, including the
// binary-mode `*name` spelling both sha256sum and shasum emit.
func TestChecksumFor(t *testing.T) {
	body := "aaa  dexel-v0.1.0-linux-amd64.tar.gz\nbbb *dexel-v0.1.0-darwin-arm64.tar.gz\n"
	if got, ok := checksumFor(body, "dexel-v0.1.0-linux-amd64.tar.gz"); !ok || got != "aaa" {
		t.Errorf("plain-mode line: got %q,%v", got, ok)
	}
	if got, ok := checksumFor(body, "dexel-v0.1.0-darwin-arm64.tar.gz"); !ok || got != "bbb" {
		t.Errorf("binary-mode (*name) line: got %q,%v", got, ok)
	}
	if _, ok := checksumFor(body, "not-in-file.tar.gz"); ok {
		t.Error("checksumFor found a name not in the file")
	}
}

// TestVerifyChecksum is the two-witness bar as a pure function: pass when
// the download's hash matches sha256sums.txt AND GitHub's digest agrees;
// REFUSE on either mismatch, and refuse when there is no line at all.
func TestVerifyChecksum(t *testing.T) {
	const name = "dexel-v0.1.0-linux-amd64.tar.gz"
	const good = "1111111111111111111111111111111111111111111111111111111111111111"
	sums := good + "  " + name + "\n"

	// PASS: file hash matches, and GitHub's digest matches.
	if err := verifyChecksum(name, good, sums, "sha256:"+good); err != nil {
		t.Fatalf("happy path should verify: %v", err)
	}
	// PASS: GitHub digest absent (older release) — still verifies on the
	// one witness the checksum file provides.
	if err := verifyChecksum(name, good, sums, ""); err != nil {
		t.Fatalf("absent API digest should still verify on the checksum file: %v", err)
	}
	// REFUSE: the downloaded bytes hash to something else.
	if err := verifyChecksum(name, "2222222222222222222222222222222222222222222222222222222222222222", sums, "sha256:"+good); err == nil {
		t.Fatal("a sha256 mismatch must be refused")
	}
	// REFUSE: the checksum file and GitHub disagree (two-witness failure).
	if err := verifyChecksum(name, good, sums, "sha256:3333333333333333333333333333333333333333333333333333333333333333"); err == nil {
		t.Fatal("a checksum-file vs GitHub-digest disagreement must be refused")
	}
	// REFUSE: no line for this archive at all.
	if err := verifyChecksum(name, good, "cccc  some-other-file\n", ""); err == nil {
		t.Fatal("a missing checksum line must be refused")
	}
}

// TestSwapBinary is the rename dance: the old binary ends up preserved as
// target+".old", the target ends up as the NEW bytes, and the target's
// original mode is preserved on the newly installed binary.
func TestSwapBinary(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "dexel")
	if err := os.WriteFile(target, []byte("OLD-BINARY"), 0o755); err != nil {
		t.Fatal(err)
	}
	newBin := filepath.Join(dir, ".dexel.new-xyz")
	if err := os.WriteFile(newBin, []byte("NEW-BINARY"), 0o600); err != nil {
		t.Fatal(err)
	}

	oldPath, err := swapBinary(target, newBin, 0o755)
	if err != nil {
		t.Fatalf("swapBinary: %v", err)
	}

	// target is now the NEW bytes...
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "NEW-BINARY" {
		t.Fatalf("target after swap = %q (err %v), want NEW-BINARY", got, err)
	}
	// ...at the preserved mode (0755, not the staging file's 0600)...
	fi, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Fatalf("target mode = %v, want 0755 (the original binary's mode)", fi.Mode().Perm())
	}
	// ...and the OLD bytes are preserved at target+".old".
	if oldPath != target+".old" {
		t.Fatalf("oldPath = %q, want %q", oldPath, target+".old")
	}
	old, err := os.ReadFile(oldPath)
	if err != nil || string(old) != "OLD-BINARY" {
		t.Fatalf(".old = %q (err %v), want OLD-BINARY", old, err)
	}
	// The staging file is gone (it was renamed into place).
	if _, err := os.Stat(newBin); !os.IsNotExist(err) {
		t.Fatalf("staging file still exists after swap")
	}
}

// TestExtractBinaryFromTarGz pulls exactly the binary out of an archive
// shaped the way build-release.sh writes it (a top-level dir holding the
// binary plus paperwork).
func TestExtractBinaryFromTarGz(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "a.tar.gz")
	writeTarGzFixture(t, archive, "dexel-v0.9.9-linux-amd64", map[string]string{
		"dexel":     "BINARY-BYTES",
		"README.md": "not the binary",
		"LICENSE":   "also not it",
	})
	dst := filepath.Join(dir, ".dexel.new")
	if err := os.WriteFile(dst, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := extractBinary(archive, "linux", "dexel", dst); err != nil {
		t.Fatalf("extractBinary: %v", err)
	}
	got, _ := os.ReadFile(dst)
	if string(got) != "BINARY-BYTES" {
		t.Fatalf("extracted %q, want BINARY-BYTES", got)
	}

	// A binary that is not in the archive is an error, not a silent empty
	// file.
	if err := extractBinary(archive, "linux", "dexel.exe", dst); err == nil {
		t.Fatal("extracting a missing binary name should error")
	}
}

// --- full-pipeline tests against an httptest server (no network) --------

// TestUpdateUpToDate is THE FREEZE REALITY end to end: the live-shaped
// feed says v0.1.0, the binary is v0.1.0, and `update` reports "up to
// date" and exits 0 without touching anything.
func TestUpdateUpToDate(t *testing.T) {
	srv := newReleaseServer(t, "v0.1.0", nil)
	defer srv.Close()

	var out bytes.Buffer
	u := newTestUpdater(t, srv.URL, "v0.1.0", &out)
	env := newTestEnv(t, &out)

	if code := u.run(env, false, true, false); code != updateOK {
		t.Fatalf("run() = %d, want %d (up to date)", code, updateOK)
	}
	if !strings.Contains(out.String(), "already up to date") {
		t.Fatalf("output missing 'already up to date':\n%s", out.String())
	}
}

// TestUpdateCheckReportsAvailable: --check on a newer feed reports the
// available version and changes nothing (exit 0).
func TestUpdateCheckReportsAvailable(t *testing.T) {
	srv := newReleaseServer(t, "v0.2.0", nil)
	defer srv.Close()

	var out bytes.Buffer
	u := newTestUpdater(t, srv.URL, "v0.1.0", &out)
	env := newTestEnv(t, &out)

	if code := u.run(env, true /*check*/, true, false); code != updateOK {
		t.Fatalf("run(--check) = %d, want %d", code, updateOK)
	}
	if !strings.Contains(out.String(), "update is available: v0.2.0") {
		t.Fatalf("output missing availability line:\n%s", out.String())
	}
	// --check must NOT have replaced the binary.
	if got, _ := os.ReadFile(u.self); string(got) != "ORIGINAL" {
		t.Fatalf("--check replaced the binary (%q)", got)
	}
}

// TestUpdateHappyPath: a newer release is downloaded, verified, unpacked
// and installed; the target binary is replaced with the new bytes, the
// .old is cleaned up, and (no runtime running) nothing is restarted.
func TestUpdateHappyPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture archive is a .tar.gz; the windows path uses .zip")
	}
	archive, sum := buildArchiveFixture(t, "dexel-v0.2.0-"+goosArch()+".tar.gz", "NEW-DEXEL")
	srv := newReleaseServer(t, "v0.2.0", map[string]releaseAsset{
		platformAssetName(hostGOOS(), hostGOARCH(), "v0.2.0"): {bytes: archive, digest: "sha256:" + sum},
		checksumsAsset: {bytes: []byte(sum + "  " + platformAssetName(hostGOOS(), hostGOARCH(), "v0.2.0") + "\n")},
	})
	defer srv.Close()

	var out bytes.Buffer
	u := newTestUpdater(t, srv.URL, "v0.1.0", &out)
	u.verifyRun = func(string) error { return nil } // don't exec the fixture
	env := newTestEnv(t, &out)

	if code := u.run(env, false, true, false); code != updateOK {
		t.Fatalf("run() = %d, want %d\n%s", code, updateOK, out.String())
	}
	got, _ := os.ReadFile(u.self)
	if string(got) != "NEW-DEXEL" {
		t.Fatalf("target after update = %q, want NEW-DEXEL", got)
	}
	// The .old was cleaned up on success.
	if _, err := os.Stat(u.self + ".old"); !os.IsNotExist(err) {
		t.Fatalf(".old was not cleaned up after a successful update")
	}
	// No .dexel.new-* droppings left in the bin dir.
	if leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(u.self), ".dexel.new-*")); len(leftovers) > 0 {
		t.Fatalf("staging files left behind: %v", leftovers)
	}
}

// TestUpdateChecksumMismatchReplacesNothing is the injection the task
// requires: a bad sha256sums.txt must make the update refuse with the
// checksum exit code and leave the target binary byte-for-byte unchanged.
func TestUpdateChecksumMismatchReplacesNothing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture archive is a .tar.gz; the windows path uses .zip")
	}
	name := platformAssetName(hostGOOS(), hostGOARCH(), "v0.2.0")
	archive, _ := buildArchiveFixture(t, name, "NEW-DEXEL")
	badSum := "deadbeef00000000000000000000000000000000000000000000000000000000"
	srv := newReleaseServer(t, "v0.2.0", map[string]releaseAsset{
		name:           {bytes: archive, digest: "sha256:" + badSum},
		checksumsAsset: {bytes: []byte(badSum + "  " + name + "\n")},
	})
	defer srv.Close()

	var out bytes.Buffer
	u := newTestUpdater(t, srv.URL, "v0.1.0", &out)
	env := newTestEnv(t, &out)

	if code := u.run(env, false, true, false); code != updateChecksum {
		t.Fatalf("run() = %d, want %d (checksum)\n%s", code, updateChecksum, out.String())
	}
	if got, _ := os.ReadFile(u.self); string(got) != "ORIGINAL" {
		t.Fatalf("a checksum mismatch REPLACED the binary (%q) — the two-witness bar must refuse before the swap", got)
	}
	if _, err := os.Stat(u.self + ".old"); !os.IsNotExist(err) {
		t.Fatalf(".old exists — the swap ran despite a checksum mismatch")
	}
	if leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(u.self), ".dexel.new-*")); len(leftovers) > 0 {
		t.Fatalf("staging files left behind after a refused update: %v", leftovers)
	}
}

// TestUpdateNoBuildForPlatform: a release that ships no archive for this
// os/arch fails with the no-build code and replaces nothing.
func TestUpdateNoBuildForPlatform(t *testing.T) {
	// A release whose only asset is for a platform this host is not.
	srv := newReleaseServer(t, "v0.2.0", map[string]releaseAsset{
		"dexel-v0.2.0-plan9-mips.tar.gz": {bytes: []byte("x")},
		checksumsAsset:                   {bytes: []byte("x  dexel-v0.2.0-plan9-mips.tar.gz\n")},
	})
	defer srv.Close()

	var out bytes.Buffer
	u := newTestUpdater(t, srv.URL, "v0.1.0", &out)
	env := newTestEnv(t, &out)

	if code := u.run(env, false, true, false); code != updateNoBuild {
		t.Fatalf("run() = %d, want %d (no build)\n%s", code, updateNoBuild, out.String())
	}
	if got, _ := os.ReadFile(u.self); string(got) != "ORIGINAL" {
		t.Fatalf("binary replaced despite no build for this platform")
	}
}

// TestUpdatePrivateRepoDegradesGracefully: a 404 (the private-repo/no-token
// reality) is a clear message and a non-zero-but-not-crash exit, and it
// names the fix (make it public, or set GH_TOKEN).
func TestUpdatePrivateRepoDegradesGracefully(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Not Found", http.StatusNotFound)
	}))
	defer srv.Close()

	var out bytes.Buffer
	u := newTestUpdater(t, srv.URL, "v0.1.0", &out)
	u.token = "" // the no-token private-repo path
	env := newTestEnv(t, &out)

	if code := u.run(env, false, true, false); code != updateNetwork {
		t.Fatalf("run() = %d, want %d (network/feed)\n%s", code, updateNetwork, out.String())
	}
	s := out.String()
	if !strings.Contains(s, "release feed") || !strings.Contains(s, "GH_TOKEN") {
		t.Fatalf("private-repo message did not name the feed and the token fix:\n%s", s)
	}
	if got, _ := os.ReadFile(u.self); string(got) != "ORIGINAL" {
		t.Fatalf("binary replaced on a failed resolve")
	}
}

// TestUpdateDevBuildRefusedWithoutForce: a `dev` build has no version to
// compare, so it is refused (exit 0, a message) unless --force.
func TestUpdateDevBuildRefusedWithoutForce(t *testing.T) {
	srv := newReleaseServer(t, "v0.2.0", nil)
	defer srv.Close()

	var out bytes.Buffer
	u := newTestUpdater(t, srv.URL, "dev", &out)
	env := newTestEnv(t, &out)

	if code := u.run(env, false, true, false /*no force*/); code != updateOK {
		t.Fatalf("run() = %d, want %d", code, updateOK)
	}
	if !strings.Contains(out.String(), "development build") {
		t.Fatalf("dev build message missing:\n%s", out.String())
	}
}

// TestResolveUpdateToken pins the GH_TOKEN-then-GITHUB_TOKEN order.
func TestResolveUpdateToken(t *testing.T) {
	env := map[string]string{"GH_TOKEN": "gh", "GITHUB_TOKEN": "github"}
	get := func(k string) string { return env[k] }
	if got := resolveUpdateToken(get); got != "gh" {
		t.Fatalf("GH_TOKEN should win, got %q", got)
	}
	delete(env, "GH_TOKEN")
	if got := resolveUpdateToken(get); got != "github" {
		t.Fatalf("GITHUB_TOKEN fallback, got %q", got)
	}
	delete(env, "GITHUB_TOKEN")
	if got := resolveUpdateToken(get); got != "" {
		t.Fatalf("no token should be empty, got %q", got)
	}
}

// ------------------------------------------------------------ test helpers

// The pipeline tests run on the host's own os/arch, since the fixture
// archive holds a binary named for that platform and the pipeline resolves
// the same name.
func hostGOOS() string   { return runtime.GOOS }
func hostGOARCH() string { return runtime.GOARCH }
func goosArch() string   { return hostGOOS() + "-" + hostGOARCH() }

// newTestUpdater builds an updater pointed at a fake feed, with a target
// binary file containing "ORIGINAL" so a wrongful replacement is visible.
func newTestUpdater(t *testing.T, apiBase, current string, out *bytes.Buffer) *updater {
	t.Helper()
	dir := t.TempDir()
	self := filepath.Join(dir, "dexel")
	if err := os.WriteFile(self, []byte("ORIGINAL"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &updater{
		repo:      updateRepo,
		apiBase:   apiBase,
		current:   current,
		goos:      hostGOOS(),
		goarch:    hostGOARCH(),
		token:     "test-token",
		client:    &http.Client{},
		out:       out,
		errOut:    out,
		self:      self,
		verifyRun: func(string) error { return nil },
	}
}

func newTestEnv(t *testing.T, out *bytes.Buffer) *cliEnv {
	t.Helper()
	return &cliEnv{
		stateDir: t.TempDir(),
		out:      out,
		errOut:   out,
		client:   lifecycle.ProbeClient(lifecycle.DefaultProbeTimeout),
	}
}

type releaseAsset struct {
	bytes  []byte
	digest string
}

// newReleaseServer serves a GitHub-shaped /releases/latest plus every
// asset's bytes, so the resolve → download → verify pipeline runs with no
// network. assets may be nil for the metadata-only tests (up to date,
// --check, dev-build, private-repo).
func newReleaseServer(t *testing.T, tag string, assets map[string]releaseAsset) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/repos/"+updateRepo+"/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		rel := ghRelease{TagName: tag}
		for name, a := range assets {
			rel.Assets = append(rel.Assets, ghAsset{
				Name:               name,
				URL:                srv.URL + "/asset/" + name,
				BrowserDownloadURL: srv.URL + "/download/" + name,
				Digest:             a.digest,
				Size:               int64(len(a.bytes)),
			})
		}
		_ = json.NewEncoder(w).Encode(rel)
	})
	serveAsset := func(w http.ResponseWriter, r *http.Request, name string) {
		a, ok := assets[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(a.bytes)
	}
	mux.HandleFunc("/asset/", func(w http.ResponseWriter, r *http.Request) {
		serveAsset(w, r, strings.TrimPrefix(r.URL.Path, "/asset/"))
	})
	mux.HandleFunc("/download/", func(w http.ResponseWriter, r *http.Request) {
		serveAsset(w, r, strings.TrimPrefix(r.URL.Path, "/download/"))
	})
	srv = httptest.NewServer(mux)
	return srv
}

// buildArchiveFixture makes a build-release.sh-shaped .tar.gz (a top-level
// dir holding the binary) and returns its bytes and hex sha256.
func buildArchiveFixture(t *testing.T, archiveName, binContent string) ([]byte, string) {
	t.Helper()
	top := strings.TrimSuffix(archiveName, ".tar.gz")
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	files := map[string]string{
		binaryBaseName(hostGOOS()): binContent,
		"README.md":                "paperwork",
		"LICENSE":                  "license",
	}
	for name, content := range files {
		hdr := &tar.Header{Name: top + "/" + name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:])
}

// writeTarGzFixture writes a .tar.gz with a top-level dir and the given
// files, for the extract unit test.
func writeTarGzFixture(t *testing.T, path, top string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		hdr := &tar.Header{Name: top + "/" + name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
}
