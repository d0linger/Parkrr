# UI refinement — 2026-09-10

## Direction

Preserve Parkrr's existing Fahrwerk identity: petrol accents, copper financial
signals, established typography, and the parking-space background. Prioritize
readable operational information and quick actions over a new visual language.

The dashboard now groups financial metrics, operational counts, and charts into
distinct responsive sections. Desktop charts share a row; small screens keep
amounts readable. List actions wrap below content on narrow screens, and page
headings and controls can wrap without clipping. Noninteractive metrics no longer
move on hover.

## Implemented

- Refined heading scale, spacing, metadata readability, currency layout, field
  sizing, and mobile action placement in light and dark themes.
- Added visible list counts, distinct search-empty feedback, search reset, and
  keyboard focus after pagination. The skip link focuses content without changing
  the application route.
- Added route busy states, retry actions, and partial-dashboard failure notices.
  Optional dashboard responses tolerate missing data, and stale routes cannot
  overwrite the latest view.
- Guarded login and form submissions against duplicates. Pending forms cannot be
  dismissed by Escape; failures retain entered values. Short dialogs keep their
  action footer reachable while the body scrolls.
- Improved coarse-pointer target sizes, forced-color boundaries and selected
  states, narrow layouts, and overflow handling.
- Parallelized independent dashboard requests, reused the currency formatter, and
  moved segmented-control indicators with transforms and frame-coalesced resize
  updates.

## Validation

The isolated browser suite uses local static assets and synthetic API fixtures;
it does not depend on a live database. It covers 17 checks, including light/dark
previews at 390, 768, and 1440 pixels, 320-pixel long-content cases, a 568-by-320
dialog, keyboard navigation, forced colors, failed and delayed requests, and
duplicate submission guards. Main-screen previews cover the dashboard, people,
vehicles, finance, tariffs, garages, and settings. Long-content checks include
axe WCAG A/AA assertions for serious and critical violations; this is not a full
accessibility certification.

Existing frontend authentication and geometry tests: 43 passed. JavaScript syntax
and whitespace checks passed.

With a synthetic 120 ms delay per API response, five dashboard requests previously
started over roughly 590 ms; they now start within 0–3 ms of one another. Observed
dashboard readiness in the local light-theme preview tests improved from roughly
1.23 seconds to 0.59–0.72 seconds. These are controlled test observations, not field
Core Web Vitals or real-device performance measurements.

The Impeccable detector was run once. Existing decorative rails, background
motifs, shadows, and unrelated progress/disclosure animations were retained as
part of the incumbent design. The segmented-control bounce was replaced with a
quieter ease-out transition.

## Reproduce

From `tests/a11y`, with dependencies and Chromium installed:

```sh
node node_modules/@playwright/test/cli.js test ui-refinement.spec.js --workers=1 --retries=0
```

From the project root:

```sh
node --check web/static/js/app.js
node --test tests/frontend/auth-redirect.test.js tests/geometry/geometry.test.js
```

At the end of this original visual pass, changes were in source only. The later
local Docker update and expanded functional verification are documented in
[validation-20260912.md](validation-20260912.md), including the remaining limits
for live integrations and physical devices.
