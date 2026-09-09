package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/preining/parkrr/internal/config"
	"github.com/preining/parkrr/internal/database"

	"github.com/jackc/pgx/v5"
)

// runSeedDemo befüllt eine Datenbank mit erkennbaren Demo-Daten (Hundert 95):
// zwei Tarife, drei Personen, vier Gefährte, Zusatzkosten, eine Zahlung. Bisher
// begann jede Vorführung und jedes lokale Ausprobieren mit einer leeren Anwendung
// und zehn Minuten Handarbeit — oder, schlimmer, mit einem Abzug echter Daten.
//
// Zwei Leitplanken:
//   - IDEMPOTENT: alle Datensätze tragen das Präfix "Demo:"; ein zweiter Lauf
//     findet sie und legt nichts doppelt an.
//   - WEIGERT SICH auf einer benutzten Datenbank: gibt es auch nur eine Person
//     OHNE Demo-Präfix, bricht der Lauf ab (—force übersteuert bewusst NICHT;
//     Demo-Daten zwischen echten Kunden sind der Zustand, den niemand wollte).
func runSeedDemo(args []string) int {
	fs := flag.NewFlagSet("seed-demo", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: parkrr seed-demo")
		fmt.Fprintln(os.Stderr, "Befüllt die per PARKRR_DATABASE_URL erreichbare Datenbank mit Demo-Daten.")
		fmt.Fprintln(os.Stderr, "Weigert sich, wenn die Datenbank bereits echte (nicht-Demo-)Personen enthält.")
	}
	_ = fs.Parse(args)

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		return 1
	}
	// Wie in run(): die Geschäftszeitzone als Prozesszone setzen — VOR den
	// time.Now()-Startdaten unten und vor database.Connect (das bindet die
	// Sitzungszeitzone daran). Sonst liegen Demo-Startdaten nahe Mitternacht
	// einen Kalendertag daneben, wenn die Container-Zone von PARKRR_TIMEZONE
	// abweicht (Hundert 13).
	time.Local = cfg.Location
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := database.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "connect:", err)
		return 1
	}
	defer pool.Close()
	if err := database.Migrate(ctx, pool); err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		return 1
	}

	// Schutz vor der benutzten Datenbank.
	var realPersons int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM persons WHERE last_name NOT LIKE 'Demo:%'`).Scan(&realPersons); err != nil {
		fmt.Fprintln(os.Stderr, "check:", err)
		return 1
	}
	if realPersons > 0 {
		fmt.Fprintf(os.Stderr, "seed-demo: Datenbank enthält %d Nicht-Demo-Person(en) — abgebrochen.\n", realPersons)
		fmt.Fprintln(os.Stderr, "Demo-Daten gehören in eine frische Datenbank, nicht zwischen echte Kunden.")
		return 1
	}

	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		// Tarife (idempotent über den eindeutigen Namen).
		catID := func(name string, mon, yr float64) (int64, error) {
			var id int64
			e := tx.QueryRow(ctx,
				`INSERT INTO categories (name, default_monthly_cost, default_yearly_cost)
				 VALUES ($1,$2,$3)
				 ON CONFLICT (name) DO UPDATE SET updated_at = categories.updated_at
				 RETURNING id`, name, mon, yr).Scan(&id)
			return id, e
		}
		wohnwagen, err := catID("Demo: Wohnwagen", 45, 480)
		if err != nil {
			return err
		}
		oldtimer, err := catID("Demo: Oldtimer", 60, 650)
		if err != nil {
			return err
		}
		// Standardmaße für den Planer gleich mitgeben (Hundert 80).
		if _, err := tx.Exec(ctx,
			`UPDATE categories SET default_length_m=7.2, default_width_m=2.3, default_height_m=2.8
			  WHERE id=$1 AND default_length_m IS NULL`, wohnwagen); err != nil {
			return err
		}

		person := func(first, last, email string) (int64, error) {
			var id int64
			e := tx.QueryRow(ctx, `SELECT id FROM persons WHERE last_name=$1 AND first_name=$2`, last, first).Scan(&id)
			if e == nil {
				return id, nil
			}
			return id, tx.QueryRow(ctx,
				`INSERT INTO persons (first_name, last_name, email, phone)
				 VALUES ($1,$2,$3,'+43 660 0000000') RETURNING id`, first, last, email).Scan(&id)
		}
		anna, err := person("Anna", "Demo: Muster", "anna@example.com")
		if err != nil {
			return err
		}
		bernd, err := person("Bernd", "Demo: Beispiel", "bernd@example.com")
		if err != nil {
			return err
		}
		clara, err := person("Clara", "Demo: Probe", "")
		if err != nil {
			return err
		}

		vehicle := func(pid, cat int64, label, status, start string, rate float64) error {
			var exists bool
			if e := tx.QueryRow(ctx,
				`SELECT EXISTS (SELECT 1 FROM vehicles WHERE label=$1)`, label).Scan(&exists); e != nil || exists {
				return e
			}
			_, e := tx.Exec(ctx,
				`INSERT INTO vehicles (person_id, category_id, label, status, billing_period, rate, start_date)
				 VALUES ($1,$2,$3,$4,'monthly',$5,$6)`, pid, cat, label, status, rate, start)
			return e
		}
		start := time.Now().AddDate(0, -4, 0).Format("2006-01-02")
		if err := vehicle(anna, wohnwagen, "Demo: Hobby 460", "stored", start, 45); err != nil {
			return err
		}
		if err := vehicle(anna, oldtimer, "Demo: MG B GT", "stored", start, 60); err != nil {
			return err
		}
		if err := vehicle(bernd, wohnwagen, "Demo: Knaus Sport", "stored", start, 45); err != nil {
			return err
		}
		if err := vehicle(clara, oldtimer, "Demo: Käfer 1303", "reserved", time.Now().AddDate(0, 1, 0).Format("2006-01-02"), 60); err != nil {
			return err
		}

		// Zusatzkosten + eine erfasste Zahlung, damit Salden und Berichte etwas zeigen.
		var haveCharge bool
		if e := tx.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM charges WHERE description LIKE 'Demo:%')`).Scan(&haveCharge); e != nil {
			return e
		}
		if !haveCharge {
			if _, e := tx.Exec(ctx,
				`INSERT INTO charges (person_id, description, amount, quantity) VALUES
				 ($1, 'Demo: Stromanschluss', 12.50, 2),
				 ($2, 'Demo: Reinigung', 35.00, 1)`, anna, bernd); e != nil {
				return e
			}
			if _, e := tx.Exec(ctx,
				`INSERT INTO payments (person_id, amount, method, note) VALUES ($1, 90, 'ueberweisung', 'Demo: Anzahlung')`, anna); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		return 1
	}
	fmt.Println("seed-demo: Demo-Daten angelegt (Präfix \"Demo:\") — zweiter Lauf legt nichts doppelt an.")
	return 0
}
