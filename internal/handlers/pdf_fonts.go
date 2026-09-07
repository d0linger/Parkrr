package handlers

import (
	"embed"

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
func newPDF() (*fpdf.Fpdf, func(string) string) {
	pdf := fpdf.New("P", "mm", "A4", "")
	reg, rerr := pdfFontFS.ReadFile("fonts/DejaVuSans.ttf")
	bold, berr := pdfFontFS.ReadFile("fonts/DejaVuSans-Bold.ttf")
	if rerr != nil || berr != nil {
		// Eingebettete Dateien können praktisch nicht fehlen; der Zweig existiert,
		// damit ein theoretischer Fehler Dokumente degradiert statt verhindert.
		return pdf, pdf.UnicodeTranslatorFromDescriptor("")
	}
	pdf.AddUTF8FontFromBytes(pdfFontName, "", reg)
	pdf.AddUTF8FontFromBytes(pdfFontName, "B", bold)
	return pdf, func(s string) string { return s }
}
