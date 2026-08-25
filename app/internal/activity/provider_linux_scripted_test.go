//go:build linux

package activity

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// This file is the test-only device seam for the Linux provider (Go's
// export_test.go idiom): evdev needs root or real hardware, so nothing about
// hotplug, device death, or honesty degradation can be driven through
// /dev/input from a unit test. Instead the provider's deviceScanner is
// injected with a SCRIPTED device set whose nodes can be killed, replaced,
// and re-enumerated on demand, and which counts every open and close so a
// test can assert there is no fd leak across repeated cycles.
//
// The model deliberately separates a NODE (a device instance in /dev, with
// its own inode/rdev and its own liveness) from a HANDLE (one open fd onto
// that node, which can be closed independently) — because that distinction
// is exactly what the field failure turned on: the runtime held handles
// onto nodes that no longer existed.
//
// The exported identifiers here are visible to the EXTERNAL activity_test
// package too, which is where the real-engine integration test has to live
// (internal/engine imports internal/activity, so an in-package test file
// cannot import the engine without an import cycle). Being in a _test.go
// file, none of it is part of the shipped API.

// scriptedNode is one fake evdev node in /dev/input.
type scriptedNode struct {
	path   string
	key    deviceKey
	events chan []byte

	mu   sync.Mutex
	dead bool
	died chan struct{}
}

func (n *scriptedNode) kill() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.dead {
		n.dead = true
		close(n.died)
	}
}

// scriptedHandle is one open fd onto a node. Reads block until an event is
// injected (exactly like a real blocking read on a character device), until
// the NODE dies — ENODEV, the errno GNOME's USB re-enumeration actually
// produced — or until this handle is closed, which is what Stop() does.
type scriptedHandle struct {
	node   *scriptedNode
	set    *ScriptedDevices
	closed chan struct{}
	once   sync.Once
}

func (h *scriptedHandle) Read(p []byte) (int, error) {
	select {
	case ev := <-h.node.events:
		return copy(p, ev), nil
	case <-h.node.died:
		return 0, syscall.ENODEV
	case <-h.closed:
		return 0, os.ErrClosed
	}
}

func (h *scriptedHandle) Close() error {
	h.once.Do(func() {
		close(h.closed)
		h.set.recordClose()
	})
	return nil
}

// ScriptedDevices is the injectable device set behind a test provider. It
// implements deviceScanner.
type ScriptedDevices struct {
	mu      sync.Mutex
	present map[string]*scriptedNode // path -> the node currently AT that path
	nextIno uint64
	opens   map[deviceKey]int
	closes  int
	scans   int
}

func newScriptedDevices(paths ...string) *ScriptedDevices {
	s := &ScriptedDevices{
		present: make(map[string]*scriptedNode),
		opens:   make(map[deviceKey]int),
		nextIno: 1000,
	}
	for _, p := range paths {
		s.Add(p)
	}
	return s
}

// Add creates a NEW node instance at path — the model of re-enumeration:
// the same /dev/input/eventN path, a different inode and rdev, which is
// precisely the shape of the failure a path-keyed device list cannot see.
func (s *ScriptedDevices) Add(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextIno++
	s.present[path] = &scriptedNode{
		path:   path,
		key:    deviceKey{dev: 6, ino: s.nextIno, rdev: 0x0d00 + s.nextIno},
		events: make(chan []byte, 64),
		died:   make(chan struct{}),
	}
}

// Kill makes the node at path die: it vanishes from enumeration and every
// read on any handle onto it fails with ENODEV.
func (s *ScriptedDevices) Kill(path string) {
	s.mu.Lock()
	n := s.present[path]
	if n != nil {
		delete(s.present, path)
	}
	s.mu.Unlock()
	if n != nil {
		n.kill()
	}
}

// Break models a node that is STILL THERE (it keeps showing up in
// enumeration and opens fine) but whose every read fails — a genuinely
// broken device, as opposed to a removed one. It is the input that would
// make a naive "death triggers a rescan" loop spin.
func (s *ScriptedDevices) Break(path string) {
	s.mu.Lock()
	n := s.present[path]
	s.mu.Unlock()
	if n != nil {
		n.kill()
	}
}

// KillAll kills every present node — suspend, dock unplug, or the
// screen-lock USB powerdown that started all this.
func (s *ScriptedDevices) KillAll() {
	s.mu.Lock()
	nodes := make([]*scriptedNode, 0, len(s.present))
	for path, n := range s.present {
		nodes = append(nodes, n)
		delete(s.present, path)
	}
	s.mu.Unlock()
	for _, n := range nodes {
		n.kill()
	}
}

// Inject feeds one raw 24-byte evdev event to the node at path. Returns
// false if no node is present there, or if nothing consumed the event
// within a second (a device nobody is reading).
func (s *ScriptedDevices) Inject(path string, raw []byte) bool {
	s.mu.Lock()
	n := s.present[path]
	s.mu.Unlock()
	if n == nil {
		return false
	}
	select {
	case n.events <- raw:
		return true
	case <-time.After(time.Second):
		return false
	}
}

// InjectKeystroke feeds one EV_KEY press to the node at path.
func (s *ScriptedDevices) InjectKeystroke(path string) bool {
	return s.Inject(path, rawEvent(evKey, 30, keyPressValue))
}

func (s *ScriptedDevices) recordClose() {
	s.mu.Lock()
	s.closes++
	s.mu.Unlock()
}

// OpenCount is the total number of successful opens across all nodes — the
// fd bookkeeping half of "no double-open, no fd leak".
func (s *ScriptedDevices) OpenCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int
	for _, c := range s.opens {
		n += c
	}
	return n
}

// OpensPerNode returns the highest number of times any single node instance
// was opened. Anything above 1 means the provider double-opened a node it
// already held an fd to.
func (s *ScriptedDevices) OpensPerNode() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	var max int
	for _, c := range s.opens {
		if c > max {
			max = c
		}
	}
	return max
}

// CloseCount is the total number of handles closed.
func (s *ScriptedDevices) CloseCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closes
}

// LiveHandles is opens-minus-closes: the number of fds the provider is
// holding right now. It must always equal the provider's open-device count,
// or an fd leaked.
func (s *ScriptedDevices) LiveHandles() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	var opened int
	for _, c := range s.opens {
		opened += c
	}
	return opened - s.closes
}

// Scans counts enumeration passes, so a test can wait for the rescan loop
// to have actually run rather than sleeping and hoping.
func (s *ScriptedDevices) Scans() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scans
}

// Scan implements deviceScanner.
func (s *ScriptedDevices) Scan() []deviceNode {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scans++
	nodes := make([]deviceNode, 0, len(s.present))
	for path, n := range s.present {
		nodes = append(nodes, deviceNode{path: path, key: n.key})
	}
	return nodes
}

// Open implements deviceScanner: a fresh handle onto whatever node is at
// path right now, keyed by that node's instance.
func (s *ScriptedDevices) Open(path string) (inputDevice, deviceKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.present[path]
	if n == nil {
		return nil, deviceKey{}, syscall.ENOENT
	}
	s.opens[n.key]++
	return &scriptedHandle{node: n, set: s, closed: make(chan struct{})}, n.key, nil
}

// rawEvent builds one 24-byte input_event with a zero timestamp.
func rawEvent(typ, code uint16, value int32) []byte {
	b := make([]byte, inputEventSize)
	b[16] = byte(typ)
	b[17] = byte(typ >> 8)
	b[18] = byte(code)
	b[19] = byte(code >> 8)
	u := uint32(value)
	b[20] = byte(u)
	b[21] = byte(u >> 8)
	b[22] = byte(u >> 16)
	b[23] = byte(u >> 24)
	return b
}

// NewLinuxProviderWithScriptedDevices builds a real LinuxProvider whose
// only fake is the OS: the device scanner. Rescan intervals are compressed
// so tests never sleep for real seconds.
func NewLinuxProviderWithScriptedDevices(paths ...string) (*LinuxProvider, *ScriptedDevices, *LogSink) {
	devices := newScriptedDevices(paths...)
	sink := &LogSink{}
	p := &LinuxProvider{
		scanner:           devices,
		logf:              sink.Printf,
		rescanInterval:    5 * time.Millisecond,
		minRescanInterval: 0,
	}
	return p, devices, sink
}

// LogSink captures the provider's log lines so a test can assert on them —
// both that a device death is reported exactly once, and that the line
// stays content-free (counts and /dev/input/eventN paths only).
type LogSink struct {
	mu    sync.Mutex
	lines []string
}

func (l *LogSink) Printf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

// Lines returns a copy of everything logged so far.
func (l *LogSink) Lines() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.lines...)
}

// Count returns how many logged lines contain substr.
func (l *LogSink) Count(substr string) int {
	var n int
	for _, line := range l.Lines() {
		if strings.Contains(line, substr) {
			n++
		}
	}
	return n
}

// ForceIdleAge backdates the provider's "last observed input" mark, so a
// test can reach the engine's OnBreakIdleThreshold without waiting 30
// wall-clock seconds. It writes the same field a real event writes, under
// the same mutex — no behaviour is bypassed.
func (p *LinuxProvider) ForceIdleAge(age time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastAnyInput = time.Now().Add(-age)
}

// OpenDeviceCount is the size of the provider's open set — the value the
// whole honesty state machine is derived from.
func (p *LinuxProvider) OpenDeviceCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.devices)
}

// KeystrokeCount exposes the provider's own counter for tests that need to
// prove counting resumed without depending on the blind Snapshot's shape.
func (p *LinuxProvider) KeystrokeCount() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.keystrokeCount
}

// waitFor polls cond until it holds or the deadline passes, and fails the
// test otherwise. Every asynchronous assertion in the hotplug tests goes
// through this rather than a bare sleep.
func waitFor(t *testing.T, timeout time.Duration, msg string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", timeout, msg)
}
