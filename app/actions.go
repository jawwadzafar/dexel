// actions.go — applyAction: the pure state-transition core of runServe's
// action branch (docs/ui-spec.md §6.2). Every WS-action wire literal is
// named here so applyAction and runServe's action loop cannot drift apart
// on a typo.
package main

import (
	"fmt"
	"strings"

	"github.com/jawwadzafar/dexel/app/internal/game"
)

// actionSetName is SET_NAME's wire literal (docs/ui-spec.md §6.2), named
// once so applyAction and main's write-through loop cannot drift apart on
// a typo — the loop has to recognise the same action applyAction handled
// in order to know a config write is owed.
const actionSetName = "SET_NAME"

// actionSetPref is SET_PREF's wire literal (docs/ui-spec.md §6.2/§11.4),
// named once for exactly the reason actionSetName is: applyAction handles
// it, and main's action loop has to recognise the SAME literal to know a
// config write-through is owed.
const actionSetPref = "SET_PREF"

// actionSessionStart/actionSessionStop are SESSION_START/SESSION_STOP's
// wire literals (docs/plan/P2-design.md §6.2, docs/ui-spec.md §9.6),
// PINNED names per §8's contract seam. Named once for the same reason
// actionSetName is: applyAction and main's action-loop (the config
// write-through, the pending-record pop, the discard flash) must
// recognise the exact same literal, never a re-typed copy of it.
const (
	actionSessionStart = "SESSION_START"
	actionSessionStop  = "SESSION_STOP"
)

// actionPause/actionResume are PR-5's wire literals
// (docs/production-runtime/ARCHITECTURE.md §3's `PAUSE`/`RESUME` on the
// existing actions channel). Named once for exactly the reason
// actionSetName and the two session literals are: applyAction handles them,
// and runServe's action loop has to recognise the SAME literal to know that
// a provider stop/start, an engine reset and an immediate save are owed.
//
// They are reachable two ways, both of which land here: POST
// /api/lifecycle/{pause,resume} (token-gated, the CLI's path) and a
// WebSocket action from the UI. That is deliberate — Decision 5's "the
// WebSocket UI gets a pause button for free, with no second code path" —
// and it is safe in a way `stop` is not: pausing is a privacy-positive,
// fully reversible act, whereas "a web page must never be able to kill the
// runtime".
const (
	actionPause  = "PAUSE"
	actionResume = "RESUME"
)

// actionBuyAndEquip is the STORE-REDESIGN one-click action's wire literal
// (docs/plan/ROADMAP.md §STORE-REDESIGN): the card-grid store sends this
// single action for every purchase/equip interaction instead of chaining
// BUY_ITEM then EQUIP_ITEM across the 1Hz broadcast. It maps to
// game.Game.BuyAndEquip, which buys the item (if not already owned) and
// equips it as one atomic transaction — validated and refused as a unit,
// never half-applied (item-only since STORE-2.0). Named once here for the
// same drift-prevention reason every other literal above is.
const actionBuyAndEquip = "BUY_AND_EQUIP"

// applyAction runs one client action against g and reports whether state
// changed plus the flash it produced (docs/ui-spec.md §6.2: "no dedicated
// ack... every successful action is answered by an immediate state
// broadcast plus a flash; every failure by a flash of kind error").
// STORE_OPEN/STORE_CLOSE mutate state but produce no flash. connID is the
// sending connection's id (already plumbed through hub.go's
// actionRequest) — STORE_OPEN/CLOSE key their hold on the work gate by it
// (game.Game.OpenStore/CloseStore) so one client's close or disconnect can
// never release a different client's gate.
func applyAction(g *game.Game, msg actionMessage, connID uint64) (mutated bool, flash *flashMessage) {
	errFlash := func(err error) *flashMessage {
		return &flashMessage{Type: "flash", Kind: "error", Text: err.Error()}
	}

	switch msg.Action {
	case "BUY_ITEM":
		if err := g.BuyItem(msg.ItemID); err != nil {
			return false, errFlash(err)
		}
		item, _ := g.ItemByID(msg.ItemID)
		slot, _ := g.SlotByID(item.Slot)
		return true, &flashMessage{Type: "flash", Kind: "purchase", Text: fmt.Sprintf("%s %s!", item.Name, strings.ToLower(slot.Name))}

	case "EQUIP_ITEM":
		if err := g.EquipItem(msg.Slot, msg.ItemID); err != nil {
			return false, errFlash(err)
		}
		item, _ := g.ItemByID(msg.ItemID)
		return true, &flashMessage{Type: "flash", Kind: "equip", Text: fmt.Sprintf("Equipped %s!", item.Name)}

	case actionBuyAndEquip:
		// STORE-REDESIGN (docs/plan/ROADMAP.md §STORE-REDESIGN), item-only
		// since STORE-2.0. The one-click card store's combined action: buy
		// the item if not yet owned, then equip, ALL atomically inside
		// game.Game.BuyAndEquip (which validates unknown-item/slot, the
		// level gate and the cost before mutating anything, so a refusal is
		// never a half-applied purchase). The flash is a plain equip toast:
		// the user-visible outcome is "it is now equipped", and any coins
		// spent show in the HUD balance — content-free, a command not user
		// data.
		if err := g.BuyAndEquip(msg.Slot, msg.ItemID); err != nil {
			return false, errFlash(err)
		}
		item, _ := g.ItemByID(msg.ItemID)
		return true, &flashMessage{Type: "flash", Kind: "equip", Text: fmt.Sprintf("Equipped %s!", item.Name)}

	case actionSetName:
		// Phase P1 (docs/ui-spec.md §6.2 SET_NAME). Server-side
		// validation is game.NormalizeName's; an empty/whitespace-only/
		// control-character-only submission is an ordinary error flash
		// with NO state change (never a stored blank, which would
		// re-trigger onboarding on the next boot). A successful set also
		// clears the onboarding flag inside SetConfigName, so the
		// broadcast this returns is what closes the modal client-side —
		// there is no dedicated ack (§6.2).
		name, err := g.SetConfigName(msg.Name)
		if err != nil {
			return false, errFlash(err)
		}
		return true, &flashMessage{Type: "flash", Kind: "welcome", Text: "Hello, " + name + "!"}

	case actionSetPref:
		// SET-1 (docs/ui-spec.md §11.4, §6.2 SET_PREF). Server-side
		// validation is game.Game.SetPref's: the key must be on that
		// file's allow-list, so a client can never create a preference
		// this build does not know about — an unknown key is an ordinary
		// error flash with NO state change.
		//
		// NO FLASH on success, following the PAUSE/STORE_OPEN precedent
		// (§6.2): a preference is a state, not an event. The toggle
		// re-rendering from the state broadcast this returns IS the
		// feedback, and a toast for every checkbox would be noise. The
		// one thing that DOES produce a flash here is a failed config
		// write — main's action loop turns that into an error, because a
		// setting that silently will not survive a restart is a lie
		// (the same reasoning SET_NAME's write-through already applies).
		//
		// Setting a preference to the value it already has reports
		// mutated=false, which makes a repeat send a genuine no-op: no
		// broadcast, and no second config write in the loop below.
		mutated, err := g.SetPref(msg.Key, msg.Value)
		if err != nil {
			return false, errFlash(err)
		}
		return mutated, nil

	case actionPause, actionResume:
		// PR-5 (ARCHITECTURE.md §6). This is the whole of pause's
		// game-state transition: flip the flag (and, on the way in, park
		// the mood so nothing keeps claiming an observation that has
		// stopped — see game.Game.SetPaused). Everything that makes the
		// pause REAL rather than cosmetic — provider.Stop(), skipping
		// eng.Tick(), Engine.Reset() on the way back, the immediate save
		// — belongs to the caller's loop, which owns those resources.
		//
		// No flash: pause is a state, not an event, and `paused` on the
		// next state broadcast is what the UI renders (Decision 15).
		// STORE_OPEN/STORE_CLOSE below set the same precedent — mutate
		// state, produce no toast.
		//
		// An already-paused PAUSE (or an already-running RESUME) reports
		// mutated=false, which is what makes a repeated `dexel pause`
		// idempotent all the way down: no second provider stop, no
		// second save, no redundant broadcast.
		return g.SetPaused(msg.Action == actionPause), nil

	case "STORE_OPEN":
		g.OpenStore(connID)
		return true, nil

	case "STORE_CLOSE":
		g.CloseStore(connID)
		return true, nil

	case actionSessionStart:
		// Phase P2 (docs/plan/P2-design.md §2.2 "Start", §6.2;
		// docs/ui-spec.md §9.6). Server-side validation only
		// (game.NormalizeSessionName): control characters dropped,
		// trimmed, capped at MaxSessionNameLen runes — and unlike
		// SET_NAME, an EMPTY result is legal ("unnamed" is a first-class
		// session, not a rejection), so StartSession only ever errors
		// when a session is already active (exactly one at a time). The
		// flash text is fixed and composed here, never assembled
		// client-side (ui-spec §6.1's "no client-side assembly" rule).
		// This does not yet WRITE the name to config.json — main's
		// action-loop write-through (mirroring SET_NAME's persistConfig
		// call) does that immediately after this returns, whether or not
		// the name ended up empty.
		if err := g.StartSession(msg.Name); err != nil {
			return false, errFlash(err)
		}
		return true, &flashMessage{Type: "flash", Kind: "session", Text: "Session started."}

	case actionSessionStop:
		// Phase P2 (docs/plan/P2-design.md §2.2 "Stop", §6.2;
		// docs/ui-spec.md §9.6). StopSession's only rejection is "no
		// session is active" — no state change. On success the session
		// is ALWAYS cleared; whether it produced a poppable record (a
		// session under SessionMinDurationSeconds never does — Fork
		// P2-E) is decided entirely inside game.Game. main's action-loop
		// pops it via g.TakeEndedSession() immediately after this
		// returns (the pending-record seam) — which is also where the
		// "too short to keep" flash and the real sessionComplete
		// celebration are decided, so this case deliberately returns no
		// flash of its own.
		if err := g.StopSession(); err != nil {
			return false, errFlash(err)
		}
		return true, nil

	default:
		return false, errFlash(fmt.Errorf("unknown action %q", msg.Action))
	}
}
