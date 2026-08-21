// Environment flags shared across layers. `?dev=1` is the documented
// dev-mode contract (see src/dev/dev-tools.ts): it seeds hardcoded catalog
// + state instead of connecting over WS, and exposes window.devApply for
// an external harness to drive specific states for a screenshot.
export const DEV_MODE = new URLSearchParams(location.search).get('dev') === '1';
