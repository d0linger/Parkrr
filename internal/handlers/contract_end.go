package handlers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/preining/parkrr/internal/auth"
)

// Kunden vor Vertragsende erinnern (Ausbau 22). Standardmäßig aus
// (mail_automation.end_reminder_days = 0). Ist es an, geht X Tage vor dem
// Vertragsende eines eingelagerten oder reservierten Gefährts eine Mail an die Person:
// verlängern oder Abholtermin vereinbaren. Je Gefährt und Vertragsende genau einmal
// (contract_end_notices), auch wenn der Lauf stündlich prüft oder einen Tag verpasst.

func contractEndBody(name, label, plate string, end time.Time, seller string) string {
	var b strings.Builder
	if name != "" {
		fmt.Fprintf(&b, "Guten Tag %s,\n\n", name)
	} else {
		b.WriteString("Guten Tag,\n\n")
	}
	what := label
	if plate != "" && plate != label {
		what += " (" + plate + ")"
	}
	fmt.Fprintf(&b, "die Einlagerung von %s endet am %s.\n\n", what, end.Format("02.01.2006"))
	b.WriteString("Möchten Sie verlängern oder einen Abholtermin vereinbaren? Beides können Sie in Ihrem Kundenzugang anfragen, oder Sie antworten einfach auf diese Mail.\n\n")
	b.WriteString("Mit freundlichen Grüßen\n")
	if seller != "" {
		b.WriteString(seller + "\n")
	}
	return b.String()
}

// QueueContractEndNotices reiht die fälligen Erinnerungen ein und meldet ihre Zahl.
func (h *Handler) QueueContractEndNotices(ctx context.Context) (int, error) {
	var days int
	if err := h.Pool.QueryRow(ctx, `SELECT end_reminder_days FROM mail_automation WHERE id=1`).Scan(&days); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	if days <= 0 || h.Mail == nil || !h.Mail.Enabled() {
		return 0, nil
	}
	seller := ""
	if bs, err := h.loadBillingSettings(ctx); err == nil {
		seller = strings.TrimSpace(bs.SellerName)
	}
	today := startOfDayUTC(h.now())
	rows, err := h.Pool.Query(ctx,
		`SELECT v.id, v.person_id, v.end_date, COALESCE(NULLIF(v.label,''), c.name), v.license_plate,
		        btrim(p.email), trim(p.first_name || ' ' || p.last_name)
		   FROM vehicles v JOIN persons p ON p.id = v.person_id JOIN categories c ON c.id = v.category_id
		  WHERE NOT v.archived AND v.status IN ('stored', 'reserved') AND v.end_date IS NOT NULL
		    AND v.end_date BETWEEN $1 AND $2
		    AND NOT p.anonymized AND btrim(p.email) <> ''
		    AND NOT EXISTS (SELECT 1 FROM contract_end_notices n WHERE n.vehicle_id = v.id AND n.end_date = v.end_date)
		  ORDER BY v.end_date, v.id`, today, today.AddDate(0, 0, days))
	if err != nil {
		return 0, err
	}
	type due struct {
		vid, pid            int64
		end                 time.Time
		label, plate, email string
		name                string
	}
	var list []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.vid, &d.pid, &d.end, &d.label, &d.plate, &d.email, &d.name); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	queued := 0
	for _, d := range list {
		err := pgx.BeginFunc(ctx, h.Pool, func(tx pgx.Tx) error {
			ct, err := tx.Exec(ctx, `INSERT INTO contract_end_notices (vehicle_id, end_date) VALUES ($1, $2) ON CONFLICT DO NOTHING`, d.vid, d.end)
			if err != nil || ct.RowsAffected() == 0 {
				return err // schon erledigt (anderer Lauf) oder Fehler
			}
			ref, pid := d.vid, d.pid
			if _, err := enqueueMail(ctx, tx, outboxRow{Kind: "contract_end", RefID: &ref, PersonID: &pid, Recipient: d.email,
				Subject: "Ihre Einlagerung endet am " + d.end.Format("02.01.2006"),
				Body:    contractEndBody(d.name, d.label, d.plate, d.end, seller)}); err != nil {
				return err
			}
			queued++
			return nil
		})
		if err != nil {
			return queued, err
		}
	}
	if queued > 0 {
		h.AuditSystem(auth.ContextWithSystemActor(ctx), "notice", "system", 0,
			fmt.Sprintf("Vertragsende-Erinnerungen: %d eingereiht", queued), nil)
		h.KickOutbox()
	}
	return queued, nil
}
