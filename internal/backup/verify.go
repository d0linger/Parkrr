package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Wiederherstellungsprüfung nach jedem Backup-Lauf.
//
// Ein geschriebenes Archiv ist kein gesichertes Archiv. Vier Stufen, aufsteigend
// nach Kosten, jede prüft etwas, das die vorige nicht kann:
//
//	1 PRÜFSUMME    sha256 über die verschlüsselten Bytes. Bindet das, was im ZIEL
//	               liegt, an das, was erzeugt wurde.
//	               Wichtig zur Einordnung: Bit-Rot und Kürzung erkennt bereits
//	               AES-GCM beim Entschlüsseln über den Auth-Tag (gegen ein echtes
//	               Archiv geprüft: ein gekipptes Bit und ein halbiertes File werden
//	               beide abgewiesen). Diese Stufe ist deshalb KEIN zweiter
//	               Integritätsbeweis. Ihr Wert ist ein anderer: sie kommt ohne den
//	               Schlüssel aus, kostet nichts, und nur mit ihr lässt sich ein
//	               S3-Objekt gegen das lokal Erzeugte vergleichen — Stufe 2 hätte
//	               sonst keinen Sollwert.
//	2 RÜCKLESEN    Beim S3-Ziel: Größe per Stat, Kopf- und Fußbytes per
//	               Byte-Range, dann das ganze Objekt und Prüfsumme vergleichen.
//	               Vorher galt ein Upload als erfolgreich, sobald PutObject
//	               zurückkam — ein Verbindungsabbruch mittendrin war unsichtbar.
//	3 ARCHIVKOPF   Entschlüsseln und `pg_restore --list`: beweist, dass Schlüssel,
//	               Rahmenformat und Inhaltsverzeichnis zusammenpassen, ohne eine
//	               Datenbank anzufassen.
//	4 INHALT       Sind die Kerntabellen im Inhaltsverzeichnis? Stufe 3 besteht
//	               auch ein technisch einwandfreier Dump der FALSCHEN oder einer
//	               leeren Datenbank. Erst diese Stufe schließt das aus.
//
// Bewusst NICHT enthalten: ein echter Test-Restore in eine Wegwerf-Datenbank.
// Er würde einen zweiten Datenbank-Server voraussetzen, den diese Installation
// nicht hat, und nach jedem nächtlichen Lauf Minuten kosten. Die Stufen 1–4
// laufen in Sekunden und fangen jeden Fehler ab, der nicht erst beim Einspielen
// selbst auftritt. Ein periodischer echter Restore-Test bleibt eine eigene,
// seltenere Aufgabe.

// coreTables sind die Tabellen, ohne die eine Wiederherstellung wertlos wäre. Die
// Liste ist bewusst kurz: sie soll "das ist nicht unsere Datenbank" und "der Dump
// ist leer" erkennen, nicht das Schema nachbilden. Ein Test hält sie aktuell.
var coreTables = []string{"persons", "vehicles", "invoices", "payments", "audit_log"}

// minArchiveObjects ist die Untergrenze für ein glaubwürdiges Archiv.
//
// "Nicht leer" reicht als Prüfung nicht: ein abgeschnittener oder fremder Dump kann
// eine Handvoll Objekte enthalten und käme durch. Ein echter Parkrr-Dump trägt das
// vollständige migrierte Schema — live gemessen 306 Objekte, auch ohne nennenswerte
// Geschäftsdaten. 40 ist deshalb sehr großzügig gewählt (rund 87 % Abstand): eine
// Fehlablehnung wäre teurer als ein zu niedriger Wert, denn sie würde JEDES künftige
// Backup als ungültig verwerfen und damit den letzten guten Stand einfrieren.
const minArchiveObjects = 40

// VerifyReport hält fest, WAS geprüft wurde — nicht nur, dass es geklappt hat.
// Bei einem Fehlschlag steht in Stage, wie weit es kam.
type VerifyReport struct {
	SHA256  string   `json:"sha256"`
	Bytes   int64    `json:"bytes"`
	Entries int      `json:"entries"` // Einträge im Inhaltsverzeichnis
	Tables  []string `json:"tables"`  // gefundene Kerntabellen
	Missing []string `json:"missing"` // erwartete, aber fehlende
	Stage   string   `json:"stage"`   // zuletzt erreichte Stufe
	Created string   `json:"created"` // Zeitstempel aus dem Archivkopf
}

// Checksum ist Stufe 1: sha256 über die verschlüsselten Bytes, hex.
func Checksum(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// VerifyArchive führt die Stufen 1, 3 und 4 auf einem verschlüsselten Archiv aus.
// Es rührt keine Datenbank an.
func VerifyArchive(ctx context.Context, enc []byte, key string) (VerifyReport, error) {
	rep := VerifyReport{Stage: "checksum", SHA256: Checksum(enc), Bytes: int64(len(enc))}
	if len(enc) == 0 {
		return rep, fmt.Errorf("archive is empty")
	}

	// Inhaltsverzeichnis EINMAL ziehen, Kopf- und Inhaltsstufe daraus ableiten:
	// pg_restore zweimal über dasselbe Archiv zu schicken wäre reine Verdopplung.
	rep.Stage = "archive"
	toc, err := archiveTOC(ctx, enc, key)
	if err != nil {
		return rep, fmt.Errorf("archive check failed: %w", err)
	}
	info := parseTOC(toc)
	rep.Entries, rep.Created = info.Entries, info.Created
	if info.Entries < minArchiveObjects {
		// Lesbar, aber zu dünn: leer, abgeschnitten oder eine fremde Datenbank —
		// genau die Fälle, die ein reiner Formatcheck durchwinkt und die erst beim
		// Ernstfall auffliegen.
		return rep, fmt.Errorf("archive holds only %d objects (< %d) — looks truncated, empty or foreign",
			info.Entries, minArchiveObjects)
	}

	rep.Stage = "content"
	found, missing := tablesInTOC(toc)
	rep.Tables, rep.Missing = found, missing
	if len(missing) > 0 {
		return rep, fmt.Errorf("archive is missing core tables: %s", strings.Join(missing, ", "))
	}
	rep.Stage = "ok"
	return rep, nil
}

// archiveTables liest das Inhaltsverzeichnis und meldet, welche Kerntabellen darin
// vorkommen. pg_restore --list gibt Zeilen der Form
// "216; 1259 16igt TABLE public persons parkrr" aus.
func tablesInTOC(toc string) (found, missing []string) {
	seen := map[string]bool{}
	for _, line := range strings.Split(toc, "\n") {
		if strings.HasPrefix(line, ";") {
			continue
		}
		fields := strings.Fields(line)
		// … TABLE <schema> <name> <owner>
		for i, f := range fields {
			if f == "TABLE" && i+2 < len(fields) {
				seen[fields[i+2]] = true
			}
		}
	}
	for _, t := range coreTables {
		if seen[t] {
			found = append(found, t)
		} else {
			missing = append(missing, t)
		}
	}
	sort.Strings(found)
	sort.Strings(missing)
	return found, missing
}

// VerifyS3Object ist Stufe 2: prüft, dass im Bucket wirklich das liegt, was
// hochgeladen wurde.
//
// Zwei Schritte mit steigenden Kosten, damit der häufigste Fehler — ein
// abgebrochener Upload — schon am billigsten auffällt:
//
//	a) Größe über einen HEAD-Aufruf: erkennt Abbruch und Nullbytes sofort.
//	b) Vollständiges Lesen und Prüfsummenvergleich: der abschließende Beweis.
//
// Hier stand einmal eine Zwischenstufe "Kopf- und Fußbytes über Byte-Bereiche".
// Sie war nie geschrieben, und ein Kommentar, der eine nicht vorhandene Prüfung
// beschreibt, ist schlimmer als gar keiner: er lässt eine Lücke geprüft aussehen.
// Zwischen a) und b) läge sie ohnehin nur bei sehr großen Archiven dazwischen.
//
// wantSHA und wantBytes stammen aus dem Upload, nicht aus dem Objekt selbst —
// sonst würde man das Ergebnis mit sich selbst vergleichen.
func VerifyS3Object(ctx context.Context, c S3Config, name, key, wantSHA string, wantBytes int64) error {
	size, err := StatS3(ctx, c, name)
	if err != nil {
		if errors.Is(err, ErrS3ObjectMissing) {
			return fmt.Errorf("uploaded object %q is not in the bucket", name)
		}
		return fmt.Errorf("s3 stat failed: %w", err)
	}
	if size != wantBytes {
		return fmt.Errorf("object %q is %d bytes, expected %d (incomplete upload?)", name, size, wantBytes)
	}

	got, err := DownloadS3(ctx, c, name)
	if err != nil {
		return fmt.Errorf("s3 read-back failed: %w", err)
	}
	if int64(len(got)) != wantBytes {
		return fmt.Errorf("read back %d bytes, expected %d", len(got), wantBytes)
	}
	if sum := Checksum(got); sum != wantSHA {
		return fmt.Errorf("checksum mismatch: bucket has %s, expected %s", short(sum), short(wantSHA))
	}
	// Und auf den GESPEICHERTEN Bytes noch die Stufen 3+4. Die Prüfsumme beweist nur,
	// dass im Bucket dasselbe liegt wie lokal erzeugt — nicht, dass das Erzeugte ein
	// brauchbarer Dump war. Beim S3-Ziel gibt es keinen zweiten Prüfpfad: ohne dies
	// bliebe eine reine S3-Installation inhaltlich völlig ungeprüft.
	if _, verr := VerifyArchive(ctx, got, key); verr != nil {
		return fmt.Errorf("stored object failed the archive check: %w", verr)
	}
	return nil
}

// VerifyLocalBytes ist Stufe 1 für das Volume-Ziel: vergleicht die gerade
// geschriebene Datei mit dem, was im Speicher erzeugt wurde. Findet einen
// abgeschnittenen Schreibvorgang und ein volles Dateisystem, die beide ohne
// Fehler zurückkommen können.
func VerifyLocalBytes(onDisk, want []byte) error {
	if len(onDisk) != len(want) {
		return fmt.Errorf("file is %d bytes, expected %d (short write?)", len(onDisk), len(want))
	}
	if !bytes.Equal(onDisk, want) {
		return fmt.Errorf("file content differs from what was written (checksum %s vs %s)",
			short(Checksum(onDisk)), short(Checksum(want)))
	}
	return nil
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12] + "…"
	}
	return sha
}
