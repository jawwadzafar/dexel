package activity

import (
	"testing"
	"time"
)

func TestParseFakeScript(t *testing.T) {
	steps, err := ParseFakeScript("type:30s,idle:40s,mouse:10s")
	if err != nil {
		t.Fatalf("ParseFakeScript: %v", err)
	}
	want := []FakeStep{
		{Kind: FakeType, Duration: 30 * time.Second},
		{Kind: FakeIdle, Duration: 40 * time.Second},
		{Kind: FakeMouse, Duration: 10 * time.Second},
	}
	if len(steps) != len(want) {
		t.Fatalf("got %d steps, want %d", len(steps), len(want))
	}
	for i := range want {
		if steps[i] != want[i] {
			t.Errorf("step %d = %+v, want %+v", i, steps[i], want[i])
		}
	}

	if _, err := ParseFakeScript(""); err == nil {
		t.Error("expected error for empty script")
	}
	if _, err := ParseFakeScript("bogus"); err == nil {
		t.Error("expected error for malformed step")
	}
	if _, err := ParseFakeScript("dance:5s"); err == nil {
		t.Error("expected error for unknown kind")
	}
}

func TestFakeProviderReportsMouseAndIdleStates(t *testing.T) {
	script := []FakeStep{
		{Kind: FakeMouse, Duration: 200 * time.Millisecond},
		{Kind: FakeIdle, Duration: 200 * time.Millisecond},
	}
	p := NewFakeProvider(script, HonestyGlobal)
	_ = p.Start()

	time.Sleep(50 * time.Millisecond)
	snap := p.Snapshot()
	if !snap.MouseActive {
		t.Errorf("expected MouseActive during the mouse step, got %+v", snap)
	}

	time.Sleep(250 * time.Millisecond) // now into the idle step
	snap = p.Snapshot()
	if snap.MouseActive {
		t.Errorf("expected MouseActive=false during the idle step, got %+v", snap)
	}
	if snap.IdleSeconds <= 0 {
		t.Errorf("expected growing IdleSeconds during the idle step, got %v", snap.IdleSeconds)
	}
}

func TestFakeProviderKeystrokeCountIsMonotonic(t *testing.T) {
	script := []FakeStep{
		{Kind: FakeType, Duration: 100 * time.Millisecond},
		{Kind: FakeIdle, Duration: 100 * time.Millisecond},
	}
	p := NewFakeProvider(script, HonestyGlobal)
	_ = p.Start()

	var last uint64
	for i := 0; i < 10; i++ {
		time.Sleep(60 * time.Millisecond)
		snap := p.Snapshot()
		if snap.KeystrokeCount < last {
			t.Fatalf("KeystrokeCount decreased: %d -> %d", last, snap.KeystrokeCount)
		}
		last = snap.KeystrokeCount
	}
}

func TestFakeProviderHonestyBlindNeverSignalsIdleTrust(t *testing.T) {
	p := NewFakeProvider(DefaultFakeScript(), HonestyBlind)
	if p.Honesty() != HonestyBlind {
		t.Fatalf("Honesty() = %v, want HonestyBlind", p.Honesty())
	}
}
