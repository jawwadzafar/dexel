//go:build linux

package activity

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// This file is BUG-9's regression suite: `dexel stop` escalated to SIGKILL
// on EVERY installer gate because LinuxProvider.Stop could not interrupt
// its reader goroutines. Two independent things had to be true for that,
// and each gets its own test here:
//
//  1. The fds the real scanner opens must be non-blocking and registered
//     with Go's netpoller, so Close() interrupts a blocked Read at once.
//     They were not: fstat-ing through os.File.Fd() silently put every fd
//     back into blocking mode (see evdevScanner.Open's comment), and a
//     blocking read(2) on an idle machine returns only when the user
//     touches a key — which, on an idle gate machine, is never.
//  2. Stop must be bounded regardless, so no future kernel or Go quirk in
//     (1) can ever again turn a graceful shutdown into a hard kill.

// fdIsNonblocking reads F_GETFL for an *os.File WITHOUT calling
// os.File.Fd() — because Fd() is the very call that broke BUG-9, and a test
// that used it would flip the flag it is trying to observe. SyscallConn's
// Control gives the raw descriptor while leaving the file's mode alone.
func fdIsNonblocking(t *testing.T, f *os.File) bool {
	t.Helper()
	sc, err := f.SyscallConn()
	if err != nil {
		t.Fatalf("SyscallConn(): %v", err)
	}
	var flags uintptr
	var errno syscall.Errno
	if err := sc.Control(func(fd uintptr) {
		flags, _, errno = syscall.Syscall(syscall.SYS_FCNTL, fd, syscall.F_GETFL, 0)
	}); err != nil {
		t.Fatalf("Control(): %v", err)
	}
	if errno != 0 {
		t.Fatalf("F_GETFL: %v", errno)
	}
	return flags&syscall.O_NONBLOCK != 0
}

// TestEvdevScannerOpensInterruptibleFds is the unit-level guard on BUG-9's
// root cause, and it needs no /dev/input and no privileges: a FIFO is the
// same kind of thing an evdev node is for this purpose — a pollable
// character-stream fd whose read blocks until somebody writes.
//
// If the O_NONBLOCK/os.NewFile pairing in evdevScanner.Open is ever
// replaced by a plain os.OpenFile + Fd() (the shape that shipped BUG-9),
// the first assertion fails immediately and the second one — a read that
// must come back when the fd is closed — fails a second later.
func TestEvdevScannerOpensInterruptibleFds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "event0")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("mkfifo unavailable here: %v", err)
	}

	scanner := evdevScanner{glob: filepath.Join(dir, "event*")}
	nodes := scanner.Scan()
	if len(nodes) != 1 || nodes[0].path != path {
		t.Fatalf("Scan() = %+v, want exactly the one node at %s", nodes, path)
	}

	// The writer is opened CONCURRENTLY with the scanner's read-side open,
	// and that is deliberate rather than fussy: opening a FIFO's read end
	// the way BUG-9's code did (a plain blocking os.OpenFile) blocks until
	// a writer arrives, so a test that opened the writer afterwards would
	// DEADLOCK against the buggy implementation instead of failing it. This
	// way both implementations get past the open, and the assertions below
	// are what separates them.
	type writerResult struct {
		f   *os.File
		err error
	}
	writerCh := make(chan writerResult, 1)
	go func() {
		f, err := os.OpenFile(path, os.O_WRONLY, 0)
		writerCh <- writerResult{f: f, err: err}
	}()

	dev, key, err := scanner.Open(path)
	if err != nil {
		t.Fatalf("Open(%s): %v", path, err)
	}
	if key == (deviceKey{}) {
		t.Error("Open() returned a zero deviceKey — the fstat on the open fd is what identifies a node instance")
	}
	f, ok := dev.(*os.File)
	if !ok {
		t.Fatalf("Open() returned %T, want *os.File", dev)
	}
	if !fdIsNonblocking(t, f) {
		t.Fatal("the opened fd is in BLOCKING mode: Close() cannot interrupt a read on it, which is exactly BUG-9 (a stop that waits for the next keystroke and gets SIGKILLed instead)")
	}

	// A live writer keeps the FIFO's read side genuinely blocked (rather
	// than at EOF), so the read below is a real in-flight read when Close
	// lands.
	var w *os.File
	select {
	case res := <-writerCh:
		if res.err != nil {
			t.Fatalf("open writer: %v", res.err)
		}
		w = res.f
	case <-time.After(2 * time.Second):
		t.Fatal("opening the FIFO's write end never completed")
	}
	defer func() { _ = w.Close() }()

	readErr := make(chan error, 1)
	go func() {
		buf := make([]byte, inputEventSize)
		_, err := f.Read(buf)
		readErr <- err
	}()
	// Give the reader time to be inside Read, so this measures interruption
	// and not a race won before the read started.
	time.Sleep(50 * time.Millisecond)

	start := time.Now()
	if err := f.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}
	select {
	case err := <-readErr:
		if took := time.Since(start); took > time.Second {
			t.Errorf("the blocked Read took %s to notice Close()", took)
		}
		if !errors.Is(err, os.ErrClosed) {
			t.Errorf("blocked Read returned %v, want an ErrClosed-shaped error", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the blocked Read did NOT return within 2s of Close() — BUG-9's exact failure: Stop() would sit here until the user touched a key")
	}
}

// TestLinuxProviderStopIsPromptOnRealDevices is the same guarantee, end to
// end, on the real hardware of whatever machine runs the suite: start the
// real provider on real /dev/input nodes, stop it while the machine is
// (probably) idle, and require the stop to be prompt. Before the fix this
// took as long as the next keypress — 10s of CLI patience and then a
// SIGKILL, every gate. It is skipped where no input node is readable (CI
// containers, a user not in the 'input' group), which is honest: there is
// nothing to regress there.
func TestLinuxProviderStopIsPromptOnRealDevices(t *testing.T) {
	p := NewLinuxProvider()
	var lines []string
	var mu sync.Mutex
	p.logf = func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, format)
	}
	if err := p.Start(); err != nil {
		t.Skipf("no readable /dev/input node on this machine: %v", err)
	}
	if got := p.Honesty(); got != HonestyGlobal {
		t.Fatalf("Honesty() after a successful Start = %v, want HonestyGlobal", got)
	}
	// Long enough that every reader goroutine is parked inside a read.
	time.Sleep(150 * time.Millisecond)

	// Stop runs on its own goroutine with a hard deadline, so the BUG-9
	// failure mode reports itself as a failed assertion instead of hanging
	// the suite until go test's panic timeout.
	stopped := make(chan error, 1)
	start := time.Now()
	go func() { stopped <- p.Stop() }()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("Stop(): %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stop() had not returned 5s after being called on real /dev/input devices — this is BUG-9: the reader goroutines are blocked in read(2) and only the next keystroke would free them")
	}
	took := time.Since(start)
	// The measured cost is dominated by one poller-close per device
	// (~6ms each, serially); a dozen devices land near 100ms. 2s is a
	// deliberately loose ceiling that still catches the real bug, whose
	// signature was "does not return at all until somebody types".
	if took > 2*time.Second {
		t.Errorf("Stop() took %s on real devices — BUG-9 territory (the CLI gives up at 5s and hard-kills)", took)
	}
	t.Logf("Stop() on real /dev/input devices returned in %s", took)
	if got := p.Honesty(); got != HonestyBlind {
		t.Errorf("Honesty() after Stop = %v, want HonestyBlind", got)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, l := range lines {
		if strings.Contains(l, "abandoning the wait") {
			t.Errorf("Stop() had to abandon its wait on real hardware: %q", l)
		}
	}
}

// stubbornDevice is a device whose Read NEVER returns, not even when the
// device is closed — the shape of the kernel/runtime quirk BUG-9 turned
// out to be, modelled directly so the ceiling can be tested rather than
// hoped for.
type stubbornDevice struct {
	closed  chan struct{}
	reading chan struct{} // closed once this device is INSIDE a read
	once    sync.Once
}

func (d *stubbornDevice) Read(p []byte) (int, error) {
	d.once.Do(func() { close(d.reading) })
	<-make(chan struct{}) // blocks forever, exactly like a bare read(2)
	return 0, nil
}

func (d *stubbornDevice) Close() error {
	close(d.closed)
	return nil
}

// stubbornScanner serves one stubborn node.
type stubbornScanner struct {
	dev *stubbornDevice
}

func (s *stubbornScanner) Scan() []deviceNode {
	return []deviceNode{{path: "/dev/input/event0", key: deviceKey{dev: 6, ino: 1, rdev: 0x0d40}}}
}

func (s *stubbornScanner) Open(path string) (inputDevice, deviceKey, error) {
	return s.dev, deviceKey{dev: 6, ino: 1, rdev: 0x0d40}, nil
}

// TestLinuxProviderStopIsBoundedEvenIfAReaderNeverReturns pins the second
// half of the fix: even in the world where closing an fd does NOT interrupt
// its read, Stop returns — promptly, and saying so — because a shutdown
// path that can hang is a shutdown path that gets SIGKILLed.
func TestLinuxProviderStopIsBoundedEvenIfAReaderNeverReturns(t *testing.T) {
	dev := &stubbornDevice{closed: make(chan struct{}), reading: make(chan struct{})}
	p := NewLinuxProvider()
	p.scanner = &stubbornScanner{dev: dev}
	var mu sync.Mutex
	var lines []string
	p.logf = func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, format)
	}
	if err := p.Start(); err != nil {
		t.Fatalf("Start(): %v", err)
	}
	// The reader must be INSIDE the uninterruptible read before Stop runs;
	// otherwise it simply sees the closed stop channel on its next loop and
	// exits, which is the easy case and not the one under test.
	select {
	case <-dev.reading:
	case <-time.After(2 * time.Second):
		t.Fatal("the reader goroutine never entered Read")
	}

	start := time.Now()
	if err := p.Stop(); err != nil {
		t.Fatalf("Stop(): %v", err)
	}
	took := time.Since(start)
	if took > stopWaitTimeout+time.Second {
		t.Errorf("Stop() took %s with an uninterruptible reader, want ~%s (bounded)", took, stopWaitTimeout)
	}
	if took < stopWaitTimeout {
		t.Errorf("Stop() returned in %s, before its own %s ceiling — it cannot have waited for the reader at all", took, stopWaitTimeout)
	}
	select {
	case <-dev.closed:
	default:
		t.Error("Stop() returned without closing the device fd")
	}
	if got := p.Honesty(); got != HonestyBlind {
		t.Errorf("Honesty() after Stop = %v, want HonestyBlind", got)
	}
	mu.Lock()
	defer mu.Unlock()
	var said bool
	for _, l := range lines {
		if strings.Contains(l, "abandoning the wait") {
			said = true
		}
	}
	if !said {
		t.Errorf("Stop() abandoned a reader goroutine SILENTLY; it must say so. logged: %q", lines)
	}
}

// evdevKeyPress is one raw evdev EV_KEY press event in the 24-byte on-wire
// layout the provider parses (see inputEventSize): a 16-byte timeval the
// provider skips, then type, code, value.
func evdevKeyPress(code uint16) []byte {
	ev := make([]byte, inputEventSize)
	binary.LittleEndian.PutUint16(ev[16:18], evKey)
	binary.LittleEndian.PutUint16(ev[18:20], code)
	binary.LittleEndian.PutUint32(ev[20:24], uint32(keyPressValue))
	return ev
}

// TestNonBlockingFdsStillDeliverEvents is the other half of BUG-9's fix
// being correct: an fd that can be interrupted is worthless if it stops
// DELIVERING. This drives the REAL evdevScanner (not the scripted seam)
// over a FIFO — a pollable stream, like an evdev node — and requires the
// events written to it to land in Snapshot().KeystrokeCount. If the
// netpoller path were broken, reads would either spin on EAGAIN or never
// see the data, and this fails.
func TestNonBlockingFdsStillDeliverEvents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "event0")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("mkfifo unavailable here: %v", err)
	}

	// A FIFO signals EOF (a Read returning 0 bytes, no error) precisely
	// when it currently has zero writers — which is exactly the shape the
	// real provider's readLoop treats as device death (provider_linux.go's
	// "read returned 0 bytes" branch), correctly: on a real evdev node that
	// only happens when the device is actually gone. But a FIFO passes
	// through that same zero-writer state for a moment every time its two
	// ends are opened independently, and that is not death, just plumbing.
	// Without something already holding a read fd open, the provider's OWN
	// first Read() can race the test's writer's open(): if it lands first,
	// the provider sees EOF, decides the device died, and closes its fd —
	// and the test's Write() below then finds no reader connected at all
	// and fails with "broken pipe". keepAlive is a second, independent read
	// fd held open for this FIFO's entire life so the pipe never legitimately
	// has zero readers-that-matter around that race, and — combined with
	// opening the writer before the provider ever touches the path, below —
	// removes the race outright rather than narrowing its window.
	keepAliveFd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatalf("open keep-alive reader: %v", err)
	}
	keepAlive := os.NewFile(uintptr(keepAliveFd), path)
	defer func() { _ = keepAlive.Close() }()

	// Opened here — before the provider exists at all — a blocking
	// O_WRONLY open on a FIFO only waits for A reader to be present, and
	// keepAlive already is one, so this returns immediately and the pipe
	// has a connected writer from this point on. That means the provider's
	// own read fd, opened by Start() below, can never observe "no writer"
	// (EOF); at worst it sees "no data yet" (EAGAIN), which its readLoop
	// already treats as pending, not death.
	w, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}
	defer func() { _ = w.Close() }()

	p := NewLinuxProvider()
	p.scanner = evdevScanner{glob: filepath.Join(dir, "event*")}
	p.logf = func(format string, args ...any) { t.Logf(format, args...) }
	p.rescanInterval = 100 * time.Millisecond
	if err := p.Start(); err != nil {
		t.Fatalf("Start() over a FIFO node: %v", err)
	}
	t.Cleanup(func() { _ = p.Stop() })

	// Three presses, spaced past the anti-mash coalescing window
	// (MouseSampleInterval) so all three are meant to count.
	const want = 3
	for i := 0; i < want; i++ {
		if _, err := w.Write(evdevKeyPress(30 /* KEY_A */)); err != nil {
			t.Fatalf("write event %d: %v", i, err)
		}
		time.Sleep(linuxSampleInterval + 20*time.Millisecond)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		if got := p.Snapshot().KeystrokeCount; got >= want {
			t.Logf("counted %d keystrokes through a non-blocking, poller-backed fd", got)
			return
		} else if time.Now().After(deadline) {
			t.Fatalf("KeystrokeCount = %d after writing %d press events, want >= %d — the non-blocking read path is not delivering events", got, want, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
