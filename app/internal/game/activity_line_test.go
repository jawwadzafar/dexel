package game

import (
	"strings"
	"testing"
	"time"

	"github.com/jawwadzafar/dexel/app/internal/activity"
	"github.com/jawwadzafar/dexel/app/internal/engine"
)

// TestActivityLineNeverClaimsYouTypedInDexel is the regression test for a
// line a user actually saw in the shipping app: **"Coding in dexel"**.
//
// It is built from two individually-true signals that must not be joined
// this way. `Coding` means "a key went down SOMEWHERE in the last 10s" — the
// macOS provider reads a global HID timer (ADR 0011), it cannot see which
// app received the keystroke. `ActiveApp` means "the frontmost app RIGHT
// NOW". So clicking dexel's window within 10 seconds of typing in your
// editor produced a sentence asserting you had been typing into dexel — a
// window with no text input, where not one character has ever been entered.
//
// Same family as the lie ADR 0010 already forbids ("On break because you
// minimized me"): a claim the data cannot support, made anyway because two
// numbers happened to line up.
func TestActivityLineNeverClaimsYouTypedInDexel(t *testing.T) {
	for _, mood := range []engine.Mood{engine.MoodCoding, engine.MoodIdle, engine.MoodOnBreak} {
		t.Run(string(mood), func(t *testing.T) {
			got := ActivityLine(mood, activity.SelfAppID, "dexel", "")
			if strings.Contains(strings.ToLower(got), "dexel") {
				t.Errorf("ActivityLine(%s, self) = %q — dexel must never name ITSELF as the app you were working in", mood, got)
			}
			// The mood itself is still reported honestly; only the
			// attribution is dropped.
			want := "Working..."
			if mood == engine.MoodCoding {
				want = "Coding"
			}
			if got != want {
				t.Errorf("ActivityLine(%s, self) = %q, want %q — the mood stays, the WHERE goes", mood, got, want)
			}
		})
	}
}

// workVerbFragments are the phrasings that assert the user was WORKING IN a
// specific app — the claim that needs a keystroke to have plausibly landed
// there. Several tests below scan for them, so they live in one place.
var workVerbFragments = []string{"coding in", "typing in", "heads-down in"}

func claimsWork(line string) string {
	lower := strings.ToLower(line)
	for _, frag := range workVerbFragments {
		if strings.Contains(lower, frag) {
			return frag
		}
	}
	return ""
}

// testComposer builds a composer with a frozen clock, so every phrasing
// choice below is deterministic. The production default (seed 0, time.Now) is
// the same code path with a different clock.
func testComposer(unixSeconds int64) *activityLineComposer {
	return &activityLineComposer{
		seed:        0,
		now:         func() time.Time { return time.Unix(unixSeconds, 0) },
		rerollAfter: activityLineRerollInterval,
	}
}

// renderPool is what the composer is allowed to have chosen: the pool for
// (type, mood), rendered with this app's display name. Asserting membership
// (rather than one exact string) is how a randomized-phrasing feature stays
// pinned without freezing the pool contents into every test.
func renderPool(typ activity.AppType, mood engine.Mood, display string) []string {
	pool := activityLinePoolsByType[typ].poolFor(mood)
	out := make([]string, 0, len(pool))
	for _, tpl := range pool {
		out = append(out, strings.ReplaceAll(tpl, "{app}", display))
	}
	return out
}

// TestActivityLineMatrix is the real matrix the fix has to survive: every app
// TYPE against every mood.
//
// The load-bearing assertion is the work-verb rule. "Coding in X" may appear
// only for a coding-class app (editor/IDE/terminal) with mood == Coding,
// because `Coding` means only "a key went down SOMEWHERE in the last 10s"
// (ADR 0011's global HID timer) — for any other app type, naming it as the
// place the typing happened is a fact dexel does not have. This is the test
// that would have caught **"Coding in Brave"**.
//
// One representative app id per type is enough for full coverage: the pools
// are keyed by TYPE, not by id, so every browser shares Brave's pool and
// every editor shares VS Code's (internal/activity's
// TestAppTypeAndFriendlyNamesDoNotDrift covers the id -> type half).
func TestActivityLineMatrix(t *testing.T) {
	apps := []struct {
		id, display string
		typ         activity.AppType
	}{
		{"code", "VS Code", activity.AppTypeCoding},
		{"iterm2", "iTerm", activity.AppTypeTerminal},
		{"brave-browser", "Brave", activity.AppTypeBrowser},
		{"slack", "Slack", activity.AppTypeComms},
		{"figma", "Figma", activity.AppTypeDesign},
		{"spotify", "Spotify", activity.AppTypeMedia},
		{"notion", "Notion", activity.AppTypeNotes},
		{"finder", "Finder", activity.AppTypeFiles},
		{"some-app-nobody-classified", "some-app-nobody-classified", activity.AppTypeUnknown},
	}
	moods := []engine.Mood{engine.MoodCoding, engine.MoodIdle, engine.MoodOnBreak}

	// A spread of frozen clocks so the matrix exercises different phrasings
	// out of each pool rather than whichever one bucket 0 happens to pick.
	for _, unix := range []int64{0, 47, 91, 1_755_000_000, 1_755_000_123} {
		c := testComposer(unix)
		for _, app := range apps {
			for _, mood := range moods {
				got := c.line(mood, app.id, app.display, "")

				// 1. Classification must be what we think it is, or the rest
				//    of this subtest is checking the wrong pool.
				if typ := activity.AppTypeOf(app.id); typ != app.typ {
					t.Fatalf("AppTypeOf(%q) = %q, want %q", app.id, typ, app.typ)
				}

				// 2. The line must come from that type+mood's pool. Nothing
				//    is composed outside the pools.
				want := renderPool(app.typ, mood, app.display)
				found := false
				for _, w := range want {
					if got == w {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("line(%s, %q) = %q, which is not in that type+mood's pool %q", mood, app.id, got, want)
				}

				// 3. THE RULE: a work verb only where a keystroke could have
				//    landed, and only when a keystroke actually happened.
				codingClass := app.typ == activity.AppTypeCoding || app.typ == activity.AppTypeTerminal
				if frag := claimsWork(got); frag != "" {
					if !codingClass {
						t.Errorf("line(%s, %q [%s]) = %q claims work (%q) for a non-coding app type — `Coding` never says WHICH app got the keystroke", mood, app.id, app.typ, got, frag)
					}
					if mood != engine.MoodCoding {
						t.Errorf("line(%s, %q) = %q claims work (%q) with no recent keystroke at all", mood, app.id, got, frag)
					}
				}

				// 4. dexel never names itself, whatever else it says.
				if strings.Contains(strings.ToLower(got), "dexel") {
					t.Errorf("line(%s, %q) = %q names dexel", mood, app.id, got)
				}
			}
		}
	}
}

// TestCodingAppsStillGetTheirWorkVerb pins the other half: this is not a
// retreat from ADR 0009's "Coding in VS Code". An editor or a terminal in
// front WITH a keystroke in the last few seconds is exactly the join the ADR
// intended, so those two types must still be able to say so.
func TestCodingAppsStillGetTheirWorkVerb(t *testing.T) {
	for _, app := range []struct{ id, display string }{
		{"code", "VS Code"},
		{"goland", "GoLand"},
		{"iterm2", "iTerm"},
		{"ghostty", "Ghostty"},
	} {
		t.Run(app.id, func(t *testing.T) {
			// Sweep buckets until the pool has been seen; every line in a
			// coding-class work pool must be either a work verb or the
			// class line ("In the terminal"), never bare presence.
			seenWork := false
			for i := int64(0); i < 200; i++ {
				c := testComposer(i * int64(activityLineRerollInterval/time.Second))
				got := c.line(engine.MoodCoding, app.id, app.display, "")
				if claimsWork(got) != "" {
					seenWork = true
					if !strings.Contains(got, app.display) && got != "In the terminal" {
						t.Errorf("work line %q does not name the app", got)
					}
				}
			}
			if !seenWork {
				t.Errorf("%s never produced a work verb while mood == Coding — ADR 0009's \"Coding in X\" must still be reachable", app.id)
			}
		})
	}
}

// TestActivityLinePoolsContainNoUnearnedClaims is the structural guard on the
// pools themselves, so a future edit cannot smuggle a work verb into a
// presence pool and quietly resurrect "Coding in Brave". It checks the tables
// rather than the output: `work` exists only for coding-class types, and no
// `atDesk`/`always` line may claim work in ANY type.
func TestActivityLinePoolsContainNoUnearnedClaims(t *testing.T) {
	for typ, pools := range activityLinePoolsByType {
		codingClass := typ == activity.AppTypeCoding || typ == activity.AppTypeTerminal
		if len(pools.work) > 0 && !codingClass {
			t.Errorf("app type %q has a work pool %q — a work verb is only claimable where a developer's keystrokes plausibly landed", typ, pools.work)
		}
		for _, tier := range []struct {
			name  string
			lines []string
		}{{"atDesk", pools.atDesk}, {"always", pools.always}} {
			for _, line := range tier.lines {
				if frag := claimsWork(line); frag != "" {
					t.Errorf("app type %q's %s pool contains %q (%q) — that tier is presence phrasing, true whatever the keystrokes did", typ, tier.name, line, frag)
				}
			}
		}
	}
	// The generic presence pool is used by every type including unknown, so
	// it must be the weakest possible claim: nothing but the app being in
	// front, and always naming the app (an unknown app's line is useless if
	// it does not say which app).
	for _, line := range activityLinePresence {
		if frag := claimsWork(line); frag != "" {
			t.Errorf("the shared presence pool contains %q (%q)", line, frag)
		}
		if !strings.Contains(line, "{app}") {
			t.Errorf("shared presence line %q does not name the app", line)
		}
	}
}

// TestEveryAppTypeHasAPool makes adding an AppType without adding phrasing a
// test failure rather than a silent fallback.
func TestEveryAppTypeHasAPool(t *testing.T) {
	for _, typ := range []activity.AppType{
		activity.AppTypeUnknown, activity.AppTypeCoding, activity.AppTypeTerminal,
		activity.AppTypeBrowser, activity.AppTypeComms, activity.AppTypeDesign,
		activity.AppTypeMedia, activity.AppTypeNotes, activity.AppTypeFiles,
		activity.AppTypeSelf,
	} {
		pools, ok := activityLinePoolsByType[typ]
		if !ok {
			t.Errorf("app type %q has no entry in activityLinePoolsByType", typ)
			continue
		}
		if typ == activity.AppTypeSelf {
			// dexel's own window must have NOTHING to say about itself.
			if len(pools.work)+len(pools.atDesk)+len(pools.always) != 0 {
				t.Errorf("AppTypeSelf has phrasing (%+v) — dexel must never have a line that names itself", pools)
			}
			continue
		}
		if len(pools.always) == 0 {
			t.Errorf("app type %q has no `always` pool — every type needs phrasing that survives OnBreak", typ)
		}
	}
}

// TestActivityLineDoesNotChurnAtOneHertz is the stability half of the
// feature. The state broadcast runs at ~1 Hz (docs/ui-spec.md §6), so a
// phrasing that re-rolled per tick would visibly flicker once a second and
// read as a broken widget. This ticks two simulated minutes, one second at a
// time, with the app and mood held constant, and asserts the line changes only
// when the reroll bucket advances.
func TestActivityLineDoesNotChurnAtOneHertz(t *testing.T) {
	const seconds = 120
	rerollSeconds := int64(activityLineRerollInterval / time.Second)

	for _, app := range []struct{ id, display string }{
		{"code", "VS Code"},
		{"brave-browser", "Brave"},
		{"slack", "Slack"},
	} {
		for _, mood := range []engine.Mood{engine.MoodCoding, engine.MoodIdle, engine.MoodOnBreak} {
			t.Run(app.id+"/"+string(mood), func(t *testing.T) {
				now := time.Unix(1_755_000_000, 0)
				c := &activityLineComposer{seed: 0, now: func() time.Time { return now }, rerollAfter: activityLineRerollInterval}

				prev := ""
				changes := 0
				for i := 0; i < seconds; i++ {
					got := c.line(mood, app.id, app.display, "")
					if i > 0 && got != prev {
						changes++
					}
					prev = got
					now = now.Add(time.Second)
				}
				// 120s of 45s buckets crosses at most 3 boundaries.
				maxChanges := int(seconds/rerollSeconds) + 1
				if changes > maxChanges {
					t.Errorf("the line changed %d times in %ds of 1 Hz ticks (max %d for a %ds reroll) — it must be STABLE while the frontmost app is", changes, seconds, maxChanges, rerollSeconds)
				}
			})
		}
	}
}

// TestActivityLineIsStableWithinOneRerollBucket is the sharper version of the
// churn test: inside a single bucket the line must be byte-identical, no
// matter how often it is asked. This is the assertion a `math/rand` call in
// the render path fails immediately.
func TestActivityLineIsStableWithinOneRerollBucket(t *testing.T) {
	rerollSeconds := int64(activityLineRerollInterval / time.Second)
	// A timestamp exactly on a bucket boundary, so the whole bucket is ahead.
	base := (int64(1_755_000_000) / rerollSeconds) * rerollSeconds

	want := ""
	for off := int64(0); off < rerollSeconds; off++ {
		got := testComposer(base+off).line(engine.MoodIdle, "brave-browser", "Brave", "")
		if off == 0 {
			want = got
			continue
		}
		if got != want {
			t.Fatalf("at +%ds inside one %ds bucket the line changed from %q to %q", off, rerollSeconds, want, got)
		}
	}
	// ...and the next bucket is allowed to differ, which is what keeps it
	// from being one frozen string forever (see TestActivityLineVaries).
	if next := testComposer(base+rerollSeconds).line(engine.MoodIdle, "brave-browser", "Brave", ""); next == "" {
		t.Fatal("the next bucket produced an empty line")
	}
}

// TestActivityLineVaries proves the pools are actually pools: the phrasing
// varies both across apps at one instant and across time for one app. Without
// this, "a small pool per app type" could be satisfied by a table nobody ever
// reads past index 0.
func TestActivityLineVaries(t *testing.T) {
	// Across apps at a single frozen instant (the app id is in the hash, so
	// switching apps re-rolls).
	c := testComposer(1_755_000_000)
	seen := map[string]bool{}
	for _, app := range []struct{ id, display string }{
		{"brave-browser", "{app}"}, {"google-chrome", "{app}"}, {"safari", "{app}"},
		{"firefox", "{app}"}, {"arc", "{app}"}, {"vivaldi", "{app}"}, {"opera", "{app}"},
	} {
		// Rendering with the literal "{app}" recovers the TEMPLATE, so
		// different app names cannot masquerade as different phrasings.
		seen[c.line(engine.MoodIdle, app.id, app.display, "")] = true
	}
	if len(seen) < 2 {
		t.Errorf("seven browsers at one instant produced %d distinct phrasing(s) %v — the pool is not being used", len(seen), seen)
	}

	// Across time for one app.
	rerollSeconds := int64(activityLineRerollInterval / time.Second)
	overTime := map[string]bool{}
	for i := int64(0); i < 40; i++ {
		overTime[testComposer(i*rerollSeconds).line(engine.MoodIdle, "brave-browser", "{app}", "")] = true
	}
	if len(overTime) < 3 {
		t.Errorf("40 reroll buckets on one app produced %d distinct phrasing(s) %v — the line would feel like a treadmill", len(overTime), overTime)
	}
}

// TestActivityLineFitsTheStatusLine keeps the pools inside the width
// docs/ui-spec.md §2.3 gives #status-line (34 chars). A phrasing that does not
// fit for a given app is not offered at all, so a long friendly name can
// never choose a line the frontend would clip.
//
// The name is swept alongside the app because it eats into the same 34-char
// budget: an UNNAMED dexel, a short name ("Pixel"), and a name at the full
// MaxNameLen (a long real name like "Professor Longbottom", and the absolute
// worst case of MaxNameLen ASCII chars) must ALL stay within budget for every
// app + mood — the composer drops the rich app-naming phrasing to the short
// "{name} is coding"/"here"/"away" anchor when the full sentence would clip.
func TestActivityLineFitsTheStatusLine(t *testing.T) {
	apps := []struct{ id, display string }{
		{"code", "VS Code"},
		{"android-studio", "Android Studio"},
		{"intellij-idea", "IntelliJ IDEA"},
		{"affinity-designer", "Affinity Designer"},
		{"safari-technology-preview", "Safari Preview"},
		{"microsoft-powerpoint", "PowerPoint"},
		{"finder", "Finder"},
	}
	names := []string{
		"",                              // unnamed — today's impersonal phrasing
		"Pixel",                         // a short, ordinary name
		"Professor Longbottom",          // a long but realistic name (20 chars)
		strings.Repeat("N", MaxNameLen), // the absolute worst case (24 ASCII)
	}
	rerollSeconds := int64(activityLineRerollInterval / time.Second)
	for _, name := range names {
		for _, app := range apps {
			for _, mood := range []engine.Mood{engine.MoodCoding, engine.MoodIdle, engine.MoodOnBreak} {
				for i := int64(0); i < 50; i++ {
					got := testComposer(i*rerollSeconds).line(mood, app.id, app.display, name)
					if len(got) > maxActivityLineLen {
						t.Errorf("line(%s, %q, name=%q) = %q is %d chars, over #status-line's %d", mood, app.id, name, got, len(got), maxActivityLineLen)
					}
				}
			}
		}
	}
}

// TestPersonalLineFitsAtMaxName is the sharp version of the width rule for the
// personal path: for a name at the full MaxNameLen, EVERY mood must have a
// rendering that fits — the app-less fallback anchor is what guarantees it, so
// this fails the instant an anchor grows past what a max-length name allows.
func TestPersonalLineFitsAtMaxName(t *testing.T) {
	name := strings.Repeat("W", MaxNameLen) // 24 ASCII, the widest legal name
	c := testComposer(1_755_000_000)
	// A coding-class app (work tier), a non-coding app (zone/present tiers via
	// mood), and no app at all — the anchor must save all of them.
	for _, app := range []struct{ id, display string }{
		{"code", "VS Code"},
		{"brave-browser", "Brave"},
		{"", ""},
	} {
		for _, mood := range []engine.Mood{engine.MoodCoding, engine.MoodIdle, engine.MoodOnBreak} {
			got := c.line(mood, app.id, app.display, name)
			if len(got) > maxActivityLineLen {
				t.Errorf("personal line(%s, %q, max-name) = %q is %d chars, over %d — the fallback anchor must fit at MaxNameLen", mood, app.id, got, len(got), maxActivityLineLen)
			}
			if !strings.HasPrefix(got, name+" is ") {
				t.Errorf("personal line(%s, %q) = %q does not read as \"{name} is …\"", mood, app.id, got)
			}
		}
	}
}

// TestPersonalLineIsAlwaysASentenceAboutTheName pins the shape of the feature:
// whenever a name is set, the line reads "{name} is …" — never the impersonal
// "Coding in X" / "Working..." forms, and never a bare echo of the name.
func TestPersonalLineIsAlwaysASentenceAboutTheName(t *testing.T) {
	const name = "Pixel"
	apps := []struct {
		id, display string
	}{
		{"code", "VS Code"}, {"iterm2", "iTerm"}, {"brave-browser", "Brave"},
		{"slack", "Slack"}, {"figma", "Figma"}, {"spotify", "Spotify"},
		{"finder", "Finder"}, {"some-app-nobody-classified", "some-app-nobody-classified"},
		{"", ""}, {activity.SelfAppID, "dexel"},
	}
	for _, unix := range []int64{0, 47, 91, 1_755_000_000, 1_755_000_123} {
		c := testComposer(unix)
		for _, app := range apps {
			for _, mood := range []engine.Mood{engine.MoodCoding, engine.MoodIdle, engine.MoodOnBreak} {
				got := c.line(mood, app.id, app.display, name)
				if !strings.HasPrefix(got, name+" is ") {
					t.Errorf("named line(%s, %q) = %q must read \"%s is …\"", mood, app.id, got, name)
				}
				// dexel never names itself, personal or not.
				if strings.Contains(strings.ToLower(got), "dexel") {
					t.Errorf("named line(%s, %q) = %q names dexel", mood, app.id, got)
				}
			}
		}
	}
}

// TestPersonalWorkVerbOnlyForCodingApps is the personal-path twin of the
// impersonal privacy rule: a work verb ("coding in {app}" &c) may appear ONLY
// for a coding-class app in Coding mood, because `Coding` still only means "a
// key went down SOMEWHERE" (ADR 0011's global HID timer). A named browser
// session must never become "Pixel is coding in Brave".
func TestPersonalWorkVerbOnlyForCodingApps(t *testing.T) {
	const name = "Pixel"
	apps := []struct {
		id, display string
		typ         activity.AppType
	}{
		{"code", "VS Code", activity.AppTypeCoding},
		{"iterm2", "iTerm", activity.AppTypeTerminal},
		{"brave-browser", "Brave", activity.AppTypeBrowser},
		{"slack", "Slack", activity.AppTypeComms},
		{"figma", "Figma", activity.AppTypeDesign},
		{"", "", activity.AppTypeUnknown},
	}
	rerollSeconds := int64(activityLineRerollInterval / time.Second)
	for _, app := range apps {
		codingClass := app.typ == activity.AppTypeCoding || app.typ == activity.AppTypeTerminal
		for _, mood := range []engine.Mood{engine.MoodCoding, engine.MoodIdle, engine.MoodOnBreak} {
			for i := int64(0); i < 60; i++ {
				got := testComposer(i*rerollSeconds).line(mood, app.id, app.display, name)
				if frag := claimsWork(got); frag != "" {
					if !codingClass {
						t.Errorf("named line(%s, %q [%s]) = %q claims work (%q) for a non-coding app type", mood, app.id, app.typ, got, frag)
					}
					if mood != engine.MoodCoding {
						t.Errorf("named line(%s, %q) = %q claims work (%q) with no recent keystroke", mood, app.id, got, frag)
					}
					// A work claim must name the app it claims work in.
					if app.display != "" && !strings.Contains(got, app.display) {
						t.Errorf("named work line %q does not name its app %q", got, app.display)
					}
				}
			}
		}
	}
}

// TestPersonalCodingAppStillNamesTheApp keeps the good half: a short name on a
// coding-class app in Coding mood must be able to say "Pixel is coding in VS
// Code" (naming the app), not just degrade to the bare "Pixel is coding".
func TestPersonalCodingAppStillNamesTheApp(t *testing.T) {
	rerollSeconds := int64(activityLineRerollInterval / time.Second)
	sawAppNamed := false
	for i := int64(0); i < 60; i++ {
		got := testComposer(i*rerollSeconds).line(engine.MoodCoding, "code", "VS Code", "Pixel")
		if claimsWork(got) != "" && strings.Contains(got, "VS Code") {
			sawAppNamed = true
		}
	}
	if !sawAppNamed {
		t.Error(`a named dexel coding in VS Code never produced "Pixel is coding in VS Code" — the app-naming work form must be reachable`)
	}
}

// TestPersonalPoolsContainNoUnearnedClaims is the structural guard on the
// personal pools, mirroring TestActivityLinePoolsContainNoUnearnedClaims: a
// work verb may live ONLY in personalWork.primary, never in a cozy pool.
func TestPersonalPoolsContainNoUnearnedClaims(t *testing.T) {
	for _, p := range []struct {
		name  string
		pools personalPools
	}{
		{"personalZone", personalZone},
		{"personalPresent", personalPresent},
		{"personalBreak", personalBreak},
	} {
		for _, tier := range [][]string{p.pools.primary, p.pools.fallback} {
			for _, line := range tier {
				if frag := claimsWork(line); frag != "" {
					t.Errorf("%s contains %q (%q) — only the coding-class work pool may claim work", p.name, line, frag)
				}
			}
		}
	}
	// And every personal line, once the tokens are stripped, must open with
	// "{name} is " — the shape the whole feature promises.
	for _, p := range []personalPools{personalWork, personalZone, personalPresent, personalBreak} {
		for _, tier := range [][]string{p.primary, p.fallback} {
			for _, tpl := range tier {
				if !strings.HasPrefix(tpl, "{name} is ") {
					t.Errorf("personal template %q does not start with \"{name} is \"", tpl)
				}
			}
		}
	}
}

// TestActivityLineHandlesTheAwkwardIdentities covers the inputs that are not
// a tidy known app: no app at all, an app with no friendly name, and an app
// id at SanitizeAppID's full length cap (where NO phrasing fits and the
// composer must fall back to the shortest true rendering rather than
// inventing a shorter, less true one).
func TestActivityLineHandlesTheAwkwardIdentities(t *testing.T) {
	c := testComposer(1_755_000_000)

	// No app identity at all — the provider looked and nothing was frontmost,
	// or it cannot see app identity here (activity.AppIdentity.Available).
	// The mood is still reported; nothing is claimed about where.
	if got := c.line(engine.MoodCoding, "", "", ""); got != "Coding" {
		t.Errorf("line(coding, no app) = %q, want %q", got, "Coding")
	}
	for _, mood := range []engine.Mood{engine.MoodIdle, engine.MoodOnBreak} {
		if got := c.line(mood, "", "", ""); got != "Working..." {
			t.Errorf("line(%s, no app) = %q, want %q", mood, got, "Working...")
		}
	}

	// An unknown app with no friendly name: the raw sanitized id is used. An
	// honest raw id beats a fabricated name, and it must still never get a
	// work verb.
	got := c.line(engine.MoodCoding, "obscure-editor-2000", "", "")
	if !strings.Contains(got, "obscure-editor-2000") {
		t.Errorf("line(coding, unclassified app, no display) = %q — it should fall back to the sanitized id", got)
	}
	if frag := claimsWork(got); frag != "" {
		t.Errorf("line(coding, unclassified app) = %q claims work (%q) — an app we cannot classify gets presence phrasing, never a guess", got, frag)
	}

	// An id at the length cap: every phrasing overflows #status-line, so the
	// shortest true rendering wins. "In X" is the shortest line in the
	// presence pool; being clipped by the frontend is a display problem,
	// inventing a shorter claim would be a lie.
	long := strings.Repeat("a", activity.MaxAppIDLen)
	if got := c.line(engine.MoodIdle, long, "", ""); got != "In "+long {
		t.Errorf("line(idle, %d-char id) = %q, want %q — the shortest true rendering", activity.MaxAppIDLen, got, "In "+long)
	}
}

// TestActivityLineUsesTheDefaultComposer keeps the exported entry point wired
// to the same code path the tests exercise: whatever ActivityLine returns for
// a real app must be a member of that app's pool, with the production clock.
func TestActivityLineUsesTheDefaultComposer(t *testing.T) {
	got := ActivityLine(engine.MoodIdle, "brave-browser", "Brave", "")
	want := renderPool(activity.AppTypeBrowser, engine.MoodIdle, "Brave")
	for _, w := range want {
		if got == w {
			return
		}
	}
	t.Errorf("ActivityLine(idle, brave-browser) = %q, not in %q", got, want)
}
