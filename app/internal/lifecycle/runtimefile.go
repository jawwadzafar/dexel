// Package lifecycle owns the three mechanisms that let one dexel CLI
// invocation find, talk to, and refuse to duplicate a dexel RUNTIME
// process (dev_docs/production-runtime/ARCHITECTURE.md Decisions 6 and 7,
// dev_docs/production-runtime/PLATFORM_NOTES.md §5):
//
//	runtime.json  the discovery file — {pid, port, token, ...} at 0600
//	runtime.lock  the OS lock that makes "one runtime per state dir" true
//	runtime.log   the log file `dexel start` points the detached child at
//
// It is deliberately a separate package from `main` (ARCHITECTURE.md
// Decision 2: "genuinely reusable, non-main logic goes in new
// app/internal/* packages") and deliberately parameter-injected: every
// function here takes the directory or path it operates on, and never
// consults paths.StateDir() itself. That is what lets the whole package
// be tested against plain temp directories, and what keeps
// app/internal/paths the only place in dexel that knows a location.
//
// The rule that runs through all of it (ARCHITECTURE.md Decision 6):
// **staleness is resolved by asking, never by trusting the file**. A pid
// in a file proves nothing — pids are recycled — so Discover below always
// finishes with a live HTTP round-trip, and treats a file it cannot
// confirm as no runtime at all.
package lifecycle

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// RuntimeSchema is runtime.json's own schema version
// (ARCHITECTURE.md Decision 6's `"schema": 1`). It is NOT state.db's
// CurrentSchema and must never be confused with it: this file is
// ephemeral process metadata that is deleted on every clean exit, and
// nothing in it is user data. A reader seeing a HIGHER schema than it
// knows refuses to interpret the file (ReadRuntime below) rather than
// guessing, exactly as ARCHITECTURE.md Decision 19 has manifest
// consumers do.
const RuntimeSchema = 1

// RuntimeFileName / LockFileName / LogFileName are the fixed basenames
// from PLATFORM_NOTES.md §1's table. They live here, next to the code
// that reads and writes them, and app/internal/paths joins them onto
// StateDir/LogDir — so the NAME has one home and the DIRECTORY has one
// home, and neither is spelled twice.
const (
	RuntimeFileName = "runtime.json"
	LockFileName    = "runtime.lock"
	LogFileName     = "runtime.log"
)

// TokenHeader is the capability-token header the CLI sends and, from PR-4
// on, the lifecycle endpoints require (ARCHITECTURE.md Decision 8). It is
// a CUSTOM header on purpose: a browser cannot send one on a simple
// cross-origin request, so it forces a preflight that the runtime answers
// for nothing under /api/lifecycle/*, which is what closes the
// drive-by-CSRF hole a bare POST /api/lifecycle/stop would open.
const TokenHeader = "X-Dexel-Token"

// Runtime is runtime.json's exact content (ARCHITECTURE.md Decision 6).
// Written by the runtime process immediately after its listener is bound;
// read by every CLI verb that needs to find it.
//
// Token is 32 bytes of crypto/rand, hex-encoded, regenerated on EVERY
// start. It is never logged, never sent to a browser client, and never
// persisted into state.db or config.json — the 0600 file is its only
// home, which is why WriteRuntime refuses to widen that mode.
type Runtime struct {
	Schema    int    `json:"schema"`
	Pid       int    `json:"pid"`
	Port      int    `json:"port"`
	URL       string `json:"url"`
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	StartedAt string `json:"startedAt"`
	Token     string `json:"token"`
}

// ErrNoRuntime means "there is no runtime.json here" — the honest,
// expected answer on a machine where dexel has never been started, and
// the answer Discover collapses every stale-file case into.
var ErrNoRuntime = errors.New("no runtime.json")

// NewToken mints a fresh capability token: 32 bytes of crypto/rand, hex.
// crypto/rand.Read is documented never to return a short read without an
// error, so a nil error means 64 hex characters.
func NewToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate runtime token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// RuntimePath / LockPath / LogPath join the fixed basenames above onto a
// caller-supplied directory. They exist so callers inside this package
// (and its tests) never hand-spell a filename.
func RuntimePath(stateDir string) string { return filepath.Join(stateDir, RuntimeFileName) }
func LockPath(stateDir string) string    { return filepath.Join(stateDir, LockFileName) }
func LogPath(logDir string) string       { return filepath.Join(logDir, LogFileName) }

// WriteRuntime writes runtime.json atomically at mode 0600.
//
// Atomically, by the same tmp-write + fsync + rename recipe
// app/internal/store's writeFileAtomically uses (ARCHITECTURE.md
// Decision 6 names it explicitly), because `dexel start` is polling for
// this file from another process the whole time it is being written: a
// plain os.WriteFile would let that poller observe a half-written,
// unparseable JSON object and conclude the runtime had failed to start.
// The rename makes the file appear complete or not at all.
//
// At 0600 because it carries the capability token (Decision 8). The mode
// is applied to the TEMP file before the rename, so the file is never
// group/world readable for even an instant — chmod-ing after the rename
// would leave exactly that window open.
func WriteRuntime(stateDir string, r Runtime) error {
	if r.Schema == 0 {
		r.Schema = RuntimeSchema
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal runtime.json: %w", err)
	}
	data = append(data, '\n')

	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("create state dir %s: %w", stateDir, err)
	}
	path := RuntimePath(stateDir)
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create temp runtime.json: %w", err)
	}
	defer func() { _ = os.Remove(tmp) }() // no-op once the rename succeeds
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("write temp runtime.json: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("sync temp runtime.json: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close temp runtime.json: %w", err)
	}
	// os.OpenFile's mode is masked by umask, so a restrictive-looking
	// 0600 can still come out as 0600&^umask — never wider than 0600, but
	// this chmod makes the intent unconditional rather than dependent on
	// the ambient umask, and it runs while the file is still the private
	// temp name.
	if err := os.Chmod(tmp, 0o600); err != nil {
		return fmt.Errorf("chmod temp runtime.json: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename runtime.json into place: %w", err)
	}
	return nil
}

// ReadRuntime reads and validates runtime.json. A missing file is
// ErrNoRuntime (the expected "not running" answer, not a failure); a file
// that exists but is unparseable, carries a future schema, or names no
// pid/port is an error — the caller's job is then to delete it, which is
// what Discover does.
func ReadRuntime(stateDir string) (Runtime, error) {
	var r Runtime
	data, err := os.ReadFile(RuntimePath(stateDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return r, ErrNoRuntime
		}
		return r, fmt.Errorf("read runtime.json: %w", err)
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return r, fmt.Errorf("parse runtime.json: %w", err)
	}
	if r.Schema > RuntimeSchema {
		return r, fmt.Errorf("runtime.json schema %d is newer than this dexel understands (%d)", r.Schema, RuntimeSchema)
	}
	if r.Pid <= 0 || r.Port <= 0 {
		return r, fmt.Errorf("runtime.json is incomplete (pid=%d port=%d)", r.Pid, r.Port)
	}
	if r.URL == "" {
		r.URL = fmt.Sprintf("http://127.0.0.1:%d", r.Port)
	}
	return r, nil
}

// RemoveRuntime deletes runtime.json. A file that is already gone is
// success: both the clean-exit path and the stale-file cleanup path call
// this, and they can legitimately race with each other.
func RemoveRuntime(stateDir string) error {
	if err := os.Remove(RuntimePath(stateDir)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove runtime.json: %w", err)
	}
	return nil
}

// RuntimeStatus is GET /api/lifecycle/status's response body
// (app/lifecycle_handlers.go's lifecycleStatusResponse) — the subset of
// it this package needs to confirm a runtime is alive and to perform
// Decision 6's pid-equality check.
type RuntimeStatus struct {
	Running       bool   `json:"running"`
	Pid           int    `json:"pid"`
	Port          int    `json:"port"`
	URL           string `json:"url"`
	Version       string `json:"version"`
	Commit        string `json:"commit"`
	StartedAt     string `json:"startedAt"`
	UptimeSeconds int64  `json:"uptimeSeconds"`
	StatePath     string `json:"statePath"`
	LogPath       string `json:"logPath"`
}

// probeLimit caps how much of a probe response is read. /api/lifecycle/
// status is a fixed, small object; anything vastly larger is not dexel,
// and this keeps a hostile or confused listener on that port from
// streaming unbounded bytes into a CLI that only wanted a yes/no.
const probeLimit = 64 << 10

// Probe performs THE round-trip that Decision 6 makes the sole authority
// on "is there a runtime to talk to": GET <url>/api/lifecycle/status,
// carrying runtime.json's token in X-Dexel-Token (PR-4 — MIGRATION_PLAN.md
// §PR-4, ARCHITECTURE.md Decision 8 — enforces this token server-side;
// PR-3 already sent it, unauthenticated, to the earlier /api/health
// probe this replaces).
//
// Beyond a live 200 with a well-formed body, this is what makes the
// round-trip the "last defence against pid reuse" Decision 6 promises: the
// endpoint reports the answering process's OWN pid, and Probe requires it
// to equal r.Pid (runtime.json's copy). A process that merely inherited
// this port after the real dexel died cannot pass that check even if it
// happens to answer something JSON-shaped on the same port; only a dexel
// that is genuinely still the process named in runtime.json can.
func Probe(client *http.Client, r Runtime) (RuntimeStatus, error) {
	var s RuntimeStatus
	if r.URL == "" {
		return s, errors.New("probe: runtime has no url")
	}
	req, err := http.NewRequest(http.MethodGet, r.URL+"/api/lifecycle/status", nil)
	if err != nil {
		return s, fmt.Errorf("probe %s: %w", r.URL, err)
	}
	req.Header.Set(TokenHeader, r.Token)
	resp, err := client.Do(req)
	if err != nil {
		return s, fmt.Errorf("probe %s: %w", r.URL, err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, probeLimit))
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		return s, fmt.Errorf("probe %s: http %d", r.URL, resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, probeLimit)).Decode(&s); err != nil {
		return s, fmt.Errorf("probe %s: decode lifecycle status: %w", r.URL, err)
	}
	// The is-this-dexel test. Version is unconditionally set by
	// lifecycleStatusHandler, so an empty one means whatever answered
	// is not a dexel lifecycle status object at all.
	if s.Version == "" {
		return s, fmt.Errorf("probe %s: response is not a dexel /api/lifecycle/status object", r.URL)
	}
	// The pid-equality check itself: runtime.json's own pid must match
	// what the live process just claimed as its own. A mismatch means
	// runtime.json outlived the process it named — pid reuse, or a
	// foreign listener on a recycled port — and the caller (Query/
	// Discover) must treat this exactly like any other failed probe.
	if s.Pid != r.Pid {
		return s, fmt.Errorf("probe %s: pid mismatch (runtime.json says %d, live status says %d) — stale runtime.json", r.URL, r.Pid, s.Pid)
	}
	return s, nil
}

// RequestStop sends POST /api/lifecycle/stop to a live runtime — PR-4's
// endpoint-primary graceful stop (MIGRATION_PLAN.md §PR-4: "the CLI calls
// the endpoints instead of poking signals"; PLATFORM_NOTES.md §2: "the
// lifecycle endpoint IS the graceful path... fallback is [a signal]").
//
// A non-2xx response, a transport error, or an unreachable server are all
// reported as an error so the caller can fall back to a signal — this
// function itself never signals anything, and never assumes the runtime
// has actually exited by the time it returns 202: it only confirms the
// request was ACCEPTED, exactly as ARCHITECTURE.md §3 specifies
// ("stop -> 202, then the runtime does exactly what the existing
// case <-sigCh: branch does"). The caller (cmd_lifecycle.go's stop()) is
// the one that waits for the process to actually disappear.
func RequestStop(client *http.Client, r Runtime) error {
	if r.URL == "" {
		return errors.New("request stop: runtime has no url")
	}
	req, err := http.NewRequest(http.MethodPost, r.URL+"/api/lifecycle/stop", nil)
	if err != nil {
		return fmt.Errorf("request stop %s: %w", r.URL, err)
	}
	req.Header.Set(TokenHeader, r.Token)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request stop %s: %w", r.URL, err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, probeLimit))
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("request stop %s: http %d", r.URL, resp.StatusCode)
	}
	return nil
}

// DefaultProbeTimeout bounds one round-trip. Generous for loopback (where
// a live server answers in microseconds) and short enough that `dexel
// status` cannot appear to hang: a dead port answers with connection
// refused immediately, and this timeout only matters for the pathological
// case of a port that accepts and then never replies.
const DefaultProbeTimeout = 2 * time.Second

// ProbeClient builds the http.Client the CLI probes with. No redirects
// are followed and no proxy is consulted: the target is a loopback URL we
// wrote ourselves, and honouring $http_proxy for it (which
// http.DefaultTransport would) is a documented way to make a local CLI
// mysteriously fail inside a corporate shell.
func ProbeClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = DefaultProbeTimeout
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("probe: unexpected redirect from a loopback runtime")
		},
	}
}

// Status is Query/Discover's answer: either a confirmed-live runtime, or
// the honest "nothing is running here" with the reason recorded.
type Status struct {
	Running bool
	Runtime Runtime
	// Live is the /api/lifecycle/status response Probe confirmed against
	// (PR-4) — the runtime's own live answer, as opposed to Runtime's
	// on-disk copy from runtime.json, which after a `dexel update` can
	// legitimately differ from it until the next restart.
	Live RuntimeStatus
	// HasFile reports that a runtime.json was present, whether or not it
	// could be parsed or confirmed. It is what lets Discover know there
	// is something to clean up, without re-stat-ing the file.
	HasFile bool
	// Cleaned reports that a runtime.json was found, could not be
	// confirmed live, and was therefore DELETED by Discover
	// (ARCHITECTURE.md Decision 6: "Connection refused, a timeout, or a
	// pid mismatch ⇒ stale ⇒ remove the file and proceed as 'not
	// running'"). Surfaced so `dexel status` can say it out loud instead
	// of silently mutating the state dir. Always false from Query.
	Cleaned bool
	// Reason is why Running is false, in human words. Never an error
	// value, because "not running" is not a failure of Query/Discover.
	Reason string
}

// Query answers "is a dexel running in this state dir" WITHOUT touching
// the filesystem: read runtime.json, round-trip it, report.
//
// This read-only variant exists because of a race that Discover cannot
// safely be used for. `dexel start` polls for readiness while the child
// it just spawned is mid-startup, and the child writes runtime.json
// immediately after net.Listen — BEFORE http.Server.Serve is running
// (app/main.go's startup order, which ARCHITECTURE.md Decision 6 pins
// deliberately: the file must appear as early as possible). A probe that
// lands in that window can fail, and a cleaning Discover would then
// DELETE the file the child had just correctly written, leaving the
// poller waiting for a file nobody will write again and turning a healthy
// start into a 10-second timeout. Polling must therefore observe without
// mutating; only a caller who has decided nothing is starting may clean
// up.
func Query(stateDir string, client *http.Client) (Status, error) {
	r, err := ReadRuntime(stateDir)
	if errors.Is(err, ErrNoRuntime) {
		return Status{Reason: "no runtime.json in " + stateDir}, nil
	}
	if err != nil {
		// A corrupt/incomplete/future runtime.json is itself proof of
		// staleness: no live runtime ever leaves one behind, because
		// WriteRuntime renames a COMPLETE file into place.
		return Status{HasFile: true, Reason: err.Error()}, nil
	}
	s, probeErr := Probe(client, r)
	if probeErr != nil {
		return Status{HasFile: true, Runtime: r, Reason: probeErr.Error()}, nil
	}
	return Status{Running: true, HasFile: true, Runtime: r, Live: s}, nil
}

// Discover is Query plus ARCHITECTURE.md Decision 6's cleanup half: a
// runtime.json that cannot be confirmed live is deleted, and the caller
// proceeds as "not running". Every CLI verb that makes a DECISION (start,
// stop, status, open) uses this; only the readiness poll inside `start`
// uses the read-only Query, for the reason its doc comment gives.
//
// It returns an error only when something is genuinely wrong with the
// machine (an undeletable file) — never for the ordinary "not running"
// outcomes, which are Status.Running == false with a Reason. That split is
// what keeps callers from having to distinguish "no dexel" from "broken
// box" by string-matching an error.
func Discover(stateDir string, client *http.Client) (Status, error) {
	st, err := Query(stateDir, client)
	if err != nil {
		return st, err
	}
	if !st.Running && st.HasFile {
		if rmErr := RemoveRuntime(stateDir); rmErr != nil {
			return Status{}, rmErr
		}
		st.Cleaned = true
	}
	return st, nil
}
