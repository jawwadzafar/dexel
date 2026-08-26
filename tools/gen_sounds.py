#!/usr/bin/env python3
"""Generate every sound effect dexel plays, as tiny deterministic chiptune WAVs.

    python3 tools/gen_sounds.py

THE GENERATOR IS THE SOURCE, exactly as it is for the sprites
(tools/gen_assets.py, docs/art-direction.md): app/public/sounds/*.wav are
build ARTEFACTS of this file, not hand-recorded binary blobs nobody can
regenerate or diff. Everything below is integer/closed-form arithmetic over a
fixed sample rate with no RNG, no wall-clock and no floating-point input from
the environment, so a re-run with no source change rewrites the same bytes —
asserted, not hoped for: main() synthesises every sound TWICE and fails if the
two passes differ by a single byte.

WHY THERE IS NO NOISE CHANNEL. A real 8-bit chip has one, and it is the
fastest way to make a companion that sits on a working developer's desk all
day feel like an arcade cabinet. Every voice here is a square or a triangle
with a pitch slide and a short envelope; the beverage's "fizz" is four
discrete high ticks rather than a white-noise burst for the same reason. The
brief for this whole feature is cozy, not loud.

FORMAT: mono, 22050 Hz, 16-bit PCM. Mono because nothing on screen has a
stereo position; 22050 because the highest partial any voice here produces
sits an octave under that Nyquist and doubling the rate would only double the
bytes go:embed compiles into the binary (app/embed.go). Every file is well
under 40 KB and the whole set is a few tens of KB — see the size line the
self-check prints.

LEVELS. Peaks are declared per sound in SPEC and normalised to hit them
exactly, in dBFS: the two completion jingles at -12 dB (the loudest thing
dexel is allowed to do), the click-reactions between -18 and -15 dB so the
room answering a poke never talks over the room itself. The self-check
re-measures every peak from the written file and fails outside a tight band,
so a botched edit here cannot quietly ship something twice as loud as the
last release.

WHAT IS DELIBERATELY MISSING: a typing sound, an ambient loop, and a UI
click. The first two are the noise a companion app is hated for. The third
was considered and dropped: menu/modal opens are the most-repeated gesture in
this UI (six launchers, all one keypress away), and a tick on every one of
them is exactly the "annoying" this feature is trying not to be. If it is ever
wanted, it belongs here, in SPEC, with its own self-check.
"""

from __future__ import annotations

import hashlib
import math
import struct
import sys
import wave
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
# Output lives INSIDE app/public/, the tree app/embed.go compiles into the
# server binary with go:embed (EMBED-1, docs/plan/ROADMAP.md) — the same
# reason tools/gen_assets.py writes to app/assets/ rather than <repo>/assets.
# The frontend fetches these at "/sounds/<file>" off the same origin the page
# came from (app/frontend/src/render/audio.ts).
SOUNDS = REPO / "app" / "public" / "sounds"

SAMPLE_RATE = 22050
FULL_SCALE = 32767


# --------------------------------------------------------------------------
# Pitch
# --------------------------------------------------------------------------
def midi(note: int) -> float:
    """Equal-tempered frequency for a MIDI note number (69 = A4 = 440 Hz).

    Notes are written as numbers rather than hex frequencies for the same
    reason gen_assets.py names its palette entries instead of inlining hex:
    a transposition is then an obvious edit, and a wrong note reads as a
    wrong note."""
    return 440.0 * (2.0 ** ((note - 69) / 12.0))


C5, D5, E5, F5, G5, A5, B5, C6, E6, G6 = (
    midi(72), midi(74), midi(76), midi(77), midi(79),
    midi(81), midi(83), midi(84), midi(88), midi(91),
)


# --------------------------------------------------------------------------
# Oscillators — the two waveshapes this whole soundtrack is built from
# --------------------------------------------------------------------------
#
# Both take a phase in [0, 1) and return a value in [-1, 1]. Raw, un-band-
# limited shapes: the aliasing that produces IS the 8-bit character, the same
# way gen_assets.py's hard palette edges are the pixel-art character. Nothing
# here is smoothed.
def _square(phase: float, duty: float) -> float:
    """A pulse wave with ZERO MEAN at any duty cycle.

    The obvious `+1 if phase < duty else -1` carries DC as soon as the duty
    leaves 50% — a 25% pulse spends three quarters of its cycle at -1, so it
    averages -0.5. That wastes half the headroom the peak normalisation just
    bought and thumps a speaker on playback (the self-check's check_dc_offset
    caught exactly this while these sounds were being tuned). Centring the
    two levels on 0 — high at (1-duty), low at -duty — and rescaling so the
    larger excursion is still 1.0 keeps the thin-pulse timbre and removes
    the offset."""
    hi, lo = 1.0 - duty, -duty
    scale = 1.0 / max(hi, -lo)
    return hi * scale if (phase % 1.0) < duty else lo * scale


def _triangle(phase: float, _duty: float) -> float:
    p = phase % 1.0
    return 4.0 * p - 1.0 if p < 0.5 else 3.0 - 4.0 * p


WAVES = {"square": _square, "triangle": _triangle}


# --------------------------------------------------------------------------
# One voice
# --------------------------------------------------------------------------
def tone(
    dur: float,
    f0: float,
    f1: float | None = None,
    *,
    wave_name: str = "square",
    duty: float = 0.5,
    attack: float = 0.005,
    curve: float = 1.6,
    level: float = 1.0,
    vibrato_hz: float = 0.0,
    vibrato_depth: float = 0.0,
    tremolo_hz: float = 0.0,
    tremolo_depth: float = 0.0,
    slide_shape: float = 1.0,
) -> list[float]:
    """Render one voice: a waveshape, an optional exponential pitch slide from
    `f0` to `f1`, an attack/decay envelope, and optional vibrato/tremolo.

    PHASE IS ACCUMULATED, never recomputed as `sin(2*pi*f*t)`. That is what
    makes a pitch slide continuous: recomputing phase from the instantaneous
    frequency jumps the waveform every sample and the slide arrives as a
    buzz instead of a slide.

    The envelope is a short linear attack (so the very first sample is 0 and
    the voice cannot start with a click) into a `(1-u)**curve` decay that
    reaches exactly 0 on the final sample (so it cannot END with one either).
    Both edges are re-asserted per FILE by the self-check.
    """
    n = int(round(dur * SAMPLE_RATE))
    if n <= 0:
        return []
    shape = WAVES[wave_name]
    f1 = f0 if f1 is None else f1
    ratio = f1 / f0
    attack_n = max(1, int(round(attack * SAMPLE_RATE)))
    out: list[float] = []
    phase = 0.0
    for i in range(n):
        t = i / n                                    # 0..1 through the voice
        freq = f0 * (ratio ** (t ** slide_shape))
        if vibrato_depth:
            freq *= 1.0 + vibrato_depth * math.sin(2.0 * math.pi * vibrato_hz * i / SAMPLE_RATE)
        if i < attack_n:
            env = i / attack_n
        else:
            u = (i - attack_n) / max(1, n - attack_n)
            env = (1.0 - u) ** curve
        if tremolo_depth:
            env *= 1.0 - tremolo_depth * (0.5 - 0.5 * math.cos(2.0 * math.pi * tremolo_hz * i / SAMPLE_RATE))
        out.append(level * env * shape(phase, duty))
        phase += freq / SAMPLE_RATE
    return out


def silence(dur: float) -> list[float]:
    return [0.0] * int(round(dur * SAMPLE_RATE))


def seq(*parts: list[float]) -> list[float]:
    """Concatenate voices end to end. Every voice ends at envelope 0, so a
    join is always sample-continuous and needs no crossfade."""
    out: list[float] = []
    for p in parts:
        out.extend(p)
    return out


def mix_at(base: list[float], other: list[float], at: float) -> list[float]:
    """Overlay `other` onto `base` starting at `at` seconds, extending `base`
    if it has to. Used only where two voices genuinely overlap."""
    start = int(round(at * SAMPLE_RATE))
    if len(base) < start + len(other):
        base = base + [0.0] * (start + len(other) - len(base))
    for i, v in enumerate(other):
        base[start + i] += v
    return base


# --------------------------------------------------------------------------
# The sounds
# --------------------------------------------------------------------------
#
# One builder per file. Each returns a float list; SPEC's declared peak is
# applied afterwards by normalise(), so a builder never has to reason about
# absolute loudness — only about balance WITHIN itself.


def build_sprint_complete() -> list[float]:
    """THE PAYDAY. Four square notes straight up a C major triad into the
    octave (C5-E5-G5-C6), 0.15s each: the shortest possible "you just got
    paid" and the only place this soundtrack is allowed to be bright. The
    last note slides a few cents up and rings on the same 0.15s slot with a
    slower decay so the jingle lands rather than stopping."""
    return seq(
        tone(0.15, C5, wave_name="square", duty=0.5, curve=1.8),
        tone(0.15, E5, wave_name="square", duty=0.5, curve=1.8),
        tone(0.15, G5, wave_name="square", duty=0.5, curve=1.8),
        tone(0.15, C6, C6 * 1.01, wave_name="square", duty=0.5, curve=1.1, level=0.95),
    )


def build_session_complete() -> list[float]:
    """THE SESSION RESOLVE. Deliberately NOT a louder sprint jingle — a
    session is a longer, calmer thing, so this is a TRIANGLE (far fewer
    partials than a square: rounder, warmer, less "coin") and it RESOLVES
    instead of climbing: E5-G5-C6 up, a B5 leaning-note down, then C6 held
    three times as long as anything in the sprint jingle. Five notes, 0.90s.

    The two files have to be distinguishable in a blind listen, which is why
    they differ on all three axes at once — waveshape, contour and length —
    rather than only on pitch."""
    return seq(
        tone(0.14, E5, wave_name="triangle", curve=1.5),
        tone(0.14, G5, wave_name="triangle", curve=1.5),
        tone(0.18, C6, wave_name="triangle", curve=1.5),
        tone(0.14, B5, wave_name="triangle", curve=1.5, level=0.85),
        tone(0.30, C6, wave_name="triangle", curve=0.9, level=1.0),
    )


def build_react_dexel() -> list[float]:
    """"BWIP?" — the startled blip when you poke the developer. A thin
    (25% duty) square sliding 400 Hz -> 950 Hz over 0.18s: an upward glide is
    heard as a question, which is the whole joke, and it matches the art's
    two-beat flinch-then-lean rather than a flat beep. Short enough
    (0.18s) that the anti-mash cooldown in render/scene.ts is never the thing
    stopping it from overlapping itself."""
    return tone(0.18, 400.0, 950.0, wave_name="square", duty=0.25,
                curve=1.4, slide_shape=0.55)


def build_react_monitor() -> list[float]:
    """A SOFT RATTLE. The monitor's react is a 2px shake, so the sound is a
    wobble, not a hit: a low 190 Hz triangle with +/-18% vibrato and a matching
    amplitude tremolo, both at 30 Hz — a rattle rendered as modulation rather
    than as the noise burst a rattle would "obviously" be. 0.30s, and the
    quietest sound in the set (SPEC: -18 dB), because the monitor is scenery
    and pokes at it should barely register."""
    return tone(0.30, 190.0, 175.0, wave_name="triangle",
                curve=1.2, vibrato_hz=30.0, vibrato_depth=0.18,
                tremolo_hz=30.0, tremolo_depth=0.7)


def build_react_beverage() -> list[float]:
    """A BLOOP AND A LITTLE FIZZ. The bloop is a triangle that dips
    520 -> 300 Hz and comes back to 470 (liquid moving, not a bell), and the
    fizz is FOUR discrete 6ms triangle ticks at descending level over the
    tail — carbonation as four events, not as a white-noise wall (see the
    module docstring). 0.25s total."""
    out = seq(
        tone(0.075, 520.0, 300.0, wave_name="triangle", curve=1.0, attack=0.004),
        tone(0.085, 300.0, 470.0, wave_name="triangle", curve=1.3, attack=0.004),
    )
    for i, (at, lvl) in enumerate(((0.155, 0.34), (0.180, 0.26), (0.205, 0.19), (0.228, 0.13))):
        out = mix_at(out, tone(0.006, 2100.0 + 260.0 * i, wave_name="triangle",
                               curve=2.2, attack=0.001, level=lvl), at)
    return out


def build_react_buddy() -> list[float]:
    """A CHEERFUL CHIRP. Two quick rising square blips (700->1100 Hz, then
    950->1400 Hz) with a 20ms gap — the same two-beat shape the buddy's own
    react art has, and a bird's answer rather than a machine's. 0.235s."""
    return seq(
        tone(0.095, 700.0, 1100.0, wave_name="square", duty=0.35, curve=1.5, slide_shape=0.7),
        silence(0.020),
        tone(0.120, 950.0, 1400.0, wave_name="square", duty=0.35, curve=1.6,
             slide_shape=0.7, level=0.9),
    )


def build_coin() -> list[float]:
    """THE COIN CHING. dexel's own currency (COIN) answering a click with the
    classic two-note up-flick: a short B5 grace note snapping up into a bright
    E6 that rings out, both squares for the coin-bright timbre a triangle would
    round off. 0.18s total - short enough to fire on every click without the
    anti-mash cooldown ever being the thing that cuts it off - and modest
    (-14 dB): a satisfying "you got a coin" blip, not an arcade payout."""
    return seq(
        tone(0.05, B5, wave_name="square", duty=0.5, curve=1.4),
        tone(0.13, E6, E6 * 1.005, wave_name="square", duty=0.5, curve=1.1),
    )


# --------------------------------------------------------------------------
# THE MANIFEST
# --------------------------------------------------------------------------
#
# name -> (builder, peak_dbfs, min_seconds, max_seconds)
#
# Every file dexel plays is here and nowhere else: the frontend's own table
# (render/audio.ts's SOUND_FILES) names the same six, the duration bounds are
# what the self-check enforces, and cleanup_stale() deletes anything in
# app/public/sounds/ that this dict does not claim — so the directory cannot
# accumulate an orphan WAV that no code plays but every binary embeds.
SPEC: dict[str, tuple[object, float, float, float]] = {
    "sprint_complete.wav":  (build_sprint_complete,  -12.0, 0.50, 0.70),
    "session_complete.wav": (build_session_complete, -12.0, 0.80, 1.00),
    "react_dexel.wav":      (build_react_dexel,      -15.0, 0.12, 0.24),
    "react_monitor.wav":    (build_react_monitor,    -18.0, 0.24, 0.36),
    "react_beverage.wav":   (build_react_beverage,   -16.0, 0.20, 0.30),
    "react_buddy.wav":      (build_react_buddy,      -15.0, 0.18, 0.30),
    "coin.wav":             (build_coin,             -14.0, 0.15, 0.22),
}

# The two bounds every sound must sit inside whatever SPEC declares for it —
# the "cozy, not arcade-loud" rule made mechanical. -9 dB is the ceiling
# (nothing may approach full scale); -24 dB the floor (nothing may be so
# quiet it is a mystery rather than a sound).
PEAK_CEILING_DBFS = -9.0
PEAK_FLOOR_DBFS = -24.0
# How close normalise() has to land on the declared peak. Normalisation is
# exact in floats; this band is the 16-bit rounding error and nothing else.
PEAK_TOLERANCE_DB = 0.05
# No file may be bigger than this. Every byte here is compiled into the
# shipped binary by app/embed.go, and the whole point of the format choices
# above is that a sound effect is cheap.
MAX_FILE_BYTES = 48 * 1024


def normalise(samples: list[float], peak_dbfs: float) -> list[int]:
    """Scale to hit `peak_dbfs` exactly, then quantise to 16-bit PCM.

    Rounding is symmetric half-away-from-zero rather than Python's banker's
    round(), so the quantisation is a pure function of the float value and
    cannot depend on the parity of a neighbouring sample."""
    peak = max((abs(v) for v in samples), default=0.0)
    if peak <= 0.0:
        raise AssertionError("silent buffer — a builder produced nothing")
    target = (10.0 ** (peak_dbfs / 20.0)) * FULL_SCALE
    gain = target / peak
    out: list[int] = []
    for v in samples:
        x = v * gain
        q = int(x + 0.5) if x >= 0.0 else -int(-x + 0.5)
        out.append(max(-FULL_SCALE, min(FULL_SCALE, q)))
    return out


def encode_wav(pcm: list[int]) -> bytes:
    """A canonical 16-bit mono RIFF file. `wave` writes a fixed 44-byte
    header from the parameters below, so identical PCM gives identical
    bytes — nothing here carries a timestamp or a tool version."""
    import io

    buf = io.BytesIO()
    with wave.open(buf, "wb") as w:
        w.setnchannels(1)
        w.setsampwidth(2)
        w.setframerate(SAMPLE_RATE)
        w.writeframes(struct.pack("<%dh" % len(pcm), *pcm))
    return buf.getvalue()


def synthesise(name: str) -> tuple[bytes, list[int]]:
    builder, peak_dbfs, _lo, _hi = SPEC[name]
    pcm = normalise(builder(), peak_dbfs)          # type: ignore[operator]
    return encode_wav(pcm), pcm


def dbfs(value: int) -> float:
    return 20.0 * math.log10(max(1, abs(value)) / FULL_SCALE)


# --------------------------------------------------------------------------
# Self-checks — the same posture as gen_assets.py's: a botched edit here must
# fail loudly at generation time, never ship a sound that is silent, clipped,
# twice as loud as the last one, or half a second longer than the moment it
# is attached to.
# --------------------------------------------------------------------------
def check_duration(name: str, pcm: list[int]) -> str:
    _b, _p, lo, hi = SPEC[name]
    secs = len(pcm) / SAMPLE_RATE
    assert lo <= secs <= hi, f"{name}: {secs:.3f}s outside the declared {lo}-{hi}s"
    return f"{name:<22} {secs:.3f}s  (bounds {lo}-{hi}s)"


def check_peak(name: str, pcm: list[int]) -> str:
    _b, declared, _lo, _hi = SPEC[name]
    measured = dbfs(max(pcm, key=abs))
    assert abs(measured - declared) <= PEAK_TOLERANCE_DB, (
        f"{name}: peak {measured:.2f} dBFS, declared {declared:.2f} "
        f"(tolerance {PEAK_TOLERANCE_DB} dB)"
    )
    assert measured <= PEAK_CEILING_DBFS, (
        f"{name}: peak {measured:.2f} dBFS is above the {PEAK_CEILING_DBFS} dBFS "
        "ceiling — this soundtrack is cozy, not arcade-loud"
    )
    assert measured >= PEAK_FLOOR_DBFS, (
        f"{name}: peak {measured:.2f} dBFS is below the {PEAK_FLOOR_DBFS} dBFS floor "
        "— inaudible is not the same as subtle"
    )
    assert max(abs(v) for v in pcm) < FULL_SCALE, f"{name}: clipped at full scale"
    return f"{name:<22} peak {measured:+.2f} dBFS (declared {declared:+.1f})"


def check_edges(name: str, pcm: list[int]) -> str:
    """No click at either end. Every voice's envelope starts and ends at 0,
    so the first and last samples must be tiny relative to the peak; a
    non-zero edge is heard as a tick and is the single most common way a
    hand-tuned envelope goes wrong."""
    peak = max(abs(v) for v in pcm)
    limit = max(2, int(peak * 0.02))
    assert abs(pcm[0]) <= limit, f"{name}: first sample {pcm[0]} > {limit} — starts with a click"
    assert abs(pcm[-1]) <= limit, f"{name}: last sample {pcm[-1]} > {limit} — ends with a click"
    return f"{name:<22} edges {pcm[0]:+d} / {pcm[-1]:+d} (limit +/-{limit})"


def check_dc_offset(name: str, pcm: list[int]) -> str:
    """A square wave with an off-centre duty cycle carries DC, which wastes
    headroom and thumps a speaker on playback. Mean must sit near zero
    relative to the peak."""
    peak = max(abs(v) for v in pcm)
    mean = sum(pcm) / len(pcm)
    assert abs(mean) <= peak * 0.12, (
        f"{name}: DC offset {mean:.1f} is more than 12% of the {peak} peak"
    )
    return f"{name:<22} DC {mean:+.1f} ({100.0 * abs(mean) / peak:.1f}% of peak)"


def check_deterministic(name: str) -> str:
    """The load-bearing one. Synthesise the whole file a SECOND time and
    require byte equality: this is what proves there is no RNG, no clock and
    no dict-ordering dependence anywhere in the builder, which is the promise
    "the generator is the source" actually rests on."""
    a, _ = synthesise(name)
    b, _ = synthesise(name)
    assert a == b, f"{name}: two synthesis passes produced different bytes"
    return f"{name:<22} sha256 {hashlib.sha256(a).hexdigest()[:16]} (stable across passes)"


def check_size(name: str, data: bytes) -> str:
    assert len(data) <= MAX_FILE_BYTES, (
        f"{name}: {len(data)} bytes exceeds the {MAX_FILE_BYTES}-byte budget "
        "(every byte is embedded in the shipped binary)"
    )
    return f"{name:<22} {len(data):>6} bytes"


def cleanup_stale(expected: set[str]) -> list[str]:
    removed = []
    for path in sorted(SOUNDS.glob("*.wav")):
        if path.name not in expected:
            path.unlink()
            removed.append(path.name)
    return removed


def main() -> int:
    SOUNDS.mkdir(parents=True, exist_ok=True)
    ok = True
    written: dict[str, bytes] = {}
    pcms: dict[str, list[int]] = {}

    print(f"-- synthesising {len(SPEC)} sounds at {SAMPLE_RATE} Hz mono 16-bit --")
    for name in SPEC:
        data, pcm = synthesise(name)
        (SOUNDS / name).write_bytes(data)
        written[name] = data
        pcms[name] = pcm
        print(f"  wrote {name}")

    for title, run in (
        ("durations", lambda n: check_duration(n, pcms[n])),
        ("peak levels", lambda n: check_peak(n, pcms[n])),
        ("envelope edges (no clicks)", lambda n: check_edges(n, pcms[n])),
        ("DC offset", lambda n: check_dc_offset(n, pcms[n])),
        ("determinism (two independent passes)", check_deterministic),
        ("file sizes", lambda n: check_size(n, written[n])),
    ):
        print(f"\n-- {title} --")
        for name in SPEC:
            try:
                print(" ", run(name))
            except AssertionError as exc:
                ok = False
                print("  FAIL:", exc)

    print("\n-- stale file cleanup --")
    removed = cleanup_stale(set(SPEC))
    if removed:
        for name in removed:
            print(f"  removed {name}")
    else:
        print("  nothing stale found")

    on_disk = {p.name for p in SOUNDS.glob("*.wav")}
    print("\n-- manifest --")
    if on_disk != set(SPEC):
        ok = False
        missing = set(SPEC) - on_disk
        extra = on_disk - set(SPEC)
        if missing:
            print("  MISSING:", sorted(missing))
        if extra:
            print("  UNEXPECTED EXTRA:", sorted(extra))
    else:
        total = sum(len(d) for d in written.values())
        print(f"  app/public/sounds/ contains exactly {len(SPEC)} files, "
              f"{total} bytes total ({total / 1024.0:.1f} KB)")

    print("\n-- sha256 (for the record; not pinned) --")
    for name in SPEC:
        print(f"  {name:<22} {hashlib.sha256(written[name]).hexdigest()}")

    if not ok:
        print("\nSELF-CHECK FAILED", file=sys.stderr)
        return 1
    print("\nself-check passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
