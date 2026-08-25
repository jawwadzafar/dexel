package activity

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
	"time"
)

// This file is deliberately NOT build-tagged, so every test in it runs on the
// Linux box that builds this repo. provider_windows.go cannot be EXECUTED
// here, but the decisions that matter can all be checked anyway — either
// because they live in windows_signals.go as pure code, or (for the two
// privacy boundaries that are about which Win32 call is made at all) because
// the file is PARSED and inspected as an AST rather than run.

const winNs = int64(time.Millisecond) // shorthand: one millisecond in nanos

// ---------------------------------------------------------------------------
// winTally — the anti-mash coalescing semantics (ADR 0005)
// ---------------------------------------------------------------------------

// TestWinTallyCountsOncePerSampleWindow is the anti-mashing property itself:
// however fast the hook callback fires, at most one keystroke is COUNTED per
// MouseSampleInterval. A keyboard hook sees every key of a held-down repeat
// and every key of somebody leaning on the keyboard to farm Dev Cash, so this
// clamp is the difference between an economy and a keyboard-mashing contest.
func TestWinTallyCountsOncePerSampleWindow(t *testing.T) {
	var tally winTally
	base := int64(0)
	tally.reset(base)

	// 100 events crammed into the first millisecond of one window.
	for i := 0; i < 100; i++ {
		tally.key(base + int64(i)*winNs/100)
	}
	if got, _, _ := tally.read(base + winNs); got != 1 {
		t.Fatalf("100 key events inside one %s window counted %d keystrokes, want 1", windowsSampleInterval, got)
	}

	// One window later, exactly one more may be counted.
	next := base + int64(windowsSampleInterval)
	for i := 0; i < 100; i++ {
		tally.key(next + int64(i)*winNs/100)
	}
	if got, _, _ := tally.read(next + winNs); got != 2 {
		t.Fatalf("a second burst one window later counted %d keystrokes in total, want 2", got)
	}
}

// TestWinTallyIdleClockIgnoresCoalescing pins the distinction the Linux
// provider makes by putting `p.lastAnyInput = now` ABOVE its switch:
// coalescing shapes the ECONOMY, it must never make a fast typist look idle.
// Get this wrong and a user typing at 20 keys/second inside one window has an
// idle clock that only moves every 100ms — which is harmless — but the
// tempting "simplification" (update lastAnyInput only when a keystroke is
// counted) is what would eventually let a coalesced-away burst read as a
// break.
func TestWinTallyIdleClockIgnoresCoalescing(t *testing.T) {
	var tally winTally
	tally.reset(0)

	// One counted keystroke, then 99 coalesced away over the next 90ms.
	for i := 0; i < 100; i++ {
		tally.key(int64(i) * 90 * winNs / 100)
	}
	_, _, idle := tally.read(90 * winNs)
	if idle > 0.001 {
		t.Fatalf("idle after a continuous 90ms burst is %.4fs — the coalesced-away events must still move the idle clock", idle)
	}
	// And it does advance once input genuinely stops.
	if _, _, idle := tally.read(90*winNs + 5*int64(time.Second)); idle < 4.9 || idle > 5.1 {
		t.Fatalf("idle 5s after the last event is %.4fs, want ~5s", idle)
	}
}

// TestWinTallyMouseActiveLastsExactlyOneWindow mirrors provider_linux.go's
// `p.mouseActiveUntil = now.Add(linuxSampleInterval)`: MouseActive is a
// RECENCY flag with a one-window lifetime, not a count and not a latch.
func TestWinTallyMouseActiveLastsExactlyOneWindow(t *testing.T) {
	var tally winTally
	tally.reset(0)

	if _, active, _ := tally.read(0); active {
		t.Fatal("MouseActive is true before any mouse event")
	}
	tally.mouse(0)
	if _, active, _ := tally.read(int64(windowsSampleInterval) - 1); !active {
		t.Fatal("MouseActive is false inside the window following a mouse event")
	}
	if _, active, _ := tally.read(int64(windowsSampleInterval)); active {
		t.Fatalf("MouseActive is still true %s after the mouse event — it must expire with the window", windowsSampleInterval)
	}
	// Mouse activity must never touch the keystroke count.
	if got, _, _ := tally.read(0); got != 0 {
		t.Fatalf("mouse events incremented the keystroke count to %d", got)
	}
}

// TestWinTallyResetRestartsIdleWithoutLosingCount is the honesty invariant
// shared with provider_linux.go's recovery path: after a blind stretch (here,
// an evicted hook that the watchdog reinstalled) the idle clock restarts,
// because unobserved time is not idleness — while the keystroke total
// survives, because those presses really were observed and zeroing the
// counter would hand the engine a bogus negative delta.
func TestWinTallyResetRestartsIdleWithoutLosingCount(t *testing.T) {
	var tally winTally
	tally.reset(0)
	tally.key(0)
	tally.key(int64(windowsSampleInterval))
	tally.mouse(int64(windowsSampleInterval))

	// Nineteen hours of nobody watching (the Linux field failure's duration).
	blindFor := int64(19 * time.Hour)
	tally.reset(blindFor)

	keys, active, idle := tally.read(blindFor)
	if keys != 2 {
		t.Errorf("keystroke count after a reset is %d, want the earned 2 to survive", keys)
	}
	if active {
		t.Error("MouseActive survived the reset — a stale recency flag would claim activity nobody saw")
	}
	if idle != 0 {
		t.Errorf("idle immediately after a reset is %.4fs, want 0 — the unobserved stretch must not be handed to the engine as idleness", idle)
	}
}

// ---------------------------------------------------------------------------
// winWatchdog — the GetLastInputInfo cross-check
// ---------------------------------------------------------------------------

func TestWinWatchdogNeedsSustainedSuspicionBeforeReinstalling(t *testing.T) {
	var w winWatchdog

	// Prime.
	if w.observe(0, 1000, true) {
		t.Fatal("the watchdog fired on its very first observation, when it has levels but no deltas")
	}
	// Suspicious intervals: the OS clock of last input advances, our hooks
	// count nothing.
	for i := 1; i < winWatchdogStrikes; i++ {
		if w.observe(0, uint32(1000+i*100), true) {
			t.Fatalf("the watchdog fired after only %d suspicious interval(s); %d are required so a UAC prompt or lock screen cannot trigger a reinstall", i, winWatchdogStrikes)
		}
	}
	if !w.observe(0, 9000, true) {
		t.Fatalf("the watchdog did not fire after %d consecutive suspicious intervals", winWatchdogStrikes)
	}
	// Having fired, it re-arms from scratch rather than firing every interval.
	if w.observe(0, 9100, true) {
		t.Fatal("the watchdog fired again on the interval immediately after a reinstall — that is a reinstall loop")
	}
}

// TestWinWatchdogClearsStrikesWhenTheHooksAreWorking is the false-positive
// guard: a single interval in which the hooks DID report resets the run, so
// intermittent quiet cannot accumulate into a reinstall over an afternoon.
func TestWinWatchdogClearsStrikesWhenTheHooksAreWorking(t *testing.T) {
	var w winWatchdog
	w.observe(0, 1000, true) // prime

	events := uint64(0)
	tick := uint32(1000)
	for i := 0; i < winWatchdogStrikes*3; i++ {
		tick += 100
		if i%winWatchdogStrikes == winWatchdogStrikes-1 {
			events++ // one interval in every run where the hooks did report
		}
		if w.observe(events, tick, true) {
			t.Fatalf("the watchdog fired at interval %d even though the hooks reported events within the run", i)
		}
	}
}

// TestWinWatchdogAbstainsWithoutASecondOpinion: if GetLastInputInfo itself
// fails there is nothing to cross-check against, and guessing is exactly what
// this provider must not do. Abstain, and require the strike run to build up
// again once the cross-check returns.
func TestWinWatchdogAbstainsWithoutASecondOpinion(t *testing.T) {
	var w winWatchdog
	w.observe(0, 1000, true)
	for i := 0; i < winWatchdogStrikes-1; i++ {
		w.observe(0, uint32(1100+i*100), true)
	}
	// One failed cross-check must wipe the run, not complete it.
	if w.observe(0, 0, false) {
		t.Fatal("the watchdog fired on an interval where GetLastInputInfo failed — it has no second opinion there")
	}
	if w.observe(0, 5000, true) {
		t.Fatal("the watchdog fired one interval after the cross-check came back; the strike run must restart from scratch")
	}
}

// TestWinWatchdogTreatsATickWrapAsInput: GetLastInputInfo's dwTime is a
// GetTickCount value, which wraps every ~49.7 days. Comparing with `>` would
// read the wrap as "time went backwards" and therefore as "no input", which
// on a machine up that long is a silent hole in the cross-check.
func TestWinWatchdogTreatsATickWrapAsInput(t *testing.T) {
	var w winWatchdog
	const nearMax = ^uint32(0) - 50
	w.observe(0, nearMax, true) // prime just below the wrap

	// Wrapped, and our hooks saw nothing: that is a suspicious interval, so
	// the strike must land. Drive the rest of the run to prove it counted.
	fired := false
	tick := uint32(10)
	for i := 0; i < winWatchdogStrikes; i++ {
		if w.observe(0, tick, true) {
			fired = true
			break
		}
		tick += 100
	}
	if !fired {
		t.Fatalf("a wrapped dwTime was not counted as input, so the eviction run never completed within %d intervals", winWatchdogStrikes)
	}
}

// ---------------------------------------------------------------------------
// windowsAppNameFromImagePath — the app-identity narrowing
// ---------------------------------------------------------------------------

func TestWindowsAppNameFromImagePath(t *testing.T) {
	cases := []struct{ in, want string }{
		{`C:\Users\jawwad\AppData\Local\Programs\Microsoft VS Code\Code.exe`, "Code"},
		{`C:\Program Files\Google\Chrome\Application\chrome.exe`, "chrome"},
		{`C:\WINDOWS\SYSTEM32\NOTEPAD.EXE`, "NOTEPAD"},
		{`\Device\HarddiskVolume3\Windows\explorer.exe`, "explorer"},
		{`C:/msys64/usr/bin/mintty.exe`, "mintty"},
		{`dexel.exe`, "dexel"},
		{`dexel`, "dexel"},
		// Not ".exe": nothing is stripped, because inventing a general
		// extension rule is how a meaningful suffix eventually gets eaten.
		{`C:\tools\node.com`, "node.com"},
		{`C:\tools\v1.2.3.exe`, "v1.2.3"},
		// Degenerate shapes must not panic or invent a name.
		{``, ""},
		{`C:\Program Files\`, ""},
		{`.exe`, ""},
	}
	for _, c := range cases {
		if got := windowsAppNameFromImagePath(c.in); got != c.want {
			t.Errorf("windowsAppNameFromImagePath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestWindowsAppNameDropsEveryDirectory is the privacy half of the same
// function, asserted directly rather than left to the table above: a real
// image path carries the account name, and for a portable or project-local
// binary it can carry a client or repository name. None of it may survive.
func TestWindowsAppNameDropsEveryDirectory(t *testing.T) {
	secrets := []string{"jawwad", "acme-corp", "Project Nightingale", "Users", "AppData"}
	in := `C:\Users\jawwad\acme-corp\Project Nightingale\build\Code.exe`
	got := windowsAppNameFromImagePath(in)
	for _, s := range secrets {
		if strings.Contains(strings.ToLower(got), strings.ToLower(s)) {
			t.Fatalf("windowsAppNameFromImagePath(%q) = %q — it leaked the directory component %q", in, got, s)
		}
	}
	if got != "Code" {
		t.Fatalf("got %q, want just the base name %q", got, "Code")
	}
}

// TestWindowsImageNamesLandOnTheExistingFriendlyNames proves the ".exe"
// stripping is what makes the friendly-name table — written against macOS
// bundle names — work unchanged on Windows, end to end through the same
// NewAppIdentity pipeline every other platform uses.
func TestWindowsImageNamesLandOnTheExistingFriendlyNames(t *testing.T) {
	cases := []struct{ image, wantID, wantDisplay string }{
		{`C:\Users\x\AppData\Local\Programs\Microsoft VS Code\Code.exe`, "code", "VS Code"},
		{`C:\Program Files\Google\Chrome\Application\chrome.exe`, "chrome", "Chrome"},
		{`C:\Program Files\Mozilla Firefox\firefox.exe`, "firefox", "Firefox"},
		{`C:\Users\x\AppData\Local\slack\slack.exe`, "slack", "Slack"},
		{`C:\Users\x\AppData\Roaming\Spotify\Spotify.exe`, "spotify", "Spotify"},
		{`C:\Users\x\AppData\Local\Obsidian\Obsidian.exe`, "obsidian", "Obsidian"},
		// No table entry: FriendlyName falls back to the honest raw id
		// rather than inventing one. Windows-flavoured names like this
		// (msedge, devenv, WindowsTerminal) are a follow-up noted in ADR
		// 0021 — adding them means adding an appTypes entry too, which
		// sanitize_test.go's no-drift test enforces.
		{`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`, "msedge", "msedge"},
	}
	for _, c := range cases {
		id := NewAppIdentity(windowsAppNameFromImagePath(c.image), true)
		if id.ID != c.wantID || id.Display != c.wantDisplay {
			t.Errorf("%s -> {ID:%q Display:%q}, want {ID:%q Display:%q}", c.image, id.ID, id.Display, c.wantID, c.wantDisplay)
		}
		if !id.Available {
			t.Errorf("%s produced Available=false from an observable query", c.image)
		}
	}
}

// TestWindowsSelfIdentityIsRecognised: dexel must never narrate itself as the
// app you were working in (see SanitizeAppID's SelfAppID comment). Confirm the
// Windows path produces the id IsSelf actually matches — on Windows the
// executable is `dexel.exe`, so the ".exe" stripping is load-bearing for that
// guard rather than merely cosmetic.
func TestWindowsSelfIdentityIsRecognised(t *testing.T) {
	id := NewAppIdentity(windowsAppNameFromImagePath(`C:\Users\x\AppData\Local\dexel\bin\dexel.exe`), true)
	if !IsSelf(id.ID) {
		t.Fatalf("the Windows image name for dexel itself sanitized to %q, which IsSelf does not recognise — dexel would narrate itself as the app you were working in", id.ID)
	}
}

// ---------------------------------------------------------------------------
// The structural privacy guards on provider_windows.go
//
// provider_windows.go is //go:build windows and cannot RUN here, but it can
// be PARSED here — and what these two tests check is precisely which Win32
// calls the file makes and which struct fields it can name, both of which are
// syntax. Parsing (rather than a substring scan of the raw bytes) is what
// makes the checks comment-immune: the file discusses GetWindowTextW and
// MSLLHOOKSTRUCT at length in its HARD BOUNDARY comments explaining why it
// never touches them, and that documentation must not be what breaks the
// test — the same lesson TestDarwinProviderNeverReadsWindowTitle records.
// ---------------------------------------------------------------------------

// winParseProvider parses provider_windows.go and returns every identifier
// and string literal that appears in its CODE. Comments are not part of the
// AST, so they are excluded by construction.
func winParseProvider(t *testing.T) (*ast.File, map[string]bool) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "provider_windows.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing provider_windows.go: %v (this test must run from the package dir)", err)
	}
	tokens := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.Ident:
			tokens[v.Name] = true
		case *ast.SelectorExpr:
			tokens[v.Sel.Name] = true
		case *ast.BasicLit:
			tokens[strings.Trim(v.Value, "`\"")] = true
		}
		return true
	})
	return file, tokens
}

// TestWindowsProviderReadsNoWindowTitleAndNoCursorPosition is the Windows
// counterpart of TestDarwinProviderNeverReadsWindowTitle, and it guards two
// boundaries rather than one:
//
//   - From an HWND, exactly two things are reachable: the owning PROCESS
//     (allowed — ADR 0009 app identity) and the window TEXT (forbidden — the
//     document you have open, the URL of your tab). GetWindowTextW is one
//     LazyProc line away from being correct-looking code.
//   - The LL mouse hook's lParam points at an MSLLHOOKSTRUCT whose first
//     field is the CURSOR POSITION. Declaring that struct is the first step
//     of reading it, so the type must not exist here at all.
//
// Plus the neighbouring APIs that turn a keystroke count into a keylogger:
// ToUnicode/MapVirtualKey/GetKeyNameText translate a vkCode into a character,
// and GetKeyboardState/GetAsyncKeyState read key state directly.
func TestWindowsProviderReadsNoWindowTitleAndNoCursorPosition(t *testing.T) {
	_, tokens := winParseProvider(t)

	forbidden := map[string]string{
		"GetWindowTextW":        "the window TITLE — the document you have open, the URL of your tab (ADR 0002/0009)",
		"GetWindowText":         "the window TITLE (ADR 0002/0009)",
		"InternalGetWindowText": "the window TITLE by another name",
		"MSLLHOOKSTRUCT":        "the mouse hook struct, whose first field is the CURSOR POSITION — wParam alone is enough for a recency flag",
		"winMouseLLHookStruct":  "a local declaration of the mouse hook struct (see MSLLHOOKSTRUCT)",
		"ToUnicode":             "translation of a vkCode into the CHARACTER that was typed",
		"ToUnicodeEx":           "translation of a vkCode into the CHARACTER that was typed",
		"ToAscii":               "translation of a vkCode into the CHARACTER that was typed",
		"MapVirtualKey":         "translation of a vkCode into a character or scan code",
		"MapVirtualKeyW":        "translation of a vkCode into a character or scan code",
		"GetKeyNameText":        "the human-readable NAME of a key",
		"GetKeyNameTextW":       "the human-readable NAME of a key",
		"GetKeyboardState":      "the state of every key at once",
		"GetAsyncKeyState":      "the state of a specific key",
		"GetClipboardData":      "the CLIPBOARD (ADR 0012 defers copy/paste behind a permission fork; it is not smuggled in here)",
		"GetCursorPos":          "the CURSOR POSITION",
	}
	for name, why := range forbidden {
		if tokens[name] {
			t.Errorf("provider_windows.go references %s — forbidden, because it reads %s. Count events; never identify them.", name, why)
		}
	}

	// Non-vacuity: if the provider stopped using low-level hooks altogether,
	// every check above would pass while guarding nothing.
	for _, required := range []string{"SetWindowsHookExW", "winKbdLLHookStruct", "GetWindowThreadProcessId"} {
		if !tokens[required] {
			t.Errorf("provider_windows.go no longer references %s — this guard has gone vacuous; re-point it at whatever mechanism replaced the low-level hooks", required)
		}
	}
}

// TestWindowsKeyboardHookStructNamesOnlyVkCode is the structural half of the
// keyboard boundary. The hook ABI hands us a KBDLLHOOKSTRUCT, so its SIZE has
// to be described — but scanCode (the physical key), flags, time and
// dwExtraInfo are declared as BLANK fields, which means the physical key
// identity is not merely unused: it has no name in this program, and reading
// it requires editing the type declaration (where this test is waiting).
func TestWindowsKeyboardHookStructNamesOnlyVkCode(t *testing.T) {
	file, _ := winParseProvider(t)

	var named []string
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || ts.Name.Name != "winKbdLLHookStruct" {
			return true
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			t.Fatal("winKbdLLHookStruct is no longer a struct type")
		}
		found = true
		for _, f := range st.Fields.List {
			for _, name := range f.Names {
				if name.Name != "_" {
					named = append(named, name.Name)
				}
			}
		}
		return false
	})
	if !found {
		t.Fatal("winKbdLLHookStruct is not declared in provider_windows.go — this guard has gone vacuous")
	}
	if len(named) != 1 || named[0] != "vkCode" {
		t.Errorf("winKbdLLHookStruct names the field(s) %v; it may name only vkCode (a validity predicate that is dropped immediately). Every other field — scanCode above all — must stay blank so the physical key has no name in this program.", named)
	}
}
