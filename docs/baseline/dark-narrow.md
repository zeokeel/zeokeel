# Dark & narrow screen baselines (M7-D)

Coverage of the five static shell screens (`Home`, `Session`, `Plugins`,
`Settings`, `Automation`) in the dark theme at the design size (1600×1000)
and in a narrow window (1200×800), using the existing `ui.Render` pixel
harness and the DSH dark captures in `baseline/screenshots/`.

## Run

```sh
export PATH=/usr/local/go/bin:$PATH \
       GOCACHE=/data/mygo/.cache/go-build \
       GOMODCACHE=/data/mygo/.cache/gomod \
       GOSUMDB=off CGO_ENABLED=0
go test -count=1 ./internal/shell -run 'TestScreensDarkNarrow|TestScreensBaseline' -v
# one screen:  SCREEN=home go test ./internal/shell -run TestScreensDarkNarrow -v
```

Renders land in `artifacts/screens-dark/<screen>.png` and
`artifacts/screens-narrow/<screen>.png`; red diff overlays land in
`artifacts/diff-dark/` and `artifacts/diff-narrow/` when a baseline exists.

`TestScreensDarkNarrow` sets the theme through a render wrapper
(`c.SetTheme(design.Dark().Theme())` before `app.View`; `App.View` follows
`c.Theme().Dark` while the persisted appearance is the default `system`),
renders every screen at both sizes, and compares with
`pixeltest.CompareExcluding` using the same masks as `TestScreensBaseline`
(tolerance 12): the brand row `0,0–280,64`, the dynamic sidebar list
`0,204–280,1000`, and the DSH hover tooltip `276,210–520,410`.

## Results

Masked diff is the ratio after those masks. Budgets are per-screen
regression gates, not DSH-parity goals: the current dark captures were taken
with the right sidebar open and a different session state, which the static
renderer (`Ctrl == nil`) does not draw. See “Budget derivation” below.

| Screen | Theme | Size | DSH baseline | Masked diff | Budget | Notes |
| --- | --- | --- | --- | ---: | ---: | --- |
| Home | dark | 1600×1000 | `102-home-dark.png` | 6.42% | 9.00% | baseline has the right Start pane open; static render leaves it closed |
| Home | dark | 1200×800 | `105-home-dark-narrow.png` | 6.86% | 9.00% | same right-pane delta, proportionally larger |
| Session | dark | 1600×1000 | `69-session-dark.png` | 19.39% | 23.00% | baseline session history + open right pane differ from the static demo session |
| Session | dark | 1200×800 | `70-session-dark-narrow.png` | 18.44% | 22.00% | same, narrow |
| Plugins | dark | 1600×1000 | `103-plugins-dark.png` | 2.98% | 6.20% | structurally aligned |
| Plugins | dark | 1200×800 | — | — | — | no DSH capture yet |
| Settings | dark | 1600×1000 | `104-settings-dark.png` | 2.80% | 7.70% | 800×800 modal over the dark theme’s 0.5 black scrim |
| Settings | dark | 1200×800 | — | — | — | no DSH capture yet; modal covers the sidebar boundary |
| Automation | dark | 1600×1000 | — | — | — | no DSH capture yet |
| Automation | dark | 1200×800 | — | — | — | no DSH capture yet |

Baselines that do not exist are skipped with a log line; the renders are
still written so they can be inspected.

## Dark token assertions

`TestScreensDarkNarrow` samples fixed coordinates on every frame and fails
if the painted pixels are not the `design.Dark()` tokens:

| Coordinate | Expected | Screen(s) |
| --- | --- | --- |
| `(10,450)` | `Dark().BgSidebar` `#1B1B1C` | all (Settings: blended through the 0.5 scrim) |
| `(1500,900)` @1600 / `(1150,700)` @1200 | `Dark().BgBase` `#151517` | all (Settings: scrimmed) |
| `(278,450)` | last sidebar interior column | all at 1600; all but Settings at 1200 |
| `(280,450)` | first main-content column (sidebar stays 280px) | all at 1600; all but Settings at 1200 |
| `(w-1,450)` | `Dark().BgBase` | all: main content still reaches the right edge in 1200×800 |

`Settings` is a full-window modal: its backdrop scrim dims the sidebar and
main background. The scrim is theme-aware (`design.Tokens.Scrim()`): 0.24
in the light theme and 0.5 in the dark one, matching DSH's dark capture
(`104-settings-dark.png`). The expected colors are the tokens composited
over black through the same `ui.Color.Over` path the renderer uses. The 800×800
panel is centered, so on the 1200×800 frame it covers the sidebar boundary;
that boundary is asserted on the 1600×1000 frame instead.

Token audit against `baseline/css/{45-home,46-plugins,47-settings,15-session}-dark.json`
confirms `BgBase #151517`, `BgSidebar #1B1B1C`, `LabelPrimary #F9FAFB`,
`LabelSecondary #CFD3D6`, and `LabelTertiary #ADB2B8` exactly. No token
change was needed for this task. The sidebar divider now uses `BorderL3`
(light 0.12, dark 0.16), matching the capture, and the Settings/session
search scrims are theme-aware (0.24 light, 0.5 dark). Remaining small
deltas are pre-existing and out of scope: `Dark().BgHover` is white 0.07
where the capture uses 0.08, and `Dark().Accent` is `#60A5FA` where dark
captures show `#7AAAFF`.

## Budget derivation

```
budget(screen, size) = max(2 × light masked diff, measured dark diff + 3pp)
```

Light references from `TestScreensBaseline` (same masks, 1600×1000):
Home 1.00%, Session 6.48%, Plugins 3.20%, Settings 2.38%, Automation ~1.4%.
The dark captures' structural delta (open right sidebar, different session
state) dominates their diff, so the +3pp headroom over the measured value is
what makes the gate meaningful: a moved pane, lost dark theme, or broken
layout moves far more than 3 percentage points, while text antialiasing
drift does not.

## Screens lacking a DSH baseline, and what to capture next

1. **Automation, dark, 1600×1000** — capture the tasks page with `Ctrl`-free
   static content (or the demo task list) and compare like Plugins.
2. **Automation, dark, 1200×800** — narrow variant of the same.
3. **Plugins, dark, 1200×800** — the 960px column already overflows the
   920px main area at 1200; capture to decide whether it should reflow.
4. **Settings, dark, 1200×800** — note the modal covers the sidebar
   boundary; capture to verify the centered 800px panel and nav column at
   narrow widths.
5. **Home/Session, dark, 1600×1000 and 1200×800 with the right sidebar
   closed** — the existing `102/105/69/70` captures are the main reason the
   dark diffs are 12–31% while their light counterparts are 1–6%. Recapture
   in the static-render state, or add a dedicated live-right-pane baseline
   so both states are covered.

## Known gaps

- The static renderer never opens the right sidebar (`rightOpen` needs a
  live `Ctrl`), so the existing Home/Session dark captures cannot converge
  below the right-pane delta without new captures.
- The dark session capture shows a different conversation; session content
  is intentionally not part of the static demo fixture.
- Settings narrow cannot assert the sidebar boundary by pixel because the
  modal overlays `x=200…1000`; the 1600 frame covers it.
- Font rasterization (Chromium LCD AA vs mygo headless grayscale AA) keeps a
  small antialiasing floor even on structurally aligned screens; the
  budgets allow for it.
