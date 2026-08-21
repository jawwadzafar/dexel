# Third-party licenses

dexel is licensed under the [Apache License 2.0](LICENSE) (see [`NOTICE`](NOTICE)).
It bundles or is built with the third-party components listed below, each
under its own license. Full license texts for MIT and ISC are reproduced
here; Apache-2.0 components are covered by the same license text as this
project (see [`LICENSE`](LICENSE)); the OFL is referenced rather than
reproduced in full, per the note below.

| Component | Used for | License |
|---|---|---|
| [NES.css](https://nostalgic-css.github.io/NES.css/) | Retro/8-bit CSS framework for the frontend UI (`app/public/css/nes.min.css`) | MIT |
| [Press Start 2P](https://fonts.google.com/specimen/Press+Start+2P) | Pixel-style display font for the frontend UI (`app/public/fonts/PressStart2P.woff2`) | SIL Open Font License 1.1 |
| [nhooyr.io/websocket](https://github.com/coder/websocket) | WebSocket server implementation used by the Go backend to broadcast game state | ISC |
| [esbuild](https://esbuild.github.io/) | Bundles and minifies the TypeScript frontend source into `app/public/js/game.js` | MIT |
| [TypeScript](https://www.typescriptlang.org/) | Source language / compiler for the frontend (type-checking via `tsc`) | Apache License 2.0 |

## NES.css — MIT License

```
MIT License

Copyright (c) NES.css contributors

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

## Press Start 2P — SIL Open Font License, Version 1.1

Press Start 2P is licensed under the SIL Open Font License, Version 1.1,
available at <https://openfontlicense.org/open-font-license-official-text/>.

**Redistribution requirement:** the OFL requires that its full license text
accompany the font whenever it is redistributed — the font may not be
redistributed under any other license, and the copyright notice(s) and
license text must be retained in all copies. If `PressStart2P.woff2` ships
in a distribution of this project, the OFL 1.1 full text must be included
alongside it (e.g. as `app/public/fonts/OFL.txt`); it must not be dropped
just because it isn't reproduced verbatim in this file.

## nhooyr.io/websocket — ISC License

```
Copyright (c) 2023 Anmol Sethi <hi@nhooyr.io>

Permission to use, copy, modify, and distribute this software for any
purpose with or without fee is hereby granted, provided that the above
copyright notice and this permission notice appear in all copies.

THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES
WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF
MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR
ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN
ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF
OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.
```

## esbuild — MIT License

```
MIT License

Copyright (c) 2020 Evan Wallace

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

## TypeScript — Apache License 2.0

TypeScript is licensed under the Apache License, Version 2.0 — the same
license as this project. See [`LICENSE`](LICENSE) for the full text.
