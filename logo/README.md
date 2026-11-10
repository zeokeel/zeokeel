# ZeoKeel logo sources

This directory holds the **only** logo sources of the project:

| File | MD5 | Use |
| --- | --- | --- |
| `logo.svg` | `f9c42c883ec16b804c2c241d4cf325f3` | app icon: the mark on the white rounded container |
| `logo-dark.svg` | `56c2a571d6777d0add06045a9ab57bfa` | UI mark only: the mark on the black rounded container |

Both are copied **byte for byte** from the official brand channel
(`/data/ZeoKeel/brands/official/{logo.svg,logo-dark.svg}`, listed as
`brand.logo.light` / `brand.logo.dark` in that channel's `channel.json`).
They are square 1254×1254 SVGs on the same 1254-unit grid, with a 180-unit
rounded container: the light file paints it white and the dark file paints it
black. The light version is the app icon (installers, Windows/macOS
bundles, desktop entry); the dark version is the in-app UI mark for dark
surfaces. Do not edit either file here — update the official channel and
copy both again, so light and dark never drift apart.

## The single-source rule

These two SVGs are the **only** logo/icon sources in the repository. Every
raster logo or icon is derived, deterministically, by
[`scripts/gen-icons.sh`](../scripts/gen-icons.sh):

| Output | Size | From |
| --- | --- | --- |
| `assets/icon.png` (mygo.json `icon`, packaging) | 1024×1024 | `logo/logo.svg` |
| `internal/brand/mark.png` (embedded light UI mark) | 48×48 | `logo/logo.svg` |
| `internal/brand/mark-dark@2x.png` (embedded dark UI mark) | 96×96 | `logo/logo-dark.svg` |

Generated files are build outputs: `assets/` is gitignored, and the two
`internal/brand/*.png` files are produced by the script, never hand-edited.
No other logo, wordmark, icon, or raster copy may be added to the
repository — add a render step to `scripts/gen-icons.sh` instead (and update
this table).
