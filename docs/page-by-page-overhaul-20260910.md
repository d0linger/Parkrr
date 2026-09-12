# Page-by-page overhaul — 2026-09-10

Update 2026-09-12: the implementation is now running locally on port 8099.
See the [completed functional validation](validation-20260912.md) for the expanded
tests, corrected edge cases, deployment checks and remaining verification limits.

## Scope and direction

This continues the initial [shared UI refinement](ui-refinement-20260910.md).
The scope is all 17 operator routes plus login and the public customer portal.
The established Fahrwerk identity is retained, including local fonts, petrol
actions, copper financial emphasis, the parking background, and the planner's
separate dark working surface. No new visual world or generated comp is introduced.

The operating principle is task-first hierarchy: find the record, see its current
state, act directly, then explore history or configuration. Lists stay lists;
detail pages gain section navigation; complex settings are grouped by purpose.
Desktop uses complementary columns where useful; narrow screens follow the same
DOM order and keep actions reachable. Existing one-tap controls, billing semantics,
role gates, immutable invoice content, and planner placement mathematics remain.

## Page-by-page coverage

| Surface | Changes across both refinement passes |
| --- | --- |
| Dashboard | Financial/operational grouping, compact side-by-side desktop charts, concurrent reads, visible partial failures and retry. |
| People list | Responsive contact/action layout, visible result counts, native linked names, clearer search recovery. |
| Person detail | Balance and derivation grouped, section navigation to vehicles/costs/payments/invoices/history, clearer section rhythm. |
| Vehicle list | Responsive actions, stable direct status/payment controls, readable results and task guidance. |
| Vehicle detail | Data and status controls form a desktop workspace; costs, photos, handovers, attachments and history are indexed. |
| Additional costs | Person and payment-state filters, shared payment-state interpretation, responsive amount/action rows. |
| Tariffs and services | Compact contextual creation action, count-bearing catalog selectors with pressed state, wrapping editable rows. |
| Users | Role filter alongside search; shared responsive list and action hierarchy. |
| Billing settings | Grouped issuer/tax and numbering/banking columns; associated labels/help, native numeric validation, inline save feedback. |
| Invoice detail | Document/action separation, keyboard-accessible horizontal containment for line items, preserved print document. |
| Backups | Section navigation, compact status overview, distinct restore section, labeled restore fields; read failures no longer mean “disabled.” |
| Audit log | Labeled date ranges, visible loading/results status, usable retry on initial or subsequent failures. |
| Account settings | Named account region, section navigation, populated/error session region, direct admin destinations for administrators. |
| Calendar | Month/agenda selector, readable event list by day (default on narrow screens), accessible labeled day groups and retry. |
| Garages | Native destination links, explicit hall-opening actions, wrapping rows and clearer initial setup guidance. |
| Garage detail | Linked hall names, wrapping planner actions and clearer no-hall guidance. |
| Hall planner | Proper page heading, labeled mode selection, contextual save guidance, wrapping tools and corrected narrow-screen grid sizing. |
| Login | Explicit sign-in heading and task context, discoverable access/help guidance; existing pending/duplicate protection retained. |
| Customer portal | Responsive information/request grouping, labeled validated forms, native invoice buttons, on-demand QR loading, retry for temporary failures. |

## Verification

The isolated suite serves local assets and synthetic API records, never operator
data. It captures each listed surface at 1440px/light and 390px/dark. The earlier
shared-screen suite additionally checks 390, 768 and 1440px in both themes,
320px long-content layouts, forced colors, short dialogs, and keyboard behavior.

Targeted checks exercise finance/role filters, section-link focus without route
changes, billing validation/save feedback, calendar/planner modes, administrator
read failures, portal validation and deferred QR requests. Selected pages use axe
WCAG A/AA checks; this is not a full accessibility certification.

Final results: **28 browser tests passed**, including all-route reflow checks at
768px and regression assertions for portal field height, invoice scrolling,
audit-filter insets and calendar placeholders. **43 existing authentication and
geometry tests passed**. JavaScript syntax and diff whitespace checks passed.

Commands (from `tests/a11y`):

```sh
node node_modules/@playwright/test/cli.js test page-overhaul.spec.js ui-refinement.spec.js --workers=1 --retries=0
```

From the project root:

```sh
node --check web/static/js/app.js
node --test tests/frontend/auth-redirect.test.js tests/geometry/geometry.test.js
```

Captures are in `.impeccable/review/page-overhaul/` (ignored generated files).
The layout detector's sole finding was the existing modal wrapper's zero padding;
its header/body/footer own their padding. No unrelated identity changes were made
to satisfy that wrapper-level warning.

## Boundaries

This is a page-by-page UI pass, not a rewrite of every nested dialog or backend
workflow. The full set of live SMTP, S3, restore, passkey, real-file upload, print/PDF
and physical-device integrations has not been exercised. Planner mode/layout and
pure geometry tests do not certify every drag/rotation/placement interaction.
At the end of this original visual pass, no production deployment, database
mutation, commit or push had been performed. The subsequent local Docker update
and isolated functional validation are recorded in the linked 2026-09-12 report.

## Finish review

An independent reviewer inspected all 38 named desktop/mobile captures using the
Impeccable finish-review rubric in a fresh generic subagent (the harness has no
specialized reviewer selector). Four material findings were corrected together:

- Reset inherited portal input flex sizing to normal single-line heights.
- Keep invoice headers readable within a horizontally scrollable line-item table.
- Constrain and stack narrow-screen audit filters with equal card insets.
- Isolate calendar placeholders from generic empty-state decoration.

The reviewer re-read the same captures and scored all four fixes **resolved**,
with disposition **ship** for that fix list. This verdict is not a blanket
certification of all application workflows.
