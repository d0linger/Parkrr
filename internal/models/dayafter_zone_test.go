package models

import (
	"testing"
	"time"
)

// Die zwei Zonen in models.go sind kein Versehen (Hundert 13), und dieser Test
// hält beide Hälften fest:
//
//	WELCHER Tag  -> die Geschäftszone, die in t steckt
//	WIE dargestellt -> UTC-Mitternacht, die Trägerform der DATE-Spalten
//
// Ohne die erste Hälfte rechnete ein in UTC laufender Container zwischen 00:00 und
// 02:00 Wiener Zeit mit dem gestrigen Tag; ohne die zweite wäre jeder Vergleich
// gegen eine aus der Datenbank gelesene DATE um den Zonenversatz daneben.
func TestDayAfterUsesBusinessCalendarDay(t *testing.T) {
	vienna, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		t.Skipf("Zonendatenbank nicht verfügbar: %v", err)
	}
	// Ein und derselbe Zeitpunkt, zweimal betrachtet.
	instant := time.Date(2026, 9, 6, 23, 30, 0, 0, time.UTC)

	inUTC := DayAfter(instant.In(time.UTC))
	if got := inUTC.Format("2006-01-02"); got != "2026-09-07" {
		t.Errorf("aus UTC-Sicht ist der Folgetag der 7., war %s", got)
	}
	inVienna := DayAfter(instant.In(vienna))
	if got := inVienna.Format("2006-01-02"); got != "2026-09-08" {
		t.Errorf("aus Wiener Sicht ist es bereits der 7., der Folgetag also der 8. — war %s", got)
	}

	// Die Darstellung bleibt in BEIDEN Fällen UTC-Mitternacht: so und nur so passt
	// sie zu einer aus der Datenbank gelesenen DATE.
	for _, d := range []time.Time{inUTC, inVienna} {
		if d.Location() != time.UTC {
			t.Errorf("DayAfter muss UTC-verankert liefern, war %v", d.Location())
		}
		if h, m, s := d.Clock(); h != 0 || m != 0 || s != 0 {
			t.Errorf("DayAfter muss auf Mitternacht schnappen, war %02d:%02d:%02d", h, m, s)
		}
	}
}
