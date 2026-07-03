# Security Policy

## Unterstützte Versionen

Parkrr wird als rollendes Release entwickelt. Sicherheitsfixes fließen in den
`main`-Branch bzw. das neueste Release ein. Bitte nutze immer eine aktuelle Version.

## Eine Schwachstelle melden

Bitte melde Sicherheitslücken **verantwortungsvoll und nicht öffentlich**:

- Nutze **GitHub → Security → „Report a vulnerability"** (Private Vulnerability
  Reporting), falls aktiviert, **oder**
- schreibe an die im Repository/Profil hinterlegte Kontaktadresse.

Bitte gib an:

- betroffene Komponente/Version und Konfiguration,
- Schritte zur Reproduktion (Proof-of-Concept),
- erwartete vs. tatsächliche Auswirkung.

Wir bestätigen den Eingang zeitnah, halten dich über den Fortschritt auf dem
Laufenden und stimmen einen Zeitpunkt für die Offenlegung ab. Bitte gib uns eine
angemessene Frist zur Behebung, bevor du Details veröffentlichst.

## Betriebshinweise (Härtung)

- Hinter TLS betreiben (`PARKRR_SECURE_COOKIES=true` bzw. `PARKRR_TRUSTED_PROXY=true`
  hinter einem Reverse Proxy).
- Starke, zufällige `PARKRR_SESSION_SECRET` und Admin-/DB-Passwörter setzen.
- Datenbank nicht öffentlich exponieren; regelmäßige Backups (`pg_dump`).
- Updates zeitnah einspielen (Dependabot-PRs).
