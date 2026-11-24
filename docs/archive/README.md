# docs/archive — retired documents and tooling

Files here are **evidence, not contracts**. Nothing in `scripts/`, `Makefile`,
`.github/` or the Go build references them any more. They are kept because they
answer "why is the product like this now?" months later.

**If a document is here, it does not describe the current product.** The live
documents are the ones in `docs/` (see the table at the bottom).

## Retired tooling

| File | Retired in | Why it is kept |
|---|---|---|
| `design-baseline.json` | S7 (native-UI retirement) | The grandfather list of `designlint` violations. It shows what the native UI's design contract actually enforced before the browser client took over; the web client checks the generated token/icon chain instead (`npm run gen:verify`). |
| `design-check.sh` | S7 | The Phase-0 grep checks (`ui.RGBA`/`ui.Hex` literals, font-size literals, …). They only ever scanned `internal/ui`/`internal/uiroot`, which are deleted. |

## Retired documents (native UI)

All of these describe the windowed mygo UI — its surfaces, component rules,
lints, audits and review rounds. The code they describe was deleted by
`S5: retire the native UI` (recoverable from the `pre-s4` tag).

| File | What it was |
|---|---|
| `AGENT-SURFACES.md` | The 12 native surfaces (session thread, reasoning, tool rows, approval/plan/ask dialogs, composer, run status, insights, …). **Its one still-live section moved to [../APPROVAL-SURFACES.md](../APPROVAL-SURFACES.md).** |
| `INJECTION-POINTS.md` | Shell/navigation injection points of `internal/uiroot` |
| `UI-CONFORMANCE.md` | mygo component cheat-sheet and the frame-test workflow |
| `UI-COMPONENT-MIGRATION.md` | The list of hand-drawn components kept instead of official ones, with reasons |
| `DESIGN-LINT.md` | `designlint` rule IDs and the baseline mechanism |
| `UI-FIX-STATUS-v0.8.3.md` | Fix status for the v0.8.3 native UI round |
| `COVERAGE.md` | Coverage report of the native UI period |
| `VERIFICATION.md` | Verification workflow of the native UI period |
| `UI-AUDIT-2026-11-11.md` | Native UI audit, 2026-11-11 |
| `DESIGN-REVIEW.md` | Native UI design review, 2026-11-08 |
| `UI-STYLE-DIRECTION-2026-11-11.md` | Native UI style direction |
| `UI-UX-UPGRADE-2026-11-15.md` | Native UI UX upgrade round |
| `SETTINGS-IA-REVIEW-2026-11-16.md` | Settings information architecture review |
| `USABILITY-REVIEW.md` | Usability review |
| `INTERACTION-DEPTH-ROUND.md` | Interaction-depth round notes |
| `PRODUCT-BUG-REVIEW-2026-11-11.md` | Product defect review |
| `ADVERSARIAL-AUDIT.md` | Adversarial audit |

## Retired migration records (S4/S5)

| File | What it was |
|---|---|
| `s4-retirement-inventory.md` | The read-only pre-study for retiring the native UI: blast radius, line counts, decisions taken before deleting anything. |
| `s4-execution-checklist.md` | The step-by-step execution record (S1–S9 + the post-S9 additions and the review that found six bookkeeping mismatches). This is where "why is the native UI gone and what rolled back where" is answered. |
| `stage0-decouple-contracts.md` | The plan to make `internal/contracts` engine-free. **Done**: `internal/contracts` no longer imports mygo. Its `uinative` subpackage route never shipped — the whole native UI was deleted instead, so the contract layer ended up clean without a subpackage to maintain. |
| `regression-migration.md` | The mapping from `internal/regression` (deleted with S4) to `internal/gateway/e2e_*_test.go` (15 `TestE2E*`). Records which coverage was rescued and which was lost with the UI. |

## Live documents (for contrast)

`docs/WEB-CLIENT.md` (routes, api layer, acceptance list) ·
`docs/TRANSPORT.md` (protocol) · `docs/DESIGN.md` (**native-era** design contract kept for its token values and state semantics — the web visual facts live in `docs/WEB-CLIENT.md` §9 and `web/src/design/`) ·
`docs/APPROVAL-SURFACES.md` (what parks on approval) ·
`docs/ARCHITECTURE.md` (process/boundaries) · `docs/ACP.md` ·
`docs/RELEASE.md` · `docs/SIMPLIFICATION-PLAN.md` ·
`docs/plans/*` (migration decisions and execution records).

The native-UI code itself is not archived here: it lives in the `pre-s4` tag.
`s4-execution-checklist.md` (now in this directory) records the deletion and what
rolling back means.