# Security Policy

Thank you for helping keep Dexel and its users safe.

## Supported versions

Dexel is at an early stage. Security fixes land on the latest release and the
tip of `main`; older tags are not separately patched.

| Version | Supported          |
| ------- | ------------------ |
| 0.1.0   | :white_check_mark: |
| < 0.1.0 | :x:                |

## Reporting a vulnerability

**Please do not report security vulnerabilities through public GitHub issues,
discussions, or pull requests.**

Instead, report privately via GitHub's
[**Security Advisories**](https://github.com/jawwadzafar/dexel/security/advisories/new)
("Report a vulnerability") — this opens a private channel with the maintainer.
If you cannot use that flow, contact the maintainer directly on GitHub,
[@jawwadzafar](https://github.com/jawwadzafar).
<!-- TODO: add a security contact email before going public -->

Please include, as far as you can:

- a description of the issue and its impact;
- the platform (OS/arch) and Dexel version (`dexel status`);
- steps to reproduce, or a proof of concept;
- any relevant logs (see the note on redaction below).

We will acknowledge your report, keep you informed as we investigate, and
credit you when a fix ships unless you prefer to remain anonymous. Please give
us a reasonable window to release a fix before any public disclosure.

## Redacting logs

Dexel's runtime log lives under the state directory (`dexel logs --path` prints
its exact location). By design it never records typed content — only counts and
durations — but please still review anything you attach for paths or details you
would rather not share.

## Security posture

Dexel is a local-only application, and several design decisions exist
specifically to keep it that way:

- **Local by construction.** Everything runs on your machine talking to itself
  over loopback (`127.0.0.1`). Nothing phones out. Binding `-addr` beyond
  loopback is possible but exposes the activity monitor and your save to the
  network — the default keeps it local.
- **The privacy invariant is enforced structurally.** The activity layer records
  *that* input happened — counts and durations — never *what* was typed, never
  clipboard contents, never window titles or URLs. A reflect-based allow-list
  test fails the build if a field that could carry content is ever added to the
  activity `Snapshot`, the WebSocket state, or the save types
  ([ADR 0002](docs/adr/0002-activity-isolation-and-privacy.md),
  [ADR 0009](docs/adr/0009-app-identity-not-titles.md)).
- **Tamper-evident save data.** The SQLite save (`state.db`) is integrity-checked
  on every load via an HMAC; a save that fails its check is never loaded, never
  deleted, and never silently downgraded — it is quarantined aside and the game
  starts fresh, leaving the original recoverable.
- **No elevation, no autostart by surprise.** The installer never uses `sudo`,
  never writes outside your home directory, and never enables login autostart on
  its own — that stays an explicit `dexel autostart enable`.
- **Same-origin WebSocket.** The server verifies the WebSocket `Origin` by
  default; `-insecure-origin` and `-allow-origin` exist for embedded webviews
  and are documented as such.

Reports about any of these boundaries — or ways to bypass them — are especially
welcome.
