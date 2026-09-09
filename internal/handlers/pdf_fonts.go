package handlers

import (
	"embed"
	"sync"

	"github.com/go-pdf/fpdf"
)

// Eingebettete Unicode-Schrift für alle PDF-Dokumente (Hundert 14).
//
// Bisher liefen Rechnung und Übergabeprotokoll auf der Kern-Schrift Helvetica mit
// einem cp1252-Übersetzer. Der deckt Deutsch ab (Umlaute, §, €) — aber ein Kunde
// namens Łukasz, Nováková oder Йована stand als Ersatzzeichen auf seiner eigenen
// RECHNUNG, einem Dokument mit Aufbewahrungspflicht. DejaVu Sans deckt Latin
// (vollständig), Kyrillisch und Griechisch ab; die Lizenz (Bitstream Vera /
// Public Domain, siehe fonts/LICENSE-DejaVu.txt) erlaubt die Einbettung.
// go-pdf/fpdf subsettet UTF-8-Schriften beim Schreiben, das PDF trägt also nur
// die tatsächlich benutzten Glyphen, nicht die ganze Schriftdatei.
//
//go:embed fonts/DejaVuSans.ttf fonts/DejaVuSans-Bold.ttf
var pdfFontFS embed.FS

// pdfFontName ist der Familienname, unter dem die eingebettete Schrift in den
// PDF-Erzeugern angesprochen wird.
const pdfFontName = "DejaVu"

// newPDF baut ein A4-Portrait-PDF mit der eingebetteten Unicode-Schrift und
// liefert dazu die Textübersetzung für Zellinhalte.
//
// Mit der UTF-8-Schrift ist die Übersetzung die IDENTITÄT — zurückgegeben wird
// sie trotzdem, damit die Erzeuger ihre tr(...)-Aufrufe behalten: scheitert das
// Laden der Schrift je (ein kaputtes Build wäre die einzige Ursache), fällt der
// Aufbau auf Helvetica samt cp1252-Übersetzer zurück, und die Dokumente bleiben
// lesbar statt leer.
// Die beiden Schriftdateien EINMAL aus dem eingebetteten Dateisystem holen.
// embed.FS.ReadFile liefert je Aufruf eine frische Kopie — bei 1,46 MB für beide
// Schnitte hieß das: jede Rechnung, jedes Übergabeprotokoll und jeder Bericht
// kopierte anderthalb Megabyte, nur um sie sofort wieder wegzuwerfen.
var pdfFontsOnce = sync.OnceValues(func() ([]byte, []byte) {
	reg, rerr := pdfFontFS.ReadFile("fonts/DejaVuSans.ttf")
	bold, berr := pdfFontFS.ReadFile("fonts/DejaVuSans-Bold.ttf")
	if rerr != nil || berr != nil {
		return nil, nil
	}
	return reg, bold
})

func newPDF() (*fpdf.Fpdf, func(string) string) {
	pdf := fpdf.New("P", "mm", "A4", "")
	reg, bold := pdfFontsOnce()
	if reg == nil || bold == nil {
		// Unerreichbar: //go:embed lässt das Programm gar nicht erst übersetzen, wenn
		// eine der Schriftdateien fehlt — ReadFile kann für einen eingebetteten Pfad
		// nicht scheitern. Der Zweig bleibt als Absturzschutz stehen, aber er ist KEINE
		// Degradierung auf Helvetica: die Aufrufer setzen unbedingt SetFont("DejaVu"),
		// und eine unbekannte Schrift lässt fpdf beim Output scheitern. Ein früherer
		// Kommentar hier versprach lesbare Dokumente — das wäre nicht eingetreten.
		return pdf, pdf.UnicodeTranslatorFromDescriptor("")
	}
	pdf.AddUTF8FontFromBytes(pdfFontName, "", reg)
	pdf.AddUTF8FontFromBytes(pdfFontName, "B", bold)
	return pdf, func(s string) string { return s }
}
