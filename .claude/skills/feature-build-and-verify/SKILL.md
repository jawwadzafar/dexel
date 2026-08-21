---
name: feature-build-and-verify
description: |
  How features get built and shipped in dev-companion: the provider -> engine
  -> game -> WS -> frontend architecture and its privacy boundary (counts and
  durations only, enforced by structural allow-list tests on Snapshot /
  StateMessage / SaveData), the phased-shippable-slices approach, and THE
  GATE — no visual/UX change is trusted until it is rendered in the REAL
  running game (build the Go binary, run it with the fake provider, screenshot
  via headless Chromium, judge it with your own eyes) because isolated
  mockups have lied twice already. Use when implementing, changing, or
  reviewing anything in app/, or before calling any visual/UX change done.
x-fleetsmith-origin: human
---

# Feature build and verify

## The architecture (know where a change belongs)

```
activity provider (darwin CGEventSource poll / linux evdev / fake script)
        │  Snapshot{KeystrokeCount, MouseActive, IdleSeconds, ActiveApp,
        │           ActiveAppDisplay} — counts, bools, seconds. Never content.
        ▼
engine (internal/engine) — anti-mash economy (ADR 0005), honest mood machine
        │  (ADR 0010: Coding needs a recent keystroke; mouse alone never
        │  shows it; a blind source freezes the idle clock instead of guessing)
        ▼
game (internal/game) — Game struct, sprints, catalog, save/load
        │
        ▼
WS state broadcast (hub.go) — StateMessage / CatalogMessage, ~1 Hz + on
        │  every mutation, camelCase JSON (docs/ui-spec.md §6)
        ▼
frontend (public/js/game.js + index.html + NES.css) — pure function of the
           last `state` message; never asserts anything the server didn't send
```

Each arrow is a one-way, one-owner boundary. A new signal (a new provider) or
a new visible thing (a new catalog item) is new content behind an existing
interface; it should not require touching every layer.

## The privacy boundary — non-negotiable, and it is TESTED, not promised

Every signal crossing `activity -> engine` is a **count, a bool, or a
duration** — never text, a keystroke identity, a window title, a URL, or
clipboard content. `ActiveApp`/`ActiveAppDisplay` are the *only*
machine-derived strings allowed anywhere downstream, and they are an app
identity (`"code"` -> `"VS Code"`), never a window title (ADR 0002, ADR 0009).

This is enforced structurally, not by convention: a reflection-based test
enumerates every field of `Snapshot`, `StateMessage`, and `SaveData` against
an explicit allow-list of `(fieldName, type)`, plus a second check that no
field name contains a forbidden substring (`title`, `text`, `content`,
`clipboard`, `keycode`, `url`, `path`, `document`, ...). See
`app/internal/activity/content_free_test.go`, `app/internal/game/content_free_test.go`,
`app/internal/store/content_free_test.go`.

**Adding a field to any of these types means adding it to that test's
allow-list in the same change.** If you can't justify the field's name and
type in that allow-list, it doesn't ship. This is what makes "privacy is
absolute" (`docs/plan/ROADMAP.md`) a checkable claim instead of a slogan —
a real finding once slipped through anyway (`StateMessage`/`SaveData` were
missing from the structural sweep during a v1 review) and cost a whole
fix-wave PR to close; check that *every* wire/save type your change touches
has a content-free test, not just the ones that obviously do.

## Ship in phased, independently-shippable slices

Each phase in `docs/plan/ROADMAP.md` "must not regress a prior one" and ships
with its own exit criterion. When scoping new work:

1. State the exit criterion as something a command or a screenshot can prove,
   not a feeling ("the Activity modal shows real accumulating counts and
   counters survive restart" — not "activity tracking works").
2. Keep the privacy/honesty ground rules fixed across every phase (don't
   re-derive them per feature — ROADMAP.md's "ground rules" section is the
   standing contract).
3. Land content-only additions (a new catalog item, a new ticker line) without
   touching the WS contract; land contract changes deliberately and update
   `docs/ui-spec.md` first (see the `add-a-menu-modal` skill for the
   contract-extension checklist).

## THE GATE — no visual/UX change is trusted until seen in the real game

Isolated verification has lied to this project **twice**: a hoodie whose
shading crushed to black under the default tint looked fine on a mid-gray
mockup PNG (see `pixel-art-authoring`'s tint-crush lesson), and a character
sprite that read as one hooded figure in isolation actually read as two bald
figures once composited with the real occluding desk props. Both were only
caught by rendering the actual product and looking at it.

**The rule: before calling any visual or UX change done, build the real Go
binary, run it with the fake provider, screenshot the real page with a
headless browser, and look at the image yourself (or ask a vision model if
you're a text-only agent).** Never judge a component in isolation, never
judge only the source (sprite PNGs, CSS, DOM) without rendering it.

### The exact recipe (verified working in this repo)

```bash
# 1. Build (requires Go — this box keeps it at
#    /home/darkmirror/go-toolchain/go/bin if it's not already on PATH)
cd app && go build -o /tmp/dc-verify .

# 2. Run with the FAKE provider — a scripted activity timeline, no real
#    input access needed, deterministic (pure function of elapsed wall time
#    since Start, per app/internal/activity/provider_fake.go)
DEVCOMPANION_FAKE_SCRIPT="type:20s,idle:40s,mouse:15s" \
  /tmp/dc-verify -provider fake -addr 127.0.0.1:8099 &

# 3. Confirm the server actually found its assets before screenshotting —
#    this is the loud-failure path (README "Troubleshooting"): a banner in
#    the scene and this endpoint both surface a bad DEVCOMPANION_ASSETS_DIR
#    instead of silently rendering broken.
curl -s http://127.0.0.1:8099/api/health
#   {"assetsDir":"...", "publicOk":true, "version":"<git sha>[-dirty]"}

# 4. Screenshot the REAL running page at the shipping resolution (640x400,
#    art-direction non-negotiable #7) with headless Chromium
npx --yes playwright screenshot --viewport-size=640,400 \
  --wait-for-timeout=3000 http://127.0.0.1:8099/ /tmp/dc-shot.png

# cleanup
kill %1   # or: pkill -f dc-verify
```

`-provider fake` / `DEVCOMPANION_FAKE_SCRIPT` always wins over the OS-native
provider, so this reproduces exactly on a Linux dev box even though the
shipping target is macOS — the frontend is plain HTML/CSS, so what renders
here is pixel-identical to what a Mac renders (ADR 0011's whole reason for
the engine pivot).

**Verify at the real DEFAULT state, not a seeded best-case.** The tint-crush
bug specifically hid at a neutral mockup and appeared at the default tint in
the real app; verify a fresh-player default first, then whatever seeded state
the change actually needs to exercise (e.g. `type:5s` then immediately open
the store to see a specific card).

### Judging the screenshot

- **You have native vision (a Claude agent): read the image yourself.** That
  judgment is better than a gateway vision model's. Look for: does the
  described change actually appear; does anything overlap/clip/wrap that
  shouldn't (a real bug: the `[S] STORE` button once clipped in the title
  bar); are numbers formatted (a real bug: raw floats like
  `"38.19600152587..."` rendering instead of an integer).
- `scripts/visual-check.py /tmp/dc-shot.png "<question>"` is a fallback for
  text-only agents. Ask it to *describe*, never a leading yes/no. If it
  contradicts what you can plainly see, trust your eyes and say so — one
  vision-model provider in this project's history returned canned,
  image-independent critiques on open-ended prompts.
- A defect found here is a real regression, not a nitpick — log it and fix it
  before calling the feature done, the same way the v0.4 Wave 2 gate found
  three defects and dispatched fixes before re-verifying.

## Also run before calling anything done

```bash
cd app
go build ./...     # compiles clean
go vet ./...
go test ./...       # -race is worth it for anything touching the WS hub/engine
```

If your change touches `Snapshot`, `StateMessage`, or `SaveData`: re-read the
relevant `content_free_test.go` and confirm your new field is on its
allow-list (or, if it shouldn't be there at all, that's the test telling you
something is wrong with the change, not with the test).
