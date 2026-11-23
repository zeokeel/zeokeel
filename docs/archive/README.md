# docs/archive — retired tooling and reports

Files here are **evidence, not live tooling**. Nothing in `scripts/`, `Makefile`
or `.github/` references them any more; they are kept because they document how
the product used to be checked or designed, which is what answers "why is it
like this now?" months later.

| File | Retired in | Why it is kept |
|---|---|---|
| `design-baseline.json` | S7 (native-UI retirement) | The grandfather list of `designlint` violations. It shows what the native UI's design contract actually enforced before the browser client took over; the web client checks the generated token/icon chain instead (`npm run gen:verify`). |
| `design-check.sh` | S7 | The Phase-0 grep checks (`ui.RGBA`/`ui.Hex` literals, font-size literals, …). They only ever scanned `internal/ui`/`internal/uiroot`, which are deleted. |

The native-UI code itself is not archived here: it lives in the `pre-s4` tag and
was removed by `S5: retire the native UI`. `docs/plans/s4-execution-checklist.md`
records the full deletion and what rolling back means.
