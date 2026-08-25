//go:build linux

// Linux activity provider: a best-effort raw reader of /dev/input/event*
// (evdev) nodes. Counts only — no cgo needed, no keycodes retained, no app
// identity (focus detection is compositor-specific under Wayland; ADR 0009
// notes the watcher should degrade to a generic label rather than guess, so
// this provider simply never sets ActiveApp).
//
// Device SETS are not static (field failure, 2026-08-25): GNOME's
// screen-lock/power management re-enumerated the USB input devices on the
// owner's machine, so /dev/input/event* got NEW nodes while a 19h-old
// runtime still held fds to the OLD, now-dead ones. The old code opened
// every node once at Start and never looked again: each reader goroutine's
// blocking Read returned ENODEV, the goroutine returned SILENTLY, the dead
// fd stayed in the device list, and the provider went on reporting
// HonestyGlobal with a frozen keystroke counter — so IdleSeconds climbed
// forever and the engine honestly-but-wrongly accrued idle/onBreak for
// hours. Two things follow, and this file implements both:
//
//  1. Readers treat any read failure as death: drop that device, then
//     trigger a rescan (which also picks up nodes that did not exist at
//     Start — re-enumeration, hotplug, a dock coming back).
//  2. With ZERO open devices the provider is BLIND and says so
//     (Honesty() == HonestyBlind, IdleSeconds frozen), so the engine's
//     ADR 0010 gating refuses to read "no input" as "user is idle". A
//     provider that cannot see must not be counted as seeing nothing.
package activity

import (
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// Linux input_event layout (linux/input.h) on a 64-bit kernel: a 16-byte
// timeval (two 8-byte kernel longs, time64 ABI) followed by u16 type, u16
// code, s32 value — 24 bytes total. We only ever read type/code/value; the
// timestamp bytes are skipped.
const inputEventSize = 24

// evdev event type/value constants we care about (linux/input-event-codes.h).
const (
	evKey          = 0x01
	evRel          = 0x02
	keyPressValue  = 1     // EV_KEY value==1 is press; 0 is release, 2 is autorepeat
	keyCodeCeiling = 0x100 // mirrors the Rust evdev provider: standard key codes are < 0x100
)

// linuxSampleInterval: alias for the package-wide anti-mash coalescing
// window (MouseSampleInterval, defined in provider.go) — see that
// constant's doc comment for why this and the darwin/engine copies were
// hoisted into one place instead of each declaring their own 100ms.
const linuxSampleInterval = MouseSampleInterval

// inputDeviceGlob is where evdev nodes live. Injected via the scanner seam
// in tests (evdev needs root/real hardware, so the unit tests drive a
// scripted in-memory device set instead — see the scripted-devices test
// helper).
const inputDeviceGlob = "/dev/input/event*"

const (
	// defaultRescanInterval: how often the provider re-enumerates
	// /dev/input. A PERIODIC rescan (rather than inotify on /dev/input) is
	// the deliberate choice: it is one glob + a stat per node, it needs no
	// extra syscall surface or watch-descriptor bookkeeping, and it covers
	// BOTH shapes of the real failure — nodes that were replaced (the same
	// path, a new inode, which an inotify CREATE would also catch) and
	// nodes that were never openable at Start and became openable later
	// (which no single CREATE event necessarily announces). Device death
	// additionally triggers a rescan immediately, so the interval is only
	// the ceiling on how long a hotplug goes unnoticed, never the latency
	// of noticing a device died.
	defaultRescanInterval = 15 * time.Second

	// defaultMinRescanInterval floors the gap between two rescans, so a
	// node that fails its very first read forever (a genuinely broken
	// device: open succeeds, read returns EIO) degrades to one
	// open/read/close per second instead of a hot loop.
	defaultMinRescanInterval = 1 * time.Second

	// stopWaitTimeout is how long Stop() waits for the reader goroutines
	// after closing their fds. With poller-backed non-blocking fds they
	// come back in single-digit milliseconds, so 500ms is ~50x headroom
	// for a loaded machine — and it is a CEILING, not a delay: the normal
	// path returns as soon as the last reader does. It is deliberately
	// small enough that persist + Stop + http.Shutdown together stay far
	// inside the CLI's stop grace (see main.go's shutdown closure), so a
	// graceful stop can never again lose the race to SIGKILL (BUG-9).
	stopWaitTimeout = 500 * time.Millisecond

	// eagainPollInterval is the fallback cadence for a device Go's
	// netpoller refused to take (see readLoop). 25ms keeps keystroke
	// coalescing (MouseSampleInterval, 100ms) unaffected while costing
	// 40 cheap failed reads per second per device — a path we expect never
	// to run on a real evdev node.
	eagainPollInterval = 25 * time.Millisecond
)

// deviceKey identifies one open device NODE INSTANCE, not a path. Paths are
// reused across re-enumeration (event2 disappears and event2 comes back as
// a different device), which is exactly why the field failure was invisible
// to a path-keyed device list. (st_dev, st_ino) is the node instance;
// st_rdev is the character device it points at. Keying the open set by all
// three means a replaced node is never mistaken for the one we already hold
// an fd to, and re-opening a node we already read from is impossible.
type deviceKey struct {
	dev  uint64
	ino  uint64
	rdev uint64
}

func keyFromStat(st *syscall.Stat_t) deviceKey {
	return deviceKey{dev: uint64(st.Dev), ino: uint64(st.Ino), rdev: uint64(st.Rdev)}
}

// inputDevice is the read/close surface of one evdev node — the seam that
// lets tests inject devices that can be killed on demand. *os.File
// satisfies it.
type inputDevice interface {
	Read(p []byte) (int, error)
	Close() error
}

// deviceNode is one candidate node found by a scan: where it is, and which
// node instance it currently is.
type deviceNode struct {
	path string
	key  deviceKey
}

// deviceScanner is the OS seam: enumerate candidate nodes, and open one.
// Open returns the key read back FROM THE OPEN FD (via fstat), not from the
// path — between scanning and opening, a path can already point at a
// different node, and the fd is the only authority on what we actually got.
type deviceScanner interface {
	Scan() []deviceNode
	Open(path string) (inputDevice, deviceKey, error)
}

// evdevScanner is the real /dev/input scanner.
type evdevScanner struct{ glob string }

func (s evdevScanner) Scan() []deviceNode {
	paths, _ := filepath.Glob(s.glob)
	nodes := make([]deviceNode, 0, len(paths))
	for _, path := range paths {
		var st syscall.Stat_t
		if err := syscall.Stat(path, &st); err != nil {
			continue
		}
		nodes = append(nodes, deviceNode{path: path, key: keyFromStat(&st)})
	}
	return nodes
}

// Open opens one evdev node NON-BLOCKING and hands it to os.NewFile, and
// it fstats the RAW fd — never os.File.Fd(). Both halves of that sentence
// are BUG-9 (docs/plan/BUGS.md), the bug that made `dexel stop` escalate to
// SIGKILL on every single installer gate:
//
//	os.File.Fd() puts the file back into BLOCKING mode ("if the file
//	descriptor is in non-blocking mode, Fd will return the descriptor in
//	blocking mode", os.File.Fd's own doc) and takes it off Go's netpoller
//	for good. A read on such an fd is a bare read(2) syscall, and closing
//	the fd CANNOT interrupt it — internal/poll defers the real close(2)
//	until the in-flight read returns. So every reader goroutine sat in
//	read(2) until the user touched a key, Stop()'s wait sat behind them,
//	and on an idle machine (every gate: no page open, nobody typing) the
//	CLI's 5s patience ran out and killed the process. Measured: 0 of 12
//	reader goroutines returned within 2s of Close() with the Fd() variant;
//	12 of 12 returned within ~6ms without it.
//
// So: syscall.Open with O_NONBLOCK|O_CLOEXEC, fstat on that raw int fd
// (which is what the node-instance deviceKey needs and the only authority
// on what we actually opened), then os.NewFile — which sees a non-blocking
// fd, registers it with the netpoller, and thereby makes Close() (and
// SetReadDeadline, should we ever want it) interrupt a blocked Read within
// milliseconds. evdev character devices are pollable — that is how every
// select/epoll-based input reader works — so this is the supported path,
// not a trick.
//
// O_CLOEXEC is passed explicitly because dropping to syscall.Open drops
// os.OpenFile's implicit one: Go always opens files close-on-exec, and
// these fds in particular must not leak into a spawned child (`dexel
// start` spawns the runtime, `dexel restart` spawns another), which would
// hand it a dozen live input-device descriptors it never asked for.
func (s evdevScanner) Open(path string) (inputDevice, deviceKey, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, deviceKey{}, err
	}
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		_ = syscall.Close(fd)
		return nil, deviceKey{}, err
	}
	return os.NewFile(uintptr(fd), path), keyFromStat(&st), nil
}

// deviceRetry is one node instance's death history: how many times reading
// it has failed, and the earliest time it may be reopened.
type deviceRetry struct {
	failures  int
	notBefore time.Time
}

// retryBackoff is 0s, then 1s, 2s, 4s ... capped at 64s for the nth
// consecutive death of the SAME node instance. The FIRST death retries
// immediately — a transient read error on a device that is still there
// should self-heal within milliseconds — and the cap keeps a permanently
// broken node (open succeeds, every read fails) down to roughly one reopen
// and one log line per minute instead of thousands.
func retryBackoff(failures int) time.Duration {
	exp := failures - 2
	if exp < 0 {
		return 0
	}
	if exp > 6 {
		exp = 6
	}
	return time.Duration(int64(1)<<uint(exp)) * time.Second
}

// openDevice is one device this provider currently holds an fd to.
type openDevice struct {
	dev  inputDevice
	path string
	key  deviceKey

	// eagainNoted is touched ONLY by this device's own reader goroutine
	// (there is exactly one, for the life of the open fd), so it needs no
	// lock: it just keeps the netpoller-fallback warning to one line per
	// device instead of one per poll.
	eagainNoted bool
}

// LinuxProvider reads raw evdev nodes directly (no cgo, no library). If the
// process cannot open any /dev/input/event* node (the 'input' group is
// commonly required and often absent), it degrades to a blind, all-zero
// provider rather than failing to start — Start returns a descriptive error
// so the caller can surface it, but the provider still behaves safely, and
// a background rescan keeps trying so a device that shows up later is
// picked up without a restart.
type LinuxProvider struct {
	mu               sync.Mutex
	keystrokeCount   uint64
	lastKeyTick      time.Time
	lastMouseTick    time.Time
	mouseActiveUntil time.Time
	lastAnyInput     time.Time

	// devices is the CURRENT open set, keyed by node instance. Its size is
	// the whole honesty state machine: len(devices) == 0 means blind.
	devices map[deviceKey]*openDevice
	// initialCount is how many devices the Start scan managed to open —
	// kept only so the logs can say "3 of 5 still open" rather than a bare
	// number that means nothing on its own.
	initialCount int

	// retry holds the reopen backoff for node instances that have died, so a
	// genuinely broken node (open succeeds, every read fails) cannot turn
	// the death -> rescan -> reopen path into a hot loop or a log line per
	// second. A RE-ENUMERATED device gets a new key and is therefore never
	// delayed by its predecessor's backoff — recovery from the actual field
	// failure stays immediate.
	retry map[deviceKey]*deviceRetry

	stopCh   chan struct{}
	rescanCh chan struct{}
	// wg is per-START, not per-provider: Stop() abandons the wait if it
	// ever overruns (see stopWaitTimeout), and a later Start() must not
	// then be adding to a WaitGroup an abandoned Wait is still parked on.
	// A fresh WaitGroup per generation makes that structurally impossible
	// instead of merely unlikely.
	wg *sync.WaitGroup

	// Seams (defaults filled in by Start / NewLinuxProvider): the scanner
	// is the OS, logf is the log sink, and the two intervals let tests
	// drive rescans without sleeping for real seconds.
	scanner           deviceScanner
	logf              func(format string, args ...any)
	rescanInterval    time.Duration
	minRescanInterval time.Duration
}

// NewLinuxProvider constructs an unstarted provider over the real
// /dev/input nodes.
func NewLinuxProvider() *LinuxProvider {
	return &LinuxProvider{
		scanner:           evdevScanner{glob: inputDeviceGlob},
		logf:              log.Printf,
		rescanInterval:    defaultRescanInterval,
		minRescanInterval: defaultMinRescanInterval,
	}
}

// Honesty reports HonestyBlind whenever this provider holds ZERO open input
// devices — at Start (nothing openable: no 'input' group membership), after
// every device died (suspend, dock, USB re-enumeration on screen lock), and
// while stopped. It recovers to HonestyGlobal the moment a rescan reopens
// one, so a device set that comes back does NOT need a restart.
//
// PARTIAL visibility stays HonestyGlobal, deliberately: evdev exposes one
// node per device (and several per composite device), so an open set of ≥1
// after a mouse or a dock's hub went away is still a genuine system-wide
// input source — the keyboard's own node reports every keystroke regardless
// of which window is focused, which is exactly what HonestyGlobal claims.
// Degrading on ANY loss would make a routine unplug look like a permission
// failure and freeze earning for a user who is typing perfectly visibly.
// The residual gap is honest and documented: if the surviving nodes happen
// to be non-keyboard ones (a power button, a lid switch), we claim global
// while seeing no typing. Closing that needs per-device capability
// classification (EVIOCGBIT on EV_KEY) rather than a count, which is a
// bigger change than this fix and is not what the field failure was: there,
// the count went to ZERO and the honesty bit is what was missing.
func (p *LinuxProvider) Honesty() Honesty {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.devices) == 0 {
		return HonestyBlind
	}
	return HonestyGlobal
}

// Start opens every readable /dev/input/event* node, begins reading raw
// events from each on its own goroutine, and starts the rescan loop that
// keeps the open set current for the rest of the process's life. Devices
// that fail to open (most commonly EACCES — the calling user isn't in the
// 'input' group) are skipped individually; a total failure to open ANY
// device returns a descriptive error, and leaves a blind-but-live provider
// whose rescan loop can still recover.
func (p *LinuxProvider) Start() error {
	p.mu.Lock()
	if p.stopCh != nil {
		p.mu.Unlock()
		return errors.New("linux activity provider: already started")
	}
	if p.scanner == nil {
		p.scanner = evdevScanner{glob: inputDeviceGlob}
	}
	if p.logf == nil {
		p.logf = log.Printf
	}
	if p.rescanInterval <= 0 {
		p.rescanInterval = defaultRescanInterval
	}
	if p.minRescanInterval < 0 {
		p.minRescanInterval = defaultMinRescanInterval
	}
	p.devices = make(map[deviceKey]*openDevice)
	p.retry = make(map[deviceKey]*deviceRetry)
	p.stopCh = make(chan struct{})
	p.rescanCh = make(chan struct{}, 1)
	p.wg = &sync.WaitGroup{}
	p.lastAnyInput = time.Now()
	stop, rescan, wg := p.stopCh, p.rescanCh, p.wg
	scanner := p.scanner
	p.mu.Unlock()

	nodes := scanner.Scan()
	opened, openErrs := p.openMissing(scanner, nodes, stop, false)

	p.mu.Lock()
	p.initialCount = opened
	p.mu.Unlock()

	wg.Add(1)
	go p.rescanLoop(wg, stop, rescan)

	if opened == 0 {
		// BLIND, but live: the rescan loop above is already running, so a
		// device that becomes available later (udev settling, a dock, a
		// keyboard plugged in) is picked up without a restart.
		if why := errors.Join(openErrs...); why != nil {
			return fmt.Errorf(
				"no readable /dev/input devices out of %d node(s) (add your user to the 'input' group, or run with access to input devices); reporting BLIND and rescanning every %s: %w",
				len(nodes), p.rescanInterval, why,
			)
		}
		return fmt.Errorf(
			"no /dev/input event nodes exist to read (%d scanned); reporting BLIND and rescanning every %s",
			len(nodes), p.rescanInterval,
		)
	}
	p.logf("activity(linux): %d of %d input device(s) open; rescanning every %s for hotplug/re-enumeration", opened, len(nodes), p.rescanInterval)
	return nil
}

// openMissing opens every node in `nodes` that is not already in the open
// set, starting a reader goroutine per newly opened device. It never
// double-opens: the check is by node-instance key both before opening
// (cheap, from the scan's stat) and again after opening (authoritative,
// from the fd's fstat) — and an fd that loses that second check is closed
// immediately, so a repeated rescan cycle cannot leak fds.
func (p *LinuxProvider) openMissing(scanner deviceScanner, nodes []deviceNode, stop chan struct{}, logGain bool) (int, []error) {
	var opened int
	var openErrs []error

	// Prune backoff state for node instances that no longer exist: once a
	// node is gone from /dev/input it can never be reopened, so remembering
	// its death history only grows a map for the life of the process.
	p.mu.Lock()
	if len(p.retry) > 0 {
		live := make(map[deviceKey]struct{}, len(nodes))
		for _, node := range nodes {
			live[node.key] = struct{}{}
		}
		for key := range p.retry {
			if _, ok := live[key]; !ok {
				delete(p.retry, key)
			}
		}
	}
	p.mu.Unlock()

	for _, node := range nodes {
		now := time.Now()
		p.mu.Lock()
		running := p.stopCh == stop
		_, have := p.devices[node.key]
		backoff := false
		if r := p.retry[node.key]; r != nil && now.Before(r.notBefore) {
			backoff = true
		}
		p.mu.Unlock()
		if !running {
			return opened, openErrs
		}
		if have || backoff {
			continue
		}

		dev, key, err := scanner.Open(node.path)
		if err != nil {
			openErrs = append(openErrs, fmt.Errorf("%s: %w", node.path, err))
			continue
		}

		p.mu.Lock()
		if p.stopCh != stop {
			p.mu.Unlock()
			_ = dev.Close()
			return opened, openErrs
		}
		if _, dup := p.devices[key]; dup {
			// The path pointed at a node we already read from (two paths,
			// one node; or the scan's key was stale). Not ours to keep.
			p.mu.Unlock()
			_ = dev.Close()
			continue
		}
		od := &openDevice{dev: dev, path: node.path, key: key}
		p.devices[key] = od
		recovered := len(p.devices) == 1
		if recovered {
			// Honesty: the idle clock RESTARTS at recovery. The blind
			// stretch was time we could not observe, so it must not be
			// handed to the engine as observed idleness — otherwise the
			// first sighted tick after a 19-hour blind gap reports a
			// 19-hour idle and the engine claims onBreak for a period
			// nobody watched. That is the ADR 0010 lie, just delayed.
			p.lastAnyInput = time.Now()
			p.mouseActiveUntil = time.Time{}
		}
		total := len(p.devices)
		// wg.Add BEFORE releasing the lock: Stop() takes this same lock
		// before it waits, so a device added here can never be missed by
		// that wait (which would let a reader goroutine outlive Stop).
		// p.wg is read here rather than passed in because it belongs to
		// THIS start generation, and the p.stopCh == stop check above (same
		// critical section) is what proves the generation is still ours.
		wg := p.wg
		wg.Add(1)
		p.mu.Unlock()

		opened++
		go p.readLoop(wg, od, stop)

		if recovered && logGain {
			p.logf("activity(linux): RECOVERED — reopened an input device after seeing none; %d device(s) open, honesty restored to global", total)
		}
	}
	if logGain && opened > 0 {
		p.mu.Lock()
		total := len(p.devices)
		p.mu.Unlock()
		p.logf("activity(linux): rescan opened %d new input device(s); %d device(s) open", opened, total)
	}
	return opened, openErrs
}

// rescanLoop re-enumerates /dev/input periodically, and immediately
// whenever a reader reports its device died.
func (p *LinuxProvider) rescanLoop(wg *sync.WaitGroup, stop chan struct{}, rescan chan struct{}) {
	defer wg.Done()

	ticker := time.NewTicker(p.rescanInterval)
	defer ticker.Stop()

	var last time.Time
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
		case <-rescan:
		}

		if wait := p.minRescanInterval - time.Since(last); wait > 0 && !last.IsZero() {
			select {
			case <-stop:
				return
			case <-time.After(wait):
			}
		}
		last = time.Now()

		p.mu.Lock()
		running := p.stopCh == stop
		scanner := p.scanner
		p.mu.Unlock()
		if !running {
			return
		}
		p.openMissing(scanner, scanner.Scan(), stop, true)
	}
}

func (p *LinuxProvider) triggerRescan() {
	p.mu.Lock()
	ch := p.rescanCh
	p.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- struct{}{}:
	default: // a rescan is already pending; one is enough
	}
}

func (p *LinuxProvider) readLoop(wg *sync.WaitGroup, d *openDevice, stop chan struct{}) {
	defer wg.Done()
	buf := make([]byte, inputEventSize)
	for {
		select {
		case <-stop:
			return
		default:
		}
		n, err := d.dev.Read(buf)
		if errors.Is(err, syscall.EAGAIN) {
			// The fd is non-blocking (see evdevScanner.Open) and Go's
			// netpoller normally parks this Read until data arrives, so
			// EAGAIN never surfaces here. It CAN, on a kernel or a node
			// where os.NewFile's netpoller registration failed: then the
			// non-blocking read returns EAGAIN at once. That is emphatically
			// NOT device death — reporting it as such would spin
			// death -> rescan -> reopen as fast as the CPU allows — so fall
			// back to a slow explicit poll, and say once, per device, that
			// we are doing it.
			if !d.eagainNoted {
				d.eagainNoted = true
				p.logf("activity(linux): %s is not pollable by the Go runtime — falling back to polling it every %s (input is still counted; shutdown stays prompt)", d.path, eagainPollInterval)
			}
			select {
			case <-stop:
				return
			case <-time.After(eagainPollInterval):
			}
			continue
		}
		if err == nil && n <= 0 {
			// A character device returning 0 bytes with no error is the
			// EOF-shaped half of device death (a node that went away under
			// a buffered reader). Treat it as death, not as a spin.
			err = errors.New("read returned 0 bytes")
		}
		if err != nil {
			// THE field failure: on ENODEV/EOF this used to `return` and
			// leave a dead fd in the device list forever. Now the device
			// is dropped from the open set (which is what Honesty reads)
			// and a rescan is triggered, so a re-enumerated node is picked
			// up within milliseconds rather than at the next restart.
			// Transient errors self-heal through the same path: the node
			// is simply reopened by that rescan.
			p.deviceDied(d, stop, err)
			return
		}
		if n < inputEventSize {
			continue
		}
		typ := binary.LittleEndian.Uint16(buf[16:18])
		code := binary.LittleEndian.Uint16(buf[18:20])
		value := int32(binary.LittleEndian.Uint32(buf[20:24]))
		p.handleEvent(typ, code, value)
	}
}

// deviceDied drops one device from the open set, logs a single content-free
// line (counts and the /dev/input/eventN path — never a device NAME, never
// anything about what was typed), and asks for a rescan.
func (p *LinuxProvider) deviceDied(d *openDevice, stop chan struct{}, cause error) {
	p.mu.Lock()
	if p.stopCh != stop {
		// Stop() tore the whole set down and closed this fd itself; the
		// read error IS the shutdown. Nothing to report.
		p.mu.Unlock()
		return
	}
	if cur, ok := p.devices[d.key]; !ok || cur != d {
		p.mu.Unlock()
		return
	}
	delete(p.devices, d.key)
	remaining := len(p.devices)
	initial := p.initialCount
	if p.retry == nil {
		p.retry = make(map[deviceKey]*deviceRetry)
	}
	r := p.retry[d.key]
	if r == nil {
		r = &deviceRetry{}
		p.retry[d.key] = r
	}
	r.failures++
	r.notBefore = time.Now().Add(retryBackoff(r.failures))
	firstDeath := r.failures == 1
	p.mu.Unlock()

	_ = d.dev.Close()

	// A node that keeps dying is reported once, not once per flap: the
	// backoff above already bounds how often it can be retried, and a log
	// line per second was its own kind of field problem.
	if !firstDeath && remaining != 0 {
		p.triggerRescan()
		return
	}

	if remaining == 0 {
		// The state the field failure sat in for 19 hours, now said out
		// loud AND reflected in Honesty().
		p.logf("activity(linux): input device %s died (%v) — 0 of %d device(s) open: BLIND, freezing the idle clock until a rescan reopens one (ADR 0010)", d.path, cause, initial)
	} else {
		p.logf("activity(linux): input device %s died (%v) — %d of %d device(s) still open, rescanning", d.path, cause, remaining, initial)
	}
	p.triggerRescan()
}

func (p *LinuxProvider) handleEvent(typ, code uint16, value int32) {
	now := time.Now()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastAnyInput = now
	switch {
	case typ == evKey && value == keyPressValue && code < keyCodeCeiling:
		if now.Sub(p.lastKeyTick) >= linuxSampleInterval {
			p.keystrokeCount++
			p.lastKeyTick = now
		}
	case typ == evRel:
		if now.Sub(p.lastMouseTick) >= linuxSampleInterval {
			p.lastMouseTick = now
			p.mouseActiveUntil = now.Add(linuxSampleInterval)
		}
	}
}

// Stop closes every opened device and waits for its reader goroutines to
// exit — PROMPTLY, and with a hard ceiling.
//
// Prompt is the whole point (BUG-9): the fds are opened non-blocking and
// live on Go's netpoller (evdevScanner.Open explains why), so closing one
// makes its blocked Read return immediately instead of waiting for the next
// key or mouse event. On the machine this was measured on, twelve devices
// go from "still blocked 2s later" to all twelve back within ~10ms.
//
// The ceiling exists anyway, because a shutdown path must not be able to
// hang on a kernel quirk we have not met yet. If a reader has not returned
// within stopWaitTimeout, Stop says so out loud and returns: its fd is
// already closed and its device already dropped from the open set (so this
// provider is BLIND and reports nothing further), and the goroutine itself
// ends the moment its read returns — or with the process, whichever comes
// first. Nothing it can do afterwards is observable: deviceDied sees a
// different start generation and returns without touching state.
func (p *LinuxProvider) Stop() error {
	p.mu.Lock()
	stop := p.stopCh
	devices := p.devices
	wg := p.wg
	p.stopCh = nil
	p.rescanCh = nil
	p.devices = nil
	p.retry = nil
	p.wg = nil
	p.initialCount = 0
	p.mu.Unlock()

	if stop != nil {
		close(stop)
	}
	for _, d := range devices {
		_ = d.dev.Close()
	}
	if wg == nil {
		return nil
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-time.After(stopWaitTimeout):
		p.logf("activity(linux): %d input reader goroutine(s) had not returned %s after their fds were closed — abandoning the wait so shutdown stays bounded (they are blind and will end on their next read or with the process)", len(devices), stopWaitTimeout)
		return nil
	}
}

// Snapshot returns the current view of activity. A BLIND provider (zero
// open devices) reports a frozen idle clock rather than a growing one: the
// keystroke counter it already earned stays visible and monotonic (those
// presses really were observed, and zeroing it would hand the engine a
// bogus delta the moment devices come back), while IdleSeconds is 0 and
// MouseActive false, because unobserved time is not idleness. Together with
// Honesty() == HonestyBlind — which is what the engine's ADR 0010 gating
// actually reads — this is the invariant the field failure broke: idle may
// only accrue while the provider can genuinely observe input.
func (p *LinuxProvider) Snapshot() Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.devices) == 0 {
		return Snapshot{
			KeystrokeCount:       p.keystrokeCount,
			AppIdentityAvailable: false,
		}
	}
	return Snapshot{
		KeystrokeCount: p.keystrokeCount,
		MouseActive:    time.Now().Before(p.mouseActiveUntil),
		IdleSeconds:    time.Since(p.lastAnyInput).Seconds(),
		// ActiveApp / ActiveAppDisplay intentionally left "": focus
		// detection is compositor-specific (X11 vs. the many Wayland
		// compositors) and out of scope here; we report counts honestly
		// rather than guess an app identity from /proc.
		//
		// AppIdentityAvailable is therefore false, and that is now the
		// SAID part rather than the implied part: an empty ActiveApp used
		// to be this provider's way of expressing "I have no focus source"
		// and also what a working provider returns for "nothing is
		// frontmost", so downstream could not tell them apart. This
		// provider is not looking at a bare desktop; it cannot look at all.
		AppIdentityAvailable: false,
	}
}
