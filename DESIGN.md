---
name: Parkrr Fahrwerk
description: Slate-and-petrol operating system for self-hosted vehicle storage, from the hall plan to the invoice, with standalone review workbenches in the same world.
colors:
  schiefer-canvas: "#0C1312"
  schiefer-surface: "#121B19"
  schiefer-raised: "#182321"
  schiefer-rule: "#24312E"
  schiefer-text: "#E8F0EE"
  schiefer-muted: "#8CA09B"
  papier-canvas: "#F2F6F5"
  papier-surface: "#FFFFFF"
  papier-raised: "#EBF1F0"
  papier-rule: "#DCE5E3"
  papier-text: "#14201E"
  papier-muted: "#5C6E68"
  petrol: "#2FB3A8"
  petrol-bright: "#4CC9BE"
  petrol-ink: "#5ED4C9"
  on-petrol: "#06211E"
  petrol-deep: "#0F7A73"
  petrol-deeper: "#0C615C"
  petrol-ink-on-papier: "#0B615B"
  kupfer: "#E07A3F"
  kupfer-text: "#F0A171"
  on-kupfer: "#241800"
  kupfer-deep: "#B4531F"
  kupfer-text-on-papier: "#9C4416"
  danger: "#E4606E"
  danger-bright: "#F2909B"
  on-danger: "#2A090D"
  danger-deep: "#B42331"
  danger-deeper: "#9C1F2B"
  success: "#4CC985"
  success-text: "#7BD8A4"
  success-deep: "#1B7F4D"
  success-text-on-papier: "#14663E"
  amber: "#D9A03F"
  amber-text: "#E5B85C"
  amber-deep: "#B87700"
  amber-text-on-papier: "#7A5200"
  beleg-paper: "#FFFFFF"
  beleg-ink: "#191919"
  beleg-ink-strong: "#141414"
  beleg-ink-muted: "#5A5A5A"
  beleg-rule: "#DCE0E4"
  beleg-rule-strong: "#B4B9BE"
  beleg-fill: "#EBEEF2"
  plan-ground: "#070D14"
  plan-ground-raised: "#0A141E"
  plan-ink: "#EEF4FB"
  plan-teal: "#2EE6CF"
  plan-teal-deep: "#16B7A3"
  plan-teal-ink: "#05201D"
  plan-muted: "#8BA0B6"
  plan-faint: "#5F7488"
  plan-soft: "#A8B8C8"
  plan-exclusion: "#6B829A"
  plan-hall: "#26A56B"
  plan-occupied: "#2F9E63"
  plan-ok: "#6FE0A6"
  plan-reserved: "#EAB452"
  plan-invalid: "#F26B63"
  plan-move: "#4EA8F5"
  plan-danger-text: "#FFB4AD"
  plan-chip-text: "#BFF3EC"
  calendar-due: "#B4552F"
  calendar-wish: "#8A6D1F"
  workbench-emphasis: "#1E2B28"
  workbench-rule: "#293A36"
  workbench-rule-strong: "#3A504A"
  workbench-muted: "#9AADA8"
  workbench-rule-on-papier: "#D4E0DD"
  workbench-rule-strong-on-papier: "#B7CBC6"
typography:
  display:
    fontFamily: "Bricolage Grotesque, Segoe UI Variable Display, Segoe UI, system-ui, sans-serif"
    fontSize: "2.25rem"
    fontWeight: 800
    lineHeight: 1.05
    letterSpacing: "-0.02em"
  headline:
    fontFamily: "Bricolage Grotesque, Segoe UI Variable Display, Segoe UI, system-ui, sans-serif"
    fontSize: "1.75rem"
    fontWeight: 800
    letterSpacing: "-0.02em"
  title:
    fontFamily: "Bricolage Grotesque, Segoe UI Variable Display, Segoe UI, system-ui, sans-serif"
    fontSize: "1.375rem"
    fontWeight: 800
    letterSpacing: "-0.02em"
  title-small:
    fontFamily: "Bricolage Grotesque, Segoe UI Variable Display, Segoe UI, system-ui, sans-serif"
    fontSize: "1.125rem"
    fontWeight: 700
  body-large:
    fontFamily: "Hanken Grotesk, system-ui, -apple-system, Segoe UI, sans-serif"
    fontSize: "1rem"
    fontWeight: 400
    lineHeight: 1.5
  body:
    fontFamily: "Hanken Grotesk, system-ui, -apple-system, Segoe UI, sans-serif"
    fontSize: "0.875rem"
    fontWeight: 400
    lineHeight: 1.5
  caption:
    fontFamily: "Hanken Grotesk, system-ui, -apple-system, Segoe UI, sans-serif"
    fontSize: "0.75rem"
    fontWeight: 600
  label:
    fontFamily: "JetBrains Mono, ui-monospace, SF Mono, Menlo, Consolas, monospace"
    fontSize: "0.6875rem"
    fontWeight: 700
    letterSpacing: "0.09em"
  micro:
    fontFamily: "JetBrains Mono, ui-monospace, SF Mono, Menlo, Consolas, monospace"
    fontSize: "0.625rem"
    fontWeight: 700
    letterSpacing: "0.12em"
  workbench-display:
    fontFamily: "Bricolage Grotesque, Hanken Grotesk, system-ui, sans-serif"
    fontSize: "clamp(2.4rem, 5.4vw, 4.6rem)"
    fontWeight: 800
    lineHeight: 0.98
    letterSpacing: "-0.035em"
  workbench-body:
    fontFamily: "Hanken Grotesk, system-ui, -apple-system, Segoe UI, sans-serif"
    fontSize: "15px"
    fontWeight: 400
    lineHeight: 1.55
rounded:
  hairline: "3px"
  chip: "6px"
  row-control: "8px"
  control: "9px"
  field: "10px"
  action: "12px"
  card: "14px"
  surface: "16px"
  pill: "999px"
components:
  button-primary:
    backgroundColor: "{colors.petrol}"
    textColor: "{colors.on-petrol}"
    rounded: "{rounded.action}"
    padding: "0.6rem 1rem"
    height: "44px"
  button-primary-hover:
    backgroundColor: "{colors.petrol-bright}"
  button-ghost:
    backgroundColor: "transparent"
    textColor: "{colors.schiefer-text}"
    rounded: "{rounded.action}"
    padding: "0.35rem 0.7rem"
    height: "36px"
  button-danger:
    backgroundColor: "{colors.danger}"
    textColor: "{colors.on-danger}"
    rounded: "{rounded.action}"
    padding: "0.6rem 1rem"
    height: "44px"
  input:
    backgroundColor: "{colors.schiefer-canvas}"
    textColor: "{colors.schiefer-text}"
    rounded: "{rounded.action}"
    padding: "0.65rem 0.75rem"
  card:
    backgroundColor: "{colors.schiefer-surface}"
    textColor: "{colors.schiefer-text}"
    rounded: "{rounded.card}"
    padding: "1rem"
  badge:
    rounded: "{rounded.pill}"
    padding: "0.15rem 0.5rem"
    typography: "{typography.label}"
  segmented-control:
    backgroundColor: "{colors.schiefer-raised}"
    rounded: "{rounded.pill}"
    padding: "2px"
  toast:
    backgroundColor: "{colors.schiefer-raised}"
    textColor: "{colors.schiefer-text}"
    rounded: "{rounded.pill}"
    padding: "0.6rem 1.2rem"
  tab-active:
    textColor: "{colors.petrol-ink}"
---

# Design System: Parkrr Fahrwerk

## Overview

**Creative North Star: "Fahrwerk"**

Fahrwerk is the running gear of a storage yard: the part that carries the daily load and is only noticed when it fails. Parkrr's interface follows that idea. A slate ground with a petrol tint carries dense operational screens (persons, vehicles, extra costs, invoices, the tax year) without decoration competing for attention. Petrol marks what can be acted on, copper marks money, amber marks a state that needs a look, and the faint garage-plan linework behind every page ties the tool to its spatial core, the scale hall planner.

The app is mobile-first and operated with one hand on a phone in the yard as often as at a desk. Controls are direct (one-tap sliders, segmented switches, whole-card navigation), numbers line up, and every state is readable without color. Dark is the default and is designed on its own terms; light is a full counterpart, not an inversion.

The standalone review workbenches (security audit, product roadmap, before/after comparisons) share the same world: the same slate and petrol, the same three typefaces, the same plan linework. They differ in density and composition, not in material.

**Key Characteristics:**
- Slate ground, petrol action, copper money, amber state.
- Three voices: Bricolage Grotesque for numbers and headings, Hanken Grotesk for reading, JetBrains Mono for labels and data.
- One-tap controls and whole-card targets over forms and small buttons.
- Garage-plan linework as the recurring signature, always behind content.
- Every state carried by text, shape or position as well as color.

## Colors

A green-tinted slate in two themes, one petrol voice for action, one copper voice for money, and state hues that only ever describe state.

### Primary
- **Petrol** (#2FB3A8 on slate, #0F7A73 on paper): primary buttons, the active tab, focus rings, links, selection, and the brand mark. Hover lifts to **Bright Petrol** (#4CC9BE) on slate and deepens to **Deep Petrol** (#0C615C) on paper.
- **Petrol Ink** (#5ED4C9 on slate, #0B615B on paper): petrol used as text and line work, e.g. the active tab label and section-add actions.

### Secondary
- **Kupfer** (#E07A3F on slate, #B4531F on paper; text #F0A171 / #9C4416): money only. Open balances, the "Offen gesamt" hero edge, the highlighted month in charts, the left edge of balance cards.

### Neutral
- **Schiefer** (canvas #0C1312, surface #121B19, raised #182321, rule #24312E, text #E8F0EE, muted #8CA09B): the dark theme, default.
- **Papier** (canvas #F2F6F5, surface #FFFFFF, raised #EBF1F0, rule #DCE5E3, text #14201E, muted #5C6E68): the light theme.
- **Beleg ink** (paper #FFFFFF, ink #191919, strong #141414, muted #5A5A5A, rules #DCE0E4 / #B4B9BE, fill #EBEEF2): the invoice facsimile. Identical to the PDF the customer receives, in every theme and in print.

### State
- **Success** (#4CC985, text #7BD8A4; on paper #1B7F4D / #14663E): paid, stored, healthy backup.
- **Amber** (#D9A03F, text #E5B85C; on paper #B87700 / #7A5200): reserved, overdue-soon, needs a look. Never decoration.
- **Danger** (#E4606E, bright #F2909B; on paper #B42331 / #9C1F2B): irreversible actions, failures, invalid placements outside the planner.

### Hallenplan
The planner's plan surface stays dark in both themes so vehicles, walls and clearances read like a blueprint: ground #070D14 / #0A141E, ink #EEF4FB, teal #2EE6CF / #16B7A3, and status hues for the hall (#26A56B), occupied (#2F9E63), ok (#6FE0A6), reserved (#EAB452), invalid (#F26B63) and moving (#4EA8F5). Around the plan, the planner's toolbar and side cards take the app's own tokens.

**Fixed planner palette.** Every fixed color in the planner rules has a name, `--gpc-<family>-<lightness %>[-a<opacity %>]`, defined once on `:root` at the top of the planner section in `style.css` with exactly the value it had before (October 2026; screenshots and computed colors unchanged in both themes). Equal values share one name; a palette change is now an edit in one place. The theme-dependent planner colors stay the `--gp*` tokens on `.gp`. Colors the planner writes as SVG attributes from script live in named tables in `app.js` (`WALLCOL`, `OPENCOL`, `ZONECOL`, the vehicle symbol inks), and the printable SVG export keeps its own light print colors.

- **Teal** (`--gpc-teal-…`): `04-a70` rgba(4,16,12,.7), `04-a78` rgba(4,16,12,.78), `04-a82` rgba(4,16,12,.82), `04-a85` rgba(4,16,12,.85), `05` #04140c, `05-a70` rgba(4,20,16,.7), `05-a90` rgba(4,20,16,.9), `07` #0c1a14, `08` #06231d, `13` #123028, `34` #2f7d63, `40` #26a56b, `40-a22` rgba(38,165,107,.22), `42-a14` rgba(34,180,120,.14), `44` #2bb47a, `50-a12` rgba(45,212,191,.12), `50-a18` rgba(45,212,191,.18), `54-a06` rgba(46,230,207,.06), `54-a08` rgba(46,230,207,.08), `54-a10` rgba(46,230,207,.1), `54-a14` rgba(46,230,207,.14), `54-a30` rgba(46,230,207,.3), `54-a40` rgba(46,230,207,.4), `54-a50` rgba(46,230,207,.5), `54-a55` rgba(46,230,207,.55), `61` #5adcb4, `61-a30` rgba(90,220,180,.3), `72` #8fe0d4, `75` #8ff0e0, `76` #8ff3e7, `79` #a8ead4, `85` #cfe4dd
- **Green** (`--gpc-green-…`): `14` #0f3a20, `73` #8fe6b0
- **Blue** (`--gpc-blue-…`): `04-a60` rgba(4,10,18,.6), `05` #060b11, `05-a04` rgba(6,12,20,.04), `05-a74` rgba(6,12,20,.74), `08` #0b1620, `10` #0c1a26, `12` #12202d, `14` #0f2a3a, `15` #102c3a, `63` #4ea8f5, `63-a35` rgba(78,168,245,.35), `72-a28` rgba(120,190,245,.28), `87` #bcdcff, `95` #eaf1f8, `96` #eaf6ff
- **Slate** (`--gpc-slate-…`): `08` #0c141d, `13` #151f2b, `14` #16222f, `14b` #1a232e, `19` #26313d, `21` #243547, `21b` #2b3540, `23` #2a3b4d, `29` #3c4a5a, `35` #46586c, `42` #5b6b7d, `67` #93a9c2, `70` #9fb2c4, `86` #cfdae6
- **Amber** (`--gpc-amber-…`): `05-a78` rgba(24,16,3,.78), `15` #392c12, `35` #8a6b2a, `60` #e0b452, `62-a25` rgba(234,180,82,.25), `65` #f0b45a, `65-a14` rgba(240,180,90,.14), `68` #ffd35b, `68-a12` rgba(255,211,91,.12), `76` #f0d194, `77` #ffd98a, `77b` #f4d894, `82` #ffe7a3
- **Orange** (`--gpc-orange-…`): `18` #3a2c22, `28` #5a4433
- **Red** (`--gpc-red-…`): `12` #2a1211, `17` #3a1c1c, `36` #8a3030, `57-a22` rgba(220,80,70,.22), `59-a15` rgba(229,72,77,.15), `67-a22` rgba(242,107,99,.22), `67-a40` rgba(242,107,99,.4), `84` #ffb4ad, `88` #ffc7c0
- **Gray** (`--gpc-gray-…`): `04-a60` rgba(6,12,10,.6), `04-a62` rgba(8,14,11,.62), `04-a66` rgba(8,14,11,.66), `97-a70` rgba(242,247,251,.7), `97-a92` rgba(242,247,251,.92)
- **White** (`--gpc-white-…`): `--gpc-white` #ffffff, `a05` rgba(255,255,255,.05), `a06` rgba(255,255,255,.06), `a10` rgba(255,255,255,.1), `a50` rgba(255,255,255,.5), `a80` rgba(255,255,255,.8)
- **Black** (`--gpc-black-…`): `--gpc-black` #000000, `a05` rgba(0,0,0,.05), `a18` rgba(0,0,0,.18), `a36` rgba(0,0,0,.36), `a42` rgba(0,0,0,.42), `a50` rgba(0,0,0,.5), `a55` rgba(0,0,0,.55), `a60` rgba(0,0,0,.6), `a65` rgba(0,0,0,.65)

### Calendar
Event fills: pickup in Deep Petrol, reservation in Amber, due invoice #B4552F, portal pickup wish #8A6D1F. Each event also carries its icon and, for screen readers, its kind as text.

### Workbench
The standalone dossiers add emphasis #1E2B28, rules #293A36 / #3A504A and muted #9AADA8 on slate, and rules #D4E0DD / #B7CBC6 on paper.

**The Copper-Is-Money Rule.** Kupfer appears only where money is meant: a balance, an amount owed, the current month of a money chart. Never for decoration, never for a warning.

**The Amber-Is-State Rule.** Amber describes a state (reserved, due soon, backup stale). It is not a second accent.

**The Paper-Stays-Paper Rule.** The invoice preview uses the Beleg ink in every theme, because the operator must read on screen exactly what the customer receives.

**The Restrained Petrol Rule.** Petrol directs attention; it does not wash large areas. Large surfaces stay slate or paper.

## Typography

**Display Font:** Bricolage Grotesque (with Segoe UI Variable Display, system-ui)
**Body Font:** Hanken Grotesk (with system-ui, -apple-system, Segoe UI)
**Label/Mono Font:** JetBrains Mono (with ui-monospace, SF Mono, Menlo, Consolas)

**Character:** A compact grotesque with a slightly wide, confident display cut for numbers and headings, a calm humanist grotesque for reading, and a monospace that turns labels, IDs and figures into data. All three are local WOFF2 files.

### Hierarchy
- **Display** (800, 2.25rem, 1.05): the person balance hero and the largest page figures.
- **Headline** (800, 1.75rem): page titles on desktop.
- **Title** (800, 1.375rem): section titles, dashboard hero numbers.
- **Title Small** (700, 1.125rem): card headings, dialog titles.
- **Body Large** (400, 1rem, 1.5): form fields, buttons, menu items, base reading size.
- **Body** (400, 0.875rem, 1.5): card metadata, lists, legends. Reading text stays within about 70 characters.
- **Caption** (600, 0.75rem): segmented-control labels, secondary chips.
- **Label** (JetBrains Mono 700, 0.6875rem, 0.09em, uppercase): eyebrows, KPI labels, badges, tab labels.
- **Micro** (JetBrains Mono 700, 0.625rem, 0.12em, uppercase): menu group headings.
- **Workbench Display / Body** (800 clamp(2.4rem, 5.4vw, 4.6rem), 0.98 · 400 15px, 1.55): thesis headlines and reading text in the standalone dossiers.

**The Ladder Rule.** Every size comes from the nine app steps above. Three exceptions are deliberate: the planner's zoom-dependent block labels (0.5–0.58rem), the icon-button glyph and invoice title (1.2rem), and single display figures between 1.3 and 2.5rem that each carry their own level of the hierarchy.

**The Tabular Rule.** Money, counts and dates that line up use tabular numerals.

## Layout

Mobile-first. Below 1024px a glass tab bar (64px) carries the five main destinations at the bottom and secondary places live in the menu sheet; from 1024px the same tab bar becomes a 224px left rail. The header is 56px. Page content is a single column up to 900px with 16px minimum side gutters; the planner takes the full width.

Breakpoints: 640px (two-column forms and KPI grids), 900px (planner switches to plan-first with a swipeable "not placed" strip and rare tools behind "Mehr"), 1024px (rail navigation). Tab labels shorten (Übers., Pers., Stellpl.) exactly when they no longer fit and give way to icons only below that, always keeping the full name as the accessible name. Status rows put name and value on top and the bar below when a row gets too narrow.

A tiled garage-plan pattern (parking bays, dashed lane centre lines, direction arrows) sits fixed behind all pages at 7–9% opacity, with a soft petrol glow at the top. Page heads and subtitles get a blurred scrim in the canvas color so linework never crosses text.

Workbenches use a container up to 1760px: thesis and provenance, a summary strip, then a three-column master-detail bench (control rail, dense register 300–430px, flexible dossier). The dossier becomes a full-screen layer on phones.

## Elevation & Depth

Surfaces are tonal layers (canvas, surface, raised) separated by 1px rules. App cards add a soft double shadow (`0 1px 2px rgba(0,0,0,.35), 0 10px 28px rgba(0,0,0,.35)` on slate; `0 1px 2px rgba(16,34,31,.05), 0 8px 24px rgba(16,34,31,.08)` on paper) so they sit just above the plan linework. Overlays (dialogs, menu sheet, toasts) use the stronger overlay shadow. The header and tab bar are translucent glass with a 14px blur.

**The Border-Before-Shadow Rule.** Establish structure with rules and surface contrast first. Workbenches stay flat; app cards get only the soft card shadow; only true overlays float.

## Shapes

Crisp but friendly. Cards 14px, larger surfaces 16px, buttons and fields 12px, quiet controls 8–10px, small chips 6px, rails and bar ends 3px, pills fully round. Most structure is a 1px line. Icons are inline SVG strokes (1.8px, round caps and joins) from one shared set; no emoji and no font-dependent glyphs.

## Components

### Buttons
- **Shape:** gently rounded (12px), 44px tall, 36px for small buttons.
- **Primary:** petrol fill with dark petrol ink, Bricolage 700. One per view.
- **Ghost:** transparent with a muted-text border, because the border is the only cue that it is a control (≥3:1).
- **Danger:** red fill for the confirming step of a destructive dialog; **Ghost Danger** (red outline) for irreversible side actions such as Storno.
- **Hover / Focus:** fill lightens; a 2px petrol focus outline with offset; 1px press feedback.

### Chips, badges and switches
- **Badges:** pill, mono label size, tinted background with state-colored text.
- **Two switch patterns only:** `.segments` (pill track, soft petrol active) for views and filters, `.seg-mini` (sliding thumb) for states such as stored/reserved/collected and open/paid. Each state group is named with its record ("Lagerstatus · Demo Wohnmobil 1").

### Cards / Containers
- **Corner Style:** 14px.
- **Background:** surface token with a 1px rule and the soft card shadow.
- **Whole-card navigation:** cards that open a record are one target (the title link spans the card) with a single chevron at the right; buttons inside stay their own targets.
- **Folds:** secondary blocks (key figures, history, cost statistics) are `details` cards whose summary line already names the key values; their open state is remembered.

### Inputs / Fields
- **Style:** canvas fill, 1px rule, 12px corners, 16px text.
- **Focus:** petrol border plus a 3px soft petrol halo.
- **Required:** a visual asterisk (hidden from screen readers, which hear aria-required) and one sentence under the form explaining it.

### Navigation
- **Tab bar / rail:** five destinations with stroke icons in a pill; active in Petrol Ink on a soft petrol pill. The menu sheet groups places (Betrieb, Verwaltung) and commands (Sitzung), marks the current page, and has its own close button.

### Toasts and undo
Pill toasts at the bottom (top on phones). Destructive actions ask first and then still offer "Rückgängig" while the toast is visible; the server call runs only when the toast expires.

### Hall planner (signature)
A scale floor plan on the dark plan ground: vehicles as true-to-size blocks, walls as mitred polygons, clearance buffers, a 0.1–1m snap grid. Vehicles can be placed by dragging or by tapping (select, then tap the floor or press Enter); invalid positions stay red and are never saved.

### Invoice document
A white paper facsimile with the Beleg ink, laid out like the delivered PDF.

### Workbench
Filter rail, compact register and dossier for large inspectable sets. Rows lead with identifiers and titles; selection is a tinted row with a leading rule; the dossier is URL-addressable. Before/after comparisons use a slider, side-by-side and flip views.

## Do's and Don'ts

### Do:
- **Do** keep copper for money and amber for state, and pair every color with text, an icon or a position.
- **Do** use the nine-step type ladder and tabular numerals for figures that line up.
- **Do** make the common action one tap: sliders, whole-card targets, tap-to-place in the planner.
- **Do** keep icons in the shared SVG set and draw new ones with the same 1.8px stroke.
- **Do** let the garage-plan linework sit behind content, with a scrim under page heads.
- **Do** design dark and light separately and measure every ink/ground pair (WCAG AA).

### Don't:
- **Don't** use copper or amber as decoration, and don't use petrol as a large fill.
- **Don't** introduce emoji or font-dependent Unicode glyphs as icons.
- **Don't** add font sizes outside the ladder, or radii outside the scale, without documenting why.
- **Don't** put the invoice preview into theme colors.
- **Don't** add external fonts, CDNs or hosted services; all assets are local.
- **Don't** introduce raster imagery into the workbenches without a documented need and provenance.
