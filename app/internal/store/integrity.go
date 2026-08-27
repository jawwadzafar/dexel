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
// (64 hex chars = 32 bytes). On an MIT-licensed open-source, fully-local,
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

// canonicalBody returns the exact JSON bytes the MAC is computed over,
// minus the domain tag: json.Marshal(d with Mac zeroed). d is a value
// parameter, so zeroing its Mac field here never mutates the caller's
// copy. Marshaling COMPACT (encoding/json's plain Marshal, not
// MarshalIndent — Save's on-disk formatting is a separate concern) means
// whitespace/indentation/key-order in the actual file are irrelevant:
// the MAC only ever sees our own canonical re-serialization of the typed
// struct, never raw file bytes (SEC-1 design §2.2). encoding/json is
// deterministic (struct fields in declaration order, map keys sorted,
// and every slice on SaveData is already sorted by Snapshot), and
// float64 round-trips bit-exactly, so this body is stable across
// Save/Load/Save for a logically-unchanged save.
//
// DB-1 (docs/plan/DB-1-design.md §2.3/§3.1): this is byte-for-byte what
// the sqlite `payload` BLOB column stores — the DB's MAC is verified
// against these exact stored bytes, not a re-serialization, which is why
// this split from the old single-shot macPreimage exists.
func canonicalBody(d SaveData) []byte {
	d.Mac = ""
	body, err := json.Marshal(d)
	if err != nil {
		// SaveData is our own struct of plain JSON-safe types (ints,
		// strings, slices, maps, one float64) — Marshal cannot fail on it
		// short of a programming error introducing something exotic.
		panic("store: marshal SaveData for MAC preimage failed: " + err.Error())
	}
	return body
}

// macPreimage builds the exact byte sequence the MAC is computed over
// given an already-canonicalized body (canonicalBody's output, or a
// DB row's stored `payload` bytes verbatim): domainTag ‖ 0x00 ‖ body.
func macPreimage(body []byte) []byte {
	preimage := make([]byte, 0, len(macDomain)+1+len(body))
	preimage = append(preimage, macDomain...)
	preimage = append(preimage, 0x00)
	preimage = append(preimage, body...)
	return preimage
}

// computeMACBytes returns the hex-encoded HMAC-SHA256 tag over body (via
// macPreimage, so the tag never covers itself when body already has Mac
// zeroed out, e.g. canonicalBody's output).
func computeMACBytes(body []byte) string {
	mac := hmac.New(sha256.New, integrityKey())
	mac.Write(macPreimage(body))
	return hex.EncodeToString(mac.Sum(nil))
}

// verifyMACBytes reports whether macHex matches computeMACBytes(body),
// compared in constant time via hmac.Equal on the decoded digest bytes —
// never on the hex strings themselves, and never with ==. An empty or
// non-hex macHex (e.g. a hand-edited tag) fails cleanly rather than
// panicking.
func verifyMACBytes(body []byte, macHex string) bool {
	got, err := hex.DecodeString(macHex)
	if err != nil {
		return false
	}
	want, err := hex.DecodeString(computeMACBytes(body))
	if err != nil {
		return false // unreachable: computeMACBytes always returns valid hex
	}
	return hmac.Equal(got, want)
}

// computeMAC returns the hex-encoded HMAC-SHA256 tag for d (Mac zeroed
// before computing). Unchanged behaviour from pre-DB-1: a thin wrapper
// over the byte-level helpers above.
func computeMAC(d SaveData) string {
	return computeMACBytes(canonicalBody(d))
}

// verifyMAC reports whether d.Mac matches computeMAC(d). An empty Mac
// simply fails, which is the point: an unsigned, hand-written save can
// never be trusted, so it can never mint an economy.
func verifyMAC(d SaveData) bool {
	return verifyMACBytes(canonicalBody(d), d.Mac)
}

// ErrTampered is Load's error (wrapped with detail) when a save parses
// fine but is refused: its MAC does not verify (including a save carrying
// no MAC at all), or it is an older/foreign schema this build cannot use
// (loadDB, db.go). It is deliberately a sentinel DISTINCT from both "no
// save" (Load's (SaveData{}, false, nil)) and ErrFutureSchema, so the
// caller can tell "load failed, quarantined, start fresh" apart from a
// genuinely fresh install — collapsing the two would let a tampered file
// masquerade as a first launch.
var ErrTampered = errors.New("save integrity check failed")

// logDomain is the session log's chain-MAC domain-separation tag (P2,
// docs/plan/P2-design.md §5.3, ADR 0017 Decision 5) — DISTINCT from
// macDomain so a signed state-snapshot payload can never be replayed as a
// session log row, or vice versa (SEC-1 §2.2's stated purpose for domain
// separation; this is exactly tamper-matrix row 11 in
// docs/plan/P2-design.md §7.3: "copy a valid state payload into a sessions
// row" is caught because logDomain != macDomain, not because of anything
// row-shape-specific).
const logDomain = "dexel-session-log-v1"

// logChainPreimage builds the exact byte sequence a session log row's MAC
// is computed over (§5.3): logDomain ‖ 0x00 ‖ prevMac ‖ 0x00 ‖ payload.
// prevMac is the PREVIOUS row's hex-encoded mac column verbatim — the
// ASCII bytes of that hex string, not decoded binary — and "" for the
// genesis row (row_mac_0 = ""). payload is the exact bytes stored in that
// row's own `payload` BLOB column (json.Marshal of a SessionSave, never a
// re-serialization) — the same "verify against stored bytes" discipline
// macPreimage already applies to the state row (DB-1 design §3.1).
func logChainPreimage(prevMac string, payload []byte) []byte {
	preimage := make([]byte, 0, len(logDomain)+1+len(prevMac)+1+len(payload))
	preimage = append(preimage, logDomain...)
	preimage = append(preimage, 0x00)
	preimage = append(preimage, prevMac...)
	preimage = append(preimage, 0x00)
	preimage = append(preimage, payload...)
	return preimage
}

// computeLogMAC returns the hex-encoded HMAC-SHA256 tag for one session
// log row, chained to prevMac (§5.3). Appending a row costs exactly ONE
// call to this function — never a re-sign of any earlier row, which is
// the whole point of a chain over independent per-row MACs (§5.3, DB-1
// design §2.4).
func computeLogMAC(prevMac string, payload []byte) string {
	mac := hmac.New(sha256.New, integrityKey())
	mac.Write(logChainPreimage(prevMac, payload))
	return hex.EncodeToString(mac.Sum(nil))
}

// verifyLogMAC reports whether macHex matches computeLogMAC(prevMac,
// payload), compared in constant time via hmac.Equal on the decoded
// digest bytes — verifyMACBytes's exact same discipline — never on the
// hex strings themselves, and never with ==. An empty or non-hex macHex
// (e.g. a hand-edited tag) fails cleanly rather than panicking.
func verifyLogMAC(prevMac string, payload []byte, macHex string) bool {
	got, err := hex.DecodeString(macHex)
	if err != nil {
		return false
	}
	want, err := hex.DecodeString(computeLogMAC(prevMac, payload))
	if err != nil {
		return false // unreachable: computeLogMAC always returns valid hex
	}
	return hmac.Equal(got, want)
}
