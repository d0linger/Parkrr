package backup

import (
	"strings"
	"testing"
)

// Eine Wiederherstellungsprüfung, die nur den Gutfall kennt, prüft nichts. Diese
// Tests führen jede Stufe an ihrem FEHLERFALL vor — an dem, was sie finden soll.

func TestChecksumBindetDenInhalt(t *testing.T) {
	a := Checksum([]byte("archivinhalt"))
	if a != Checksum([]byte("archivinhalt")) {
		t.Error("gleiche Bytes müssen dieselbe Prüfsumme ergeben")
	}
	// Ein einzelnes gekipptes Bit — der Bit-Rot-Fall — muss die Summe verändern.
	if a == Checksum([]byte("archivinhalu")) {
		t.Error("ein verändertes Byte muss die Prüfsumme verändern")
	}
	if len(a) != 64 {
		t.Errorf("sha256 hex hat 64 Zeichen, hat %d", len(a))
	}
}

func TestVerifyLocalBytesFindetKurzenSchreibvorgang(t *testing.T) {
	want := []byte("0123456789")
	if err := VerifyLocalBytes(want, want); err != nil {
		t.Errorf("identische Bytes müssen bestehen: %v", err)
	}
	// Volles Dateisystem: WriteFile kann ohne Fehler zurückkommen, die Datei ist
	// aber kürzer. Genau das findet keine spätere Prüfung im Speicher.
	err := VerifyLocalBytes([]byte("01234"), want)
	if err == nil || !strings.Contains(err.Error(), "short write") {
		t.Errorf("kurze Datei muss als short write gemeldet werden, got %v", err)
	}
	// Gleiche Länge, anderer Inhalt — stiller Bit-Rot auf der Platte.
	err = VerifyLocalBytes([]byte("0123456780"), want)
	if err == nil || !strings.Contains(err.Error(), "differs") {
		t.Errorf("verändertes Byte bei gleicher Länge muss auffallen, got %v", err)
	}
}

func TestVerifyArchiveLehntLeeresArchivAb(t *testing.T) {
	rep, err := VerifyArchive(t.Context(), nil, "irgendein-key")
	if err == nil {
		t.Fatal("ein leeres Archiv darf die Prüfung nicht bestehen")
	}
	if rep.Stage != "checksum" {
		t.Errorf("Stufe sollte checksum sein, ist %q", rep.Stage)
	}
}

// Das Inhaltsverzeichnis ist die Stufe, die "technisch einwandfreier Dump der
// FALSCHEN Datenbank" von "unser Backup" unterscheidet.
func TestTablesInTOCErkenntFehlendeKerntabellen(t *testing.T) {
	full := `;
; Archive created at 2026-08-20 03:00:00
;
215; 1259 16400 TABLE public persons parkrr
216; 1259 16410 TABLE public vehicles parkrr
217; 1259 16420 TABLE public invoices parkrr
218; 1259 16430 TABLE public payments parkrr
219; 1259 16440 TABLE public audit_log parkrr
220; 1259 16450 TABLE public halls parkrr
`
	found, missing := tablesInTOC(full)
	if len(missing) != 0 {
		t.Errorf("vollständiges Verzeichnis darf nichts vermissen, fehlt: %v", missing)
	}
	if len(found) != len(coreTables) {
		t.Errorf("alle %d Kerntabellen sollten gefunden werden, waren %d", len(coreTables), len(found))
	}

	// Ein Dump, in dem die Geldtabellen fehlen: formal gültig, als Backup wertlos.
	partial := `;
; Archive created at 2026-08-20 03:00:00
;
215; 1259 16400 TABLE public persons parkrr
216; 1259 16410 TABLE public vehicles parkrr
`
	found, missing = tablesInTOC(partial)
	if len(found) != 2 {
		t.Errorf("zwei Tabellen sollten gefunden werden, waren %d", len(found))
	}
	for _, want := range []string{"invoices", "payments", "audit_log"} {
		var hit bool
		for _, m := range missing {
			if m == want {
				hit = true
			}
		}
		if !hit {
			t.Errorf("%q fehlt im Dump und muss gemeldet werden (gemeldet: %v)", want, missing)
		}
	}

	// Kommentarzeilen dürfen nie als Tabelle zählen — sonst bestünde ein Archiv die
	// Prüfung allein durch seinen Kopftext.
	comment := "; TABLE public persons parkrr\n; TABLE public invoices parkrr\n"
	if found, _ := tablesInTOC(comment); len(found) != 0 {
		t.Errorf("Kommentarzeilen dürfen nicht als Tabellen zählen, gefunden: %v", found)
	}
}

func TestParseTOCLiestKopfdaten(t *testing.T) {
	toc := `;
; Archive created at 2026-08-20 03:00:00 CEST
;
215; 1259 16400 TABLE public persons parkrr
216; 1259 16410 TABLE public vehicles parkrr
`
	info := parseTOC(toc)
	if info.Entries != 2 {
		t.Errorf("zwei Einträge erwartet, waren %d", info.Entries)
	}
	if !strings.HasPrefix(info.Created, "2026-08-20") {
		t.Errorf("Erstellungszeit nicht gelesen: %q", info.Created)
	}
}

// Die Liste steuert, was als "wertloses Backup" gilt — sie darf nicht unbemerkt
// schrumpfen. Ohne diesen Test könnte jemand invoices entfernen und die Prüfung
// würde einen Dump ohne Rechnungen durchwinken.
func TestKerntabellenDeckenDieGeldpfadeAb(t *testing.T) {
	for _, want := range []string{"persons", "vehicles", "invoices", "payments", "audit_log"} {
		var hit bool
		for _, c := range coreTables {
			if c == want {
				hit = true
			}
		}
		if !hit {
			t.Errorf("%q muss in coreTables stehen: ohne sie wäre eine Wiederherstellung wertlos", want)
		}
	}
}

// Die Untergrenze ist der Unterschied zwischen "nicht leer" und "glaubwürdig". Ein
// abgeschnittener oder fremder Dump kann eine Handvoll Objekte tragen und käme durch
// eine reine Leerprüfung.
func TestUntergrenzeMitGrossemAbstandZumEchtwert(t *testing.T) {
	// Ein echter Parkrr-Dump wurde live mit 306 Objekten gemessen. Die Grenze muss
	// deutlich darunter liegen: eine Fehlablehnung verwirft JEDES künftige Backup
	// und friert damit den letzten guten Stand ein — teurer als ein zu tiefer Wert.
	if minArchiveObjects >= 150 {
		t.Errorf("minArchiveObjects = %d liegt zu nah am Echtwert (~306) — ein legitimer, "+
			"frisch installierter Bestand könnte fälschlich abgelehnt werden", minArchiveObjects)
	}
	if minArchiveObjects < 10 {
		t.Errorf("minArchiveObjects = %d ist so niedrig, dass ein abgeschnittener Dump "+
			"durchkäme — dann wäre die Grenze wirkungslos", minArchiveObjects)
	}
}

// Prefix-Kollision: "vehicle_photos" darf nicht als "vehicles" durchgehen. Treckrr
// verankert dafür den vollen Namen im TOC-Text; Parkrr vergleicht das Namensfeld
// exakt. Dieser Test hält fest, dass das so bleibt.
func TestKeinePrefixKollisionBeiKerntabellen(t *testing.T) {
	toc := `;
; Archive created at 2026-08-20 03:00:00
;
215; 1259 16400 TABLE public vehicle_photos parkrr
216; 1259 16410 TABLE public persons_archive parkrr
217; 1259 16420 TABLE public invoices_old parkrr
`
	found, missing := tablesInTOC(toc)
	if len(found) != 0 {
		t.Errorf("Tabellen mit ähnlichem Namen dürfen keine Kerntabelle erfüllen, gefunden: %v", found)
	}
	if len(missing) != len(coreTables) {
		t.Errorf("alle %d Kerntabellen müssen als fehlend gelten, gemeldet: %v", len(coreTables), missing)
	}
}
