# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

The primary users are small, German-speaking vehicle-storage operators and their staff. They use Parkrr in daily operations to manage customers, stored vehicles, storage locations, costs, invoices, and payments.

Customers are a secondary audience. They can use a time-limited, read-only self-service portal to view their balance, vehicles, and invoices, download invoice PDFs, and open payment QR codes. They do not receive a full application account and cannot change operational records through the portal.

## Product Purpose

Parkrr helps small operators of storage-space rentals, winter storage, camping storage, boat storage, and similar businesses run the complete vehicle-storage workflow in one self-hosted application.

Success means that operators can see what is stored where, move vehicles through reservation, storage, and collection, preserve handover evidence, calculate ongoing costs, invoice completed periods correctly, record payments, and answer customer or audit questions without maintaining parallel spreadsheets or cloud services.

## Positioning

Parkrr combines three mechanisms that are normally fragmented across generic tools: one-tap operational vehicle workflows, a to-scale garage and hall planner, and Austrian-compliant invoicing and payment records. It is self-hosted, privacy-conscious, and designed around the recurring seasonal routines of small storage operators rather than generic fleet management or generic accounting.

## Operating Context

- Operators manage people who may store multiple vehicles across seasons.
- Vehicle status moves through *reserviert*, *eingelagert*, and *abgeholt*. Payment status can be handled directly on operational cards.
- Seasonal customers can be stored again by duplicating established vehicle data and photos instead of re-entering master data.
- Storage space is structured as garages, halls, and spots. The planner supports walls, exclusion zones, imported plan underlays, vehicle rotation, fit checks, and occupancy tracking.
- An invalid moved or rotated vehicle may remain visibly in place while being adjusted, but it is not persisted until its placement is valid.
- Operators record handover condition and signatures, manage tariffs and extra charges, issue invoices, allocate payments, send reminders, export records, and review an append-only audit trail.
- The application is operated through Docker with PostgreSQL and may sit behind an operator-managed reverse proxy. Optional SMTP, S3-compatible backups, passkeys, 2FA, monitoring, and automated invoicing are configured by the operator.
- On this development machine the local application is normally reached on port `8099`; the container continues to listen on `8080`.

## Capabilities and Constraints

- Mobile-first, installable PWA with light and dark themes and an offline-capable app shell.
- German application UI.
- People, vehicles, photos, lifecycle history, reservations, handover protocols, tariffs, flat rates, one-off and recurring charges, invoicing, payments, credit, reporting, import/export, search, backups, and role-based access.
- Administrative roles are Admin, Editor, and Reader; customer access is separate and read-only through revocable portal links.
- Invoices are immutable once issued. Corrections use cancellation and replacement records rather than silent edits.
- Periodic charges are invoiced only for fully completed monthly or yearly periods. The current period is intentionally deferred; partial-period bookkeeping is not used.
- A per-person flat rate covers base rent only. Bound one-off additional charges remain separately payable.
- The simple vehicle `paid` state is an operational control and remains distinct from the detailed payment ledger.
- All frontend assets are served locally. The product does not depend on external CDNs, tracking, or a hosted cloud service.
- The established implementation is Go with PostgreSQL, embedded HTML/CSS/JavaScript, and Docker-based deployment.
- Parkrr is distinct from the sibling Treckrr product and must not absorb Treckrr's agricultural service-billing domain.

## Brand Commitments

- Product name: Parkrr.
- Existing logo: a house-and-garage mark, available under `web/static/icons/`.
- Product language is direct, operational, and German in the application UI.
- Favor simple one-tap actions, sensible defaults, and reuse over long forms, repeated data entry, or ledger-heavy operational screens.

## Evidence on Hand

- Product description, feature inventory, deployment constraints, roles, and security commitments in `README.md`.
- Operator procedures, portal behavior, retention requirements, backup operations, and troubleshooting in `docs/betreiber-handbuch.md`.
- Current light and dark interface captures in `docs/screenshots/`.
- Existing logo, icons, vehicle imagery, local fonts, and PWA assets in `web/static/`.
- Working application code, database migrations, and automated tests covering core workflows, billing rules, security, accessibility, and edge cases.
- No testimonials, named customers, case studies, usage benchmarks, press coverage, or commercial claims have been supplied. Future work must not fabricate them.

## Product Principles

1. Make daily storage operations fast enough to complete through direct, one-tap controls.
2. Reuse known information across recurring seasons instead of asking operators to enter it again.
3. Keep spatial truth, billing truth, and audit history reliable even when that requires delaying persistence or invoicing until a state is valid and complete.
4. Keep operators in control of their data and infrastructure through self-hosting, local assets, and explicit configuration.
5. Give customers useful visibility without exposing operational editing or requiring another account system.

## Accessibility & Inclusion

Parkrr targets WCAG 2.1 A/AA behavior and includes automated Playwright and axe-core accessibility coverage. Core workflows must remain usable on mobile screens, with keyboard navigation, accessible names, visible focus, status announcements, and non-color-only state communication.
