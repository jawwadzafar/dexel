# 0005 — Weighted, coalesced, calibrated activity economy

Status: accepted (v0.2, after a live bug report)

## Context
"Bar goes burr on simple mouse move." The original economy counted raw
events: mice report motion at 125-1000 Hz, typists manage ~5-10 keys/s, and
the ceiling (120 ev/s x 0.05 work) finished a 50-work project in 8 seconds.
The anti-mashing clamp existed but only mouse-wiggling could reach it.

## Decision
Three coordinated changes:
1. Events are summed by weight (`event_weight`): keystroke 1.0, mouse 0.25,
   focus 0.0.
2. Mouse motion is coalesced to at most one event per 100 ms wall-clock
   (`MOUSE_SAMPLE_SECS`), decoupling the signal from polling and frame rate.
3. Recalibration: `MAX_RECENT_RATE` 120 -> 15 (a ceiling a human reaches by
   working), `MIN_WORK_PER_EVENT` 0.05 -> 0.008.

## Consequences
- Real typing: ~21 min per 50-work project. Mouse-only spam: ~42 min — the
  slow path, as the plan's principle always claimed it should be.
- Lesson recorded: the old "anti-mashing test" passed before AND after a 6x
  rebalance — it tested boundedness, not incentives. Balance claims need
  tests that compare strategies, not just assert finiteness.
