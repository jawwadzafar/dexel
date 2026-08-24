# 0017 — Sessions: intention as a lens, names on the config side, a chained-MAC append log, rewards outside the economy

Status: accepted (2026-08-22, P2 design pass) · Realises PRODUCT-EVOLUTION Bet 1 (`docs/plan/PRODUCT-EVOLUTION.md` §3, §5 Phase P2) · Extends ADR 0016 (the hybrid's second half) and ADR 0014 (config/state split) · Honours ADR 0002/0009 (privacy), ADR 0005/0008 (economy), ADR 0010 (honest mechanics) · Companion design: `docs/plan/P2-design.md`

## Context

Dexel's loop is complete and honest: real content-free activity → work → sprints
→ Dev Cash → a wardrobe that visibly changes the scene. What it has never had is
**intention going in** and **occasion coming out**. Coins simply appear while you
type. PRODUCT-EVOLUTION's thesis names the missing verbs —
**INTENTION → HONEST REFLECTION → MEMORY** — and calls Sessions "the keystone of
the whole thesis" because it is the container that turns silent accrual into
*"I sat down to work with Dexel, and here's what we did together."*

Everything a session needs is **already on the boundary**. `engine.TickResult`
emits `KeystrokeDelta`, `MouseActive`, `FocusSessionsCompleted`, `FocusRunSeconds`,
`AppSwitches` and an honest `Mood`; `Game.recordStats` already sums all of them
into `today`/`lifetime` buckets every tick, `StoreOpen` or not. So the whole of
P2 is **a lens over tracking that already happens** — not a new observation, not
a new signal, not a new provider capability. `activity.Snapshot` stays at five
fields, and `content_free_test.go`'s `NumField() == 5` is the privacy proof that
nothing was added.

Four decisions in this space are genuinely load-bearing, and each has a way to
get it wrong that would erode something already settled: where the user-typed
project **name** lives, what a session **rewards**, when a session **ends**, and
how the log stays **tamper-evident**.

## Owner-decision forks (five, all with a recommended default — P2 ships on the defaults)

1. **Where does the optional project name live?** → **config.json, keyed by
   session id** (Decision 2). Alternatives: in the MAC'd log row as an
   "ADR 0014 category" field; or no per-session names at all in v1.
2. **Which schema number does P2 take?** → **5 → 6**, and PR-5 (pause) becomes
   6 → 7 (Decision 6). Only one schema-bumping task may be in flight
   (`dev_docs/production-runtime/MIGRATION_PLAN.md` sequencing constraint 3).
3. **What does completing a session reward?** → **nothing economic**: a counter,
   a "sessions this week" number, and the celebration (Decision 3).
4. **When does a session end by itself?** → **survives restarts; 2 h idle
   auto-end with the end backdated to the last observed activity (global
   providers only); 16 h hard cap** (Decision 4).
5. **Are very short sessions recorded?** → **no**: under 60 s a session is
   discarded, not logged, not counted, never scolded (Decision 3).

## Decision

**1. A session is a LENS, never a requirement, and the automatic loop is
untouched.** No code path in `Engine.Tick` or `Game.Tick` behaves differently
because a session is open. Earning, sprints, moods, the anti-mash clamp and
every A1/A2/A3 counter are byte-identical with and without a session — asserted
by a unit test that runs the same tick sequence both ways and compares the whole
economy. Sessions are **opt-in punctuation** on an always-on companion, which is
what keeps ADR 0005/0008 and the "witness, not tracker" thesis intact. A user who
never opens the Sessions modal loses nothing.

Session accounting is **delta-of-lifetime**, not a parallel tally: at
`SESSION_START` the session records a **baseline** copy of the `lifetime`
`StatCounters`, and its numbers are always `watermark − baseline`. That makes
"session counts ⊆ global counts" and "no double-counting" true **by
construction** rather than by a runtime check, and it survives restarts and
midnight for free (the `lifetime` bucket never resets, unlike `today`). The only
two per-session accumulators are the ones with no monotonic lifetime counter to
subtract from — coins earned and the longest focus block — and the rule is stated
once so it cannot drift: *delta where a lifetime counter exists, accumulator only
where one does not, never both for the same number.*

Sessions follow the **analytics** rule, not the economy rule, on the STORE_OPEN
gate: shopping seconds count toward a session exactly as they already count
toward `today`/`lifetime` (`game.go`'s own reasoning — "a few seconds of shopping
*inside* a tracked session"), while coins provably do not accrue because the
economy is frozen. Under PR-5's pause the session survives, its counters freeze
with the provider stopped, and `pausedSeconds` (which PR-5 makes a lifetime
counter) joins the delta set for free.

**2. The optional project name is CONFIG, kept out of the protected store, and
the log references it by an integer id.** `config.json` gains
`sessionNames: { "<id>": "<name>" }`; the `sessions` table carries a plain
`INTEGER` id and no text the user typed. This upholds ADR 0016's explicit
warning — *"P2's optional project name is CONFIG, not a log column… this is the
boundary most at risk of being crossed by accident"* — and it is chosen over
putting the name in the MAC'd row for three reasons that survive scrutiny:

- **A project name is closer to work content than a pet name is.** ADR 0014
  allow-listed the Dexel's name as "a *different category* from observed
  activity — data the user deliberately writes about their own pet." A
  *timestamped series* of project names is a work journal: it answers *what you
  were working on, and when*. That is the same artifact ADR 0013 refused when it
  dropped hourly buckets ("a daily count says you worked 4 hours; an hourly
  profile says *when*"). A session says you worked 90 minutes; a **named,
  timestamped** session says what on. Free text of that shape does not belong in
  the file whose structural test exists to prove it holds none.
- **Nothing is priced on a name, so nothing needs it protected.** MAC coverage
  buys anti-cheat. A name has no economic value; signing it costs the user the
  ability to edit or delete their own words, which ADR 0014 deliberately granted
  them. Under this decision a user purges what they were working on by editing a
  plain JSON file, and the honest counts survive; under the alternative they
  would have to tamper a MAC'd row and take an economy reset with it.
- **The cheating argument is a wash.** The log is MAC-chained either way, so an
  edit is detected either way. There is no integrity gain to trade the privacy
  and editability away for.

The proof is a test, not a promise: `TestSessionNameNeverReachesTheProtectedSave`
marshals both `store.Snapshot(g)` and the appended session row's payload and
asserts the literal name string appears in neither — the direct analogue of P1's
`TestSetNameNeverReachesTheProtectedSave`. Rejected alternative: **no names in
v1** — it is cheaper, but the name *is* the Intention the thesis asks the user to
supply, and PRODUCT-EVOLUTION names it twice in the P2 scope.

**3. Session-complete rewards live entirely outside the Dev Cash economy.** No
coins, no XP, no sprint progress, ever, from starting or ending a session. ADR
0005's calibration and ADR 0008's "sprint payout is the only coin source" are
touched by nothing in P2 — re-proven by a unit test that ends a session and
asserts `DevCash`, `XP` and `Progress` are unchanged. What the user actually gets
is the **moment**: a summary card (duration, effort counts, coins earned *during*
it, best focus block), a gold session flash composed server-side, and a
`sessionComplete` event; plus a **lifetime session count**, a **"sessions this
week"** number, and a **longest-session personal best** — all *derived from the
verified log*, so none of them needs a second protected counter.

Anti-mash is structural: start/stop grants **nothing at all**, and a session
shorter than 60 s is discarded rather than logged, so mashing the buttons cannot
even inflate a count. "Sessions this week" is presented as a warm fact with **no
target, no comparison and no 0-of-N framing** — the loss-framed-streak trap
PRODUCT-EVOLUTION rejects wholesale. The P4 hook is named, not built: the moments
table will hang "first named session" / "10 sessions" off exactly this data and
grant *earn-only cosmetics*, which is where a session's reward becomes visible on
screen (ADR 0008) without ever entering the priced economy.

**4. End rules are decided by what Dexel can honestly know.** Closing the app
does **not** end a session — the in-progress session lives in the signed
snapshot and resumes on the next boot, because the runtime already tracks
independently of any window and "abandoning a session loses nothing" is a P2
requirement. But an open container must never claim silence as work:

- **Idle auto-end (2 h), with the end backdated to the last observed activity.**
  This is ADR 0010 applied to a container: Dexel knows when it last saw input; it
  does **not** know you were still in your session during the silence, so it
  refuses to claim it. Backdating also makes the reopen-after-a-long-close case
  self-heal — the first tick after loading a stale session ends it at the last
  moment Dexel actually saw the user, instead of inventing a ten-hour session.
  The session's counters are taken at that same watermark, so every number on the
  card stays mutually consistent.
- **Only when the provider can see global input.** With a blind provider
  (Windows today — `PLATFORM_NOTES`) "idle" is unknowable, and ending a session
  because we cannot see would be exactly ADR 0010's forbidden claim. Blind
  providers get the duration cap instead. The check reads
  `engine.TickResult.SeesGlobalInput()` — a **method**, so `TickResult` gains no
  field and P2 adds no data to the engine boundary.
- **A 16 h hard cap**, so a forgotten session can never claim a multi-day
  container.
- **A day rollover never ends a session.** Sessions are not days; the
  delta-of-lifetime model crosses midnight without noticing.

Every automatic end is recorded with an `endReason` from a closed set
(`user | idle | maxDuration`), so the card and the future scrapbook can say "we
closed this one for you" instead of pretending the user did.

**5. Persistence is ADR 0016's hybrid, completed: the in-progress session in the
signed snapshot, finished sessions in a chained-MAC append table.** The
in-progress session is single, mutable and rewritten on every autosave — exactly
the snapshot row's shape — so it becomes two additive fields on `SaveData`
(`session`, `sessionLogHead`) and is MAC-protected for free by ADR 0014's
whole-struct-minus-the-tag preimage. Finished sessions are append-mostly and
grow without bound — exactly a table's shape:

```sql
CREATE TABLE IF NOT EXISTS sessions (
  id       INTEGER PRIMARY KEY,   -- 1-based ordinal; also inside the signed payload
  ended_at TEXT    NOT NULL,      -- denormalized mirror for range reads
  payload  BLOB    NOT NULL,      -- canonical compact JSON of SessionSave
  mac      TEXT    NOT NULL       -- HMAC over (logDomain ‖ 0x00 ‖ prev_mac ‖ 0x00 ‖ payload)
) STRICT;
CREATE INDEX IF NOT EXISTS sessions_ended_at ON sessions(ended_at);
```

The row is a **payload BLOB + tag**, not a column per counter, and that is the
same argument ADR 0016 used to reject normalizing the economy snapshot: the MAC
covers the whole struct minus the tag, so **every future session field is
protected automatically**, with no hand-built row serializer whose forgotten line
would be a silent anti-cheat hole rather than a test failure. `ended_at` is a
**mirror, not an authority** — the identical role `state.schema` plays — kept only
so a range read does not have to parse every payload; a disagreement with
`payload.endedAt` is a tamper signal.

Integrity is a **chain**, with the head bound into the signed snapshot:
`row_mac_i = HMAC(key, "dexel-session-log-v1" ‖ 0x00 ‖ row_mac_{i-1} ‖ 0x00 ‖
payload_i)`, `row_mac_0 = ""`, and `SaveData.SessionLogHead` = the last row's tag.
Appending costs **one** HMAC (never a re-sign of the history), and because the
head is inside the signed payload, **deleting, truncating, reordering or
renumbering rows is detectable** — which a bag of independent per-row MACs would
not be. A distinct domain tag keeps a state payload from ever being replayed as a
log row. Verification is not optional by construction: `Load` is a thin wrapper
over `LoadAll` that verifies the chain and then discards it, so no caller can
skip the check by calling the convenient function.

Tamper policy is **unchanged from ADR 0014/0016**: any chain failure is
`ErrTampered`, the DB is renamed aside to `.invalid` (never deleted), the economy
resets from `game.New()`, the legacy Rust import stays unreachable, and
`config.json` — including the Dexel's name *and* every project name — is
untouched. One new rule, stated precisely because it is the only place "missing"
is not "destroyed": a **missing `sessions` table is the honest empty log iff the
signed head is `""`**; a missing table with a non-empty head is tampering. (The
`state` table is created by every save, so *its* absence is destruction; the
`sessions` table is created lazily by the first append.)

Retention is **unbounded** — a row is ~150 bytes, a decade of ten-a-day is a few
megabytes — with a **UI window** (the wire carries the last 10). The whole chain
is verified once at boot and held in memory; the 1 s state broadcast never
queries SQLite. ADR 0016's deferred long-lived `*store.DB` handle stays deferred:
session writes are a handful a day, and the existing open-write-close with
`synchronous=FULL` is already the shipped pattern. A session end writes the log
row **and** the snapshot row in **one** transaction — a crash between them would
leave a row past the signed head, i.e. a false tamper report on the next boot.

**6. Schema 5 → 6, additive, and P2 takes the number.** A schema-5 payload has
neither new key, so `json.Unmarshal` leaves "no active session" and an empty
head; no `sessions` table exists, which the rule above accepts as the empty log.
`ErrFutureSchema` is preserved unchanged (a schema-7 save is still refused and
renamed `.future`). PR-5 (pause) must therefore take **6 → 7** and add
`pausedSeconds` to the session delta set — recorded here because two agents
bumping to "6" in parallel produces two incompatible schema 6s, and the
production-runtime migration plan already names that hazard.

## Consequences

- **The privacy proof is an absence, again.** No provider change, no
  `activity.Snapshot` field (still five), no new observation of any kind; the
  engine gains one *method* and zero data. Every new column and wire field is a
  count, a duration, an ISO timestamp or a closed-set enum. The one string the
  user typed lives in the unsigned config file, and a test proves it never
  reaches either the snapshot or a log row.
- **`StateMessage` grows one nested block** (`sessions`), allow-listed in
  `game/content_free_test.go` with the field count bumped, and every new nested
  type gets its own `checkExact` block. The active session's `name` is
  allow-listed on the ADR 0014 category citation P1 established — and, as that
  entry already says, allow-listing a *user-authored* string buys nothing for an
  *observed* one.
- **The economy is provably untouched**, which is what makes P2 safe to land
  ahead of P3/P4: no rebalance, no new coin source, no change to the anti-mash
  ceiling, and a unit test that compares a session run against a
  no-session run tick for tick.
- **ADR 0016's hybrid is now real**, and its integrity argument generalized: the
  same "sign the whole struct, mirror only what you must query" pattern now
  covers both halves. P4's `moments` table copies this file's chain verbatim; P6
  reads over both and adds no table.
- **A named residual risk.** Project names in `config.json` can desync from the
  log: deleting the file, or a session id reused after a discarded short session,
  leaves a logged session unnamed or renamed. This is accepted and honest —
  `SESSION_START` overwrites (or clears) its id's entry, an unnamed session
  renders as unnamed, and the counts are never affected. The alternative
  (signing the names) was rejected above.
- **A second named residual risk.** Typing a project name into the modal's input
  accrues a few crumbs of work, because the Sessions modal deliberately gates
  nothing (it is not shopping, and freezing earning while a user declares an
  intention would be perverse). Bounded and quantified: 32 keystrokes ×
  `WorkPerUnitRate 0.008` ≈ 0.26 work units, about 0.5 % of the smallest (50-unit) sprint, and
  the naming keystrokes land *before* the baseline is taken, so a session's own
  numbers are clean by construction. A general "gate while a text input has
  focus" rule is named as a possible follow-up, not built.
- **Deferred, named so they do not creep:** renaming or deleting a completed
  session from the UI; session goals/targets of any kind (a quota by another
  name); session tags/categories; per-session app breakdowns (ADR 0009 territory);
  a sessions row in the A3 History modal (P6's scrapbook is where sessions and
  days merge — duplicating it in P2 buys nothing); exporting a session card
  (PRODUCT-EVOLUTION's local-only social ceiling); multiple concurrent sessions.
