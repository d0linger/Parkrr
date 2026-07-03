# Contributing to Parkrr

Danke für dein Interesse! Beiträge – Bugfixes, Features, Doku, Übersetzungen –
sind willkommen.

## Ablauf

1. **Issue zuerst** – für größere Änderungen bitte vorab ein Issue öffnen, damit
   wir Richtung und Umfang abstimmen können.
2. **Fork & Branch** – arbeite auf einem Feature-Branch (`feat/...`, `fix/...`).
3. **Entwickeln** – siehe [README → Entwicklung](README.md#-entwicklung-ohne-docker).
4. **Checks grün halten** vor dem Push:
   ```bash
   go build ./...
   go test ./...
   go vet ./...
   golangci-lint run
   gofmt -l .        # muss leer sein
   ```
5. **Pull Request** – beschreibe *was* und *warum*. Kleine, fokussierte PRs sind
   leichter zu reviewen.

## Konventionen

- **Go:** `gofmt`/`goimports` (local prefix `github.com/preining/parkrr`),
  idiomatischer Fehlerumgang, keine neuen Laufzeit-Abhängigkeiten ohne
  Begründung. Neue Logik möglichst mit Test.
- **SQL:** Schemaänderungen als **neue** Migration in
  `internal/database/migrations/NNN_*.sql` (nie bestehende Migrationen ändern).
- **Frontend:** Vanilla JS/CSS, **keine externen CDNs** – alle Assets lokal.
  Kein Build-Step.
- **Commits:** aussagekräftige Messages; gern im Stil `feat: …` / `fix: …`.

## Sicherheit

Sicherheitslücken **nicht** über öffentliche Issues melden – siehe
[SECURITY.md](SECURITY.md).

## Lizenz

Mit deinem Beitrag stimmst du zu, dass er unter der [MIT-Lizenz](LICENSE)
veröffentlicht wird.
