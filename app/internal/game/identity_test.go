package game

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeName(t *testing.T) {
	long := strings.Repeat("a", MaxNameLen+10)
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr error
	}{
		{"plain", "Pixel", "Pixel", nil},
		{"trims surrounding space", "   Pixel   ", "Pixel", nil},
		{"keeps inner spaces", "Sir Pixel Jr", "Sir Pixel Jr", nil},
		{"drops newlines", "Pix\nel", "Pixel", nil},
		{"drops carriage returns and tabs", "\tPix\rel\n", "Pixel", nil},
		{"drops NUL", "Pix\x00el", "Pixel", nil},
		{"drops ANSI escape", "Pix\x1b[31mel", "Pix[31mel", nil},
		{"drops DEL", "Pixel\x7f", "Pixel", nil},
		{"caps at MaxNameLen runes", long, strings.Repeat("a", MaxNameLen), nil},
		{"counts runes not bytes", strings.Repeat("é", MaxNameLen), strings.Repeat("é", MaxNameLen), nil},
		{"truncation is rune-safe", strings.Repeat("é", MaxNameLen+5), strings.Repeat("é", MaxNameLen), nil},
		{"keeps non-latin scripts intact", "ピクセル", "ピクセル", nil},
		{"keeps emoji", "Pixel 🐧", "Pixel 🐧", nil},
		{"empty is rejected", "", "", ErrEmptyName},
		{"whitespace only is rejected", "   \t  ", "", ErrEmptyName},
		{"control-chars only is rejected", "\n\r\x00", "", ErrEmptyName},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeName(tc.in)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("NormalizeName(%q) err = %v, want %v", tc.in, err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("NormalizeName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestNormalizeNameNeverReturnsControlCharacters is the invariant the
// per-case table above only samples: whatever comes out of the ONE door a
// name enters through carries no control character at all. This is the
// property the titlebar/menu render, the startup log line and config.json
// all rely on.
func TestNormalizeNameNeverReturnsControlCharacters(t *testing.T) {
	var b strings.Builder
	for r := rune(0); r < 0x100; r++ {
		b.WriteRune(r)
	}
	got, err := NormalizeName(b.String())
	if err != nil {
		t.Fatalf("NormalizeName(all bytes) unexpected error: %v", err)
	}
	for _, r := range got {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			t.Fatalf("NormalizeName leaked control rune %U in %q", r, got)
		}
	}
}

func TestSetConfigNameClearsOnboardingAndEchoesOnTheWire(t *testing.T) {
	g := New()
	g.SetOnboarding(true)

	if !g.State().Onboarding {
		t.Fatal("State().Onboarding = false right after SetOnboarding(true)")
	}
	if got := g.State().Config.Name; got != "" {
		t.Fatalf("a fresh game already has a name: %q", got)
	}

	stored, err := g.SetConfigName("  Pixel\n ")
	if err != nil {
		t.Fatalf("SetConfigName: %v", err)
	}
	if stored != "Pixel" {
		t.Fatalf("SetConfigName returned %q, want %q (the caller persists THIS value)", stored, "Pixel")
	}
	st := g.State()
	if st.Config.Name != "Pixel" {
		t.Fatalf("state.config.name = %q, want %q", st.Config.Name, "Pixel")
	}
	if st.Onboarding {
		t.Fatal("state.onboarding is still true after a successful SET_NAME — the modal would never close")
	}
}

func TestSetConfigNameRejectionIsAtomic(t *testing.T) {
	g := New()
	g.SetOnboarding(true)

	if _, err := g.SetConfigName("   "); !errors.Is(err, ErrEmptyName) {
		t.Fatalf("SetConfigName(blank) err = %v, want ErrEmptyName", err)
	}
	if got := g.ConfigName(); got != "" {
		t.Fatalf("a rejected SET_NAME stored %q — must be a no-op", got)
	}
	if !g.Onboarding() {
		t.Fatal("a rejected SET_NAME cleared the onboarding flag — the user would be left unnamed AND un-onboarded")
	}

	// A rejection must not clobber an ALREADY-set name either.
	if _, err := g.SetConfigName("Pixel"); err != nil {
		t.Fatalf("SetConfigName: %v", err)
	}
	if _, err := g.SetConfigName("\n"); !errors.Is(err, ErrEmptyName) {
		t.Fatalf("SetConfigName(control-only) err = %v, want ErrEmptyName", err)
	}
	if got := g.ConfigName(); got != "Pixel" {
		t.Fatalf("a rejected rename overwrote the stored name: %q", got)
	}
}

// TestRestoreConfigNameLeavesOnboardingAlone pins the boot-path contract:
// loading config.json must not decide the onboarding flag as a side
// effect — main() decides it from (no save existed) AND (no name), and a
// RestoreConfigName that cleared the flag would make the "fresh install,
// empty config.json" case unreachable.
func TestRestoreConfigNameLeavesOnboardingAlone(t *testing.T) {
	g := New()
	g.SetOnboarding(true)
	g.RestoreConfigName("Pixel")
	if !g.Onboarding() {
		t.Fatal("RestoreConfigName cleared the onboarding flag — only SetConfigName may do that")
	}
	if g.ConfigName() != "Pixel" {
		t.Fatalf("RestoreConfigName stored %q", g.ConfigName())
	}
}

// A hand-edited config.json is unsigned and may hold anything;
// store.LoadConfig's contract is that it "degrades to defaults and never
// blocks startup", and the name path must match that.
func TestRestoreConfigNameSanitisesAndDegrades(t *testing.T) {
	g := New()
	g.RestoreConfigName("  Pix\nel  ")
	if got := g.ConfigName(); got != "Pixel" {
		t.Fatalf("RestoreConfigName(%q) = %q, want %q", "  Pix\nel  ", got, "Pixel")
	}

	g2 := New()
	g2.RestoreConfigName("   \n ")
	if got := g2.ConfigName(); got != "" {
		t.Fatalf("a whitespace-only config.json name stored %q, want unnamed", got)
	}
}

// DefaultName is the SKIP path's value and is documented in
// docs/ui-spec.md §7.3 plus hardcoded in
// app/frontend/src/features/onboarding-modal.ts. Pin it here so the two
// sides cannot drift silently, and prove it actually passes validation
// (a default that NormalizeName rejected would make SKIP a dead button).
func TestDefaultNameIsAValidName(t *testing.T) {
	if DefaultName != "dexel" {
		t.Fatalf("DefaultName = %q; docs/ui-spec.md §7.3 and onboarding-modal.ts both say %q", DefaultName, "dexel")
	}
	got, err := NormalizeName(DefaultName)
	if err != nil {
		t.Fatalf("NormalizeName(DefaultName) rejected the SKIP default: %v", err)
	}
	if got != DefaultName {
		t.Fatalf("NormalizeName(DefaultName) = %q, want %q", got, DefaultName)
	}
}
