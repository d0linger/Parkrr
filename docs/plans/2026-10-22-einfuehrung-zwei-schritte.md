# Einführung der Ausbau-Funktionen in zwei Schritten

Entscheidung O8 vom 22.10.2026 (Artifact „Parkrr Ausbauplan“): erst alles ohne
Kundenkontakt, nach etwa zwei Wochen die Mail-Automatik und den Vertrag im Portal.
Grund: die Mail-Automatik wirkt nach außen; vorher sollen Warteschlange und
Mail-Protokoll ein paar Tage im echten Betrieb gelaufen sein.

Umfang: alle Commits auf `dev` seit dem Ausbauplan (B1 bis B8, Ruhezeit, offene
Punkte O1 bis O8), Migrationen 081 bis 088.

## Vor dem Deploy

- [ ] Backup-Kachel grün, ein frisches Backup liegt vor (Handbuch, Abschnitt 3).
- [ ] Pull Request `dev` → `main`, CI grün, auch der Live-Dashboard-Test.
- [ ] Deploy. Die Migrationen 081 bis 088 laufen beim Start. 088 trägt bei früheren
      Kopien aus „Erneut einstellen“ den Vorgänger aus dem Änderungsprotokoll nach;
      danach kennt der Platzvorschlag auch für sie den Vorjahresplatz.

Ab dem Deploy ist im Kundenportal schon neu: Verlängerung anfragen und „Kurz abholen“.
Beides landet als Kundenwunsch in der Heute-Liste und ändert nichts von selbst.

## Schritt 1: ohne Kundenkontakt (ab dem Deploy, etwa zwei Wochen)

Nichts hier schickt eine Mail oder zeigt Kunden etwas Neues.

- [ ] **Rollen** (Benutzer): Konten für Buchhaltung und Hallendienst anlegen. Mit dem
      Hallendienst-Konto einmal anmelden: Startseite „Heute in der Halle“, ein Gefährt
      im Planer verschieben (wird gespeichert, der Grundriss bleibt unberührt), bei
      einem Gefährt, das unterwegs ist, „Ist zurück“ (O6).
- [ ] **Planer**: jede Halle einmal als Bearbeiter öffnen. Der Planer trägt dabei die
      nutzbare Fläche nach; danach zeigt die Startseite die Auslastung in Prozent (O1).
- [ ] **Halle**: Serviceaufgaben anlegen, Schlüsselbuch füllen, bei der nächsten
      Übergabe die Zustandsfotos nutzen.
- [ ] **Kontoauszug**: einen camt.053-Auszug einlesen und die Vorschläge prüfen.
      Gebucht wird erst beim Zuordnen.
- [ ] **Ruhezeit**: bei Bedarf „Kurz weg …“ am Gefährt.
- [ ] **Wertsicherung** (Tarife → Wertsicherung): VPI-Werte eintragen, je Tarif
      Basismonat und Schwelle, Vorschau ansehen. Noch nichts anpassen.
- [ ] **Mail-Weg beobachten**: Änderungsprotokoll → „E-Mail-Versand“ ab und zu
      ansehen. Manuell verschickte Rechnungen und Erinnerungen laufen schon über die
      Warteschlange; nichts darf dort hängen bleiben.

## Schritt 2: nach etwa zwei Wochen (ab etwa 5.11.2026)

Voraussetzung: SMTP eingerichtet, keine hängenden Sendungen im Mail-Protokoll,
Verkäufer-Pflichtangaben vollständig (Rechnungseinstellungen).

- [ ] **Mail-Automatik** (Rechnungseinstellungen, Karte „Mail-Automatik“), einen
      Schalter nach dem anderen mit ein paar Tagen Abstand, danach jeweils das
      Mail-Protokoll ansehen:
  1. „Rechnungen aus dem automatischen Lauf per Mail senden“. Wirkt nur, wenn der
     automatische Lauf eingerichtet ist (`PARKRR_AUTO_INVOICE_CRON`).
  2. „Automatisch mahnen“ mit den Tagen bis zur nächsten Stufe. Vorher bei Personen,
     die nicht gemahnt werden sollen, „nicht automatisch mahnen“ setzen.
  3. „Erinnerung vor Vertragsende (Tage)“, zum Beispiel 30.
- [ ] **Einstellvertrag** (Einstellungen): Vorlage speichern. Ab dann lesen und
      annehmen Kunden den Vertrag im Portal; das angenommene PDF wird Anhang der Person.
- [ ] **Abholzeitfenster** (Einstellungen): eintragen. Das Portal bietet dann freie
      Zeitpunkte für die Abholung an.
- [ ] **Wertsicherung**: die erste Anpassung. „Mitteilung per Mail“ nur, wenn die
      Kunden die Preisänderung so erfahren sollen.

## Rückweg

- Jeder Schalter der Mail-Automatik lässt sich wieder ausschalten. Was schon
  eingereiht ist, steht im Mail-Protokoll.
- Den Deploy zurücknehmen heißt: voriges Image und das Backup von vor dem Update, weil
  die Migrationen nur vorwärts laufen (Handbuch, Abschnitt 3).
