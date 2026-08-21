// ASSET URL PREFIX: "/assets/<file>" — docs/ui-spec.md and
// docs/art-direction.md never name an explicit HTTP prefix for the sprite
// PNGs (only that "the Go server will serve them"), so this frontend uses
// "/assets/<file>". app/main.go's mux serves this route for real (a
// registerAssetsRoute() call that locates the repository's assets/
// directory via internal/assets.Locate() and mounts
// http.FileServer(http.Dir(...)) on it) — no symlink, no dev-only stopgap.
const ASSET_PREFIX = '/assets/';
export function assetUrl(file: string | null | undefined): string | null {
  return file ? ASSET_PREFIX + file : null;
}
