// integrity.go implements SEC-1's save-integrity HMAC
// (docs/plan/SEC-1-design.md §2/§3, docs/adr/0014-save-integrity-hmac-and-config-split.md):
// an HMAC-SHA256 tag over a canonical re-serialization of the protected
// SaveData struct, with a baked-in, openly documented key.
package store

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
)

// integrityKeyHex is the HMAC key for save-integrity checks, hex-encoded
// (64 hex chars = 32 bytes). On an Apache-2.0 open-source, fully-local,
// single-player game this key is NECESSARILY public: it is in this
// source file and in every compiled binary. It exists to stop CASUAL
// save-file editing (opening state.json in a text editor and changing
// "devCash": 100), which it does completely. It does NOT — and here
// CANNOT — stop a determined user who reads this file and recomputes the
// MAC. That is an accepted, explicit non-goal (see ADR 0014's Decision
// point 3 and SEC-1 design §3). We do NOT obfuscate it: obfuscation whose
// inverse lives in the same public repo is theater, and this project's
// ethic is honesty over theater, not a fake ceiling sold as a real one.
//
// Swapping the key SOURCE later (e.g. to Fork K's OS-keychain option,
// named-not-built in the design) is meant to be a one-function change:
// only integrityKey() below would move.
const integrityKeyHex = "aa3f80a5b5179b64955fcef7040119d11707e857744d4c205bf6e5aece034243"

// integrityKey returns the baked-in 32-byte HMAC key. See
// integrityKeyHex's doc comment for why it is public and unobfuscated by
// design, and an honest, stated non-goal rather than a hidden one.
func integrityKey() []byte {
	key, err := hex.DecodeString(integrityKeyHex)
	if err != nil {
		// Unreachable for the literal above; a corrupted constant would
		// fail this package's tests on every build, immediately.
		panic("store: integrityKeyHex is not valid hex: " + err.Error())
	}
	return key
}

// macDomain is the domain-separation tag prepended to every MAC preimage
// (SEC-1 design §2.2): it keeps this tag from ever being replayed into a
// different context, and versions the whole scheme — bump to
// "dexel-save-integrity-v2" to rotate. Schema already lives inside
// SaveData itself, so a schema-downgrade edit is caught by the MAC for
// free without needing to fold it into this tag too.
const macDomain = "dexel-save-integrity-v1"

// macPreimage builds the exact byte sequence the MAC is computed over:
// domainTag ‖ 0x00 ‖ json.Marshal(d with Mac zeroed). d is a value
// parameter, so zeroing its Mac field here never mutates the caller's
// copy. Marshaling COMPACT (encoding/json's plain Marshal, not
// MarshalIndent — Save's on-disk formatting is a separate concern) means
// whitespace/indentation/key-order in the actual file are irrelevant:
// the MAC only ever sees our own canonical re-serialization of the typed
// struct, never raw file bytes (SEC-1 design §2.2). encoding/json is
// deterministic (struct fields in declaration order, map keys sorted,
// and every slice on SaveData is already sorted by Snapshot), and
// float64 round-trips bit-exactly, so this preimage is stable across
// Save/Load/Save for a logically-unchanged save.
func macPreimage(d SaveData) []byte {
	d.Mac = ""
	body, err := json.Marshal(d)
	if err != nil {
		// SaveData is our own struct of plain JSON-safe types (ints,
		// strings, slices, maps, one float64) — Marshal cannot fail on it
		// short of a programming error introducing something exotic.
		panic("store: marshal SaveData for MAC preimage failed: " + err.Error())
	}
	preimage := make([]byte, 0, len(macDomain)+1+len(body))
	preimage = append(preimage, macDomain...)
	preimage = append(preimage, 0x00)
	preimage = append(preimage, body...)
	return preimage
}

// computeMAC returns the hex-encoded HMAC-SHA256 tag for d (Mac zeroed
// before computing, via macPreimage, so the tag never covers itself).
func computeMAC(d SaveData) string {
	mac := hmac.New(sha256.New, integrityKey())
	mac.Write(macPreimage(d))
	return hex.EncodeToString(mac.Sum(nil))
}

// verifyMAC reports whether d.Mac matches computeMAC(d), compared in
// constant time via hmac.Equal on the decoded digest bytes — never on the
// hex strings themselves, and never with ==. An empty or non-hex d.Mac
// (e.g. a hand-edited tag) fails cleanly rather than panicking; schema<5
// grandfathered saves must never reach this function in the first place
// (see Load in store.go).
func verifyMAC(d SaveData) bool {
	got, err := hex.DecodeString(d.Mac)
	if err != nil {
		return false
	}
	want, err := hex.DecodeString(computeMAC(d))
	if err != nil {
		return false // unreachable: computeMAC always returns valid hex
	}
	return hmac.Equal(got, want)
}

// ErrTampered is Load's error (wrapped with detail) when a schema>=5 save
// parses fine but its MAC does not verify. It is deliberately a sentinel
// DISTINCT from both "no save" (os.IsNotExist, Load's (SaveData{}, false,
// nil)) and ErrFutureSchema: see Load's doc comment in store.go for why
// collapsing tamper detection into "no save" would open an anti-cheat
// hole — main.go's loadOrImport would fall through to the legacy-import
// path, which grants items and refunds Dev Cash, letting a tampered file
// trigger a legacy re-grant.
var ErrTampered = errors.New("save integrity check failed")
