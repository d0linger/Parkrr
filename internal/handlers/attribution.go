package handlers

import (
	"context"
	"time"
)

// attrItem is one resolved position a payment (or invoice) is attributed to: which
// Gefährt / Pauschale / Nebenkosten / Zusatzkosten, which Zeitraum, and — for a
// payment — the share of the amount. It lets the person overview show what a payment
// settled, even when several Gefährte or Pauschalen are covered by one payment.
type attrItem struct {
	Kind   string  `json:"kind"` // vehicle | charge | agreement | recurring | invoice
	Label  string  `json:"label"`
	Period string  `json:"period,omitempty"`
	Amount float64 `json:"amount,omitempty"`
}

// deDay formats a date like the UI does (de-DE, no leading zeros): "6.8.2026".
func deDay(t time.Time) string { return t.Format("2.1.2006") }

// vehicleRentPeriod renders a Gefährt's rent window: "seit 1.1.2026" (open) or
// "1.1.2025 – 6.8.2026" (closed).
func vehicleRentPeriod(start time.Time, end *time.Time) string {
	if end != nil {
		return deDay(start) + " – " + deDay(*end)
	}
	return "seit " + deDay(start)
}

// resolvePaymentItems returns, per payment id, the positions it settles — resolved
// from the structured links: payment_allocations (Gefährt-Miete / Zusatzkosten),
// settles_* (Pauschale / Nebenkosten period), and invoice_payments (Rechnung).
// Reversed payments are skipped (they carry no money). Batched — no N+1.
func (h *Handler) resolvePaymentItems(ctx context.Context, personID int64) (map[int64][]attrItem, error) {
	out := map[int64][]attrItem{}

	// (1) Vehicle-rent and one-off Zusatzkosten allocations.
	arows, err := h.Pool.Query(ctx,
		`SELECT pa.payment_id, pa.kind, pa.amount,
		        COALESCE(NULLIF(v.label,''), NULLIF(v.license_plate,''), 'Gefährt'), v.start_date, v.end_date,
		        c.description, c.charged_on
		   FROM payment_allocations pa
		   JOIN payments p ON p.id = pa.payment_id
		   LEFT JOIN vehicles v ON pa.kind='vehicle' AND v.id = pa.ref_id
		   LEFT JOIN charges  c ON pa.kind='charge'  AND c.id = pa.ref_id
		  WHERE p.person_id=$1 AND NOT p.reversed
		  ORDER BY pa.payment_id, pa.id`, personID)
	if err != nil {
		return nil, err
	}
	for arows.Next() {
		var pid int64
		var kind string
		var amt float64
		var vLabel *string
		var vStart, vEnd, chargedOn *time.Time
		var chargeDesc *string
		if err := arows.Scan(&pid, &kind, &amt, &vLabel, &vStart, &vEnd, &chargeDesc, &chargedOn); err != nil {
			arows.Close()
			return nil, err
		}
		switch kind {
		case "vehicle":
			it := attrItem{Kind: "vehicle", Label: derefStr(vLabel, "Gefährt"), Amount: amt}
			if vStart != nil {
				it.Period = vehicleRentPeriod(*vStart, vEnd)
			}
			out[pid] = append(out[pid], it)
		case "charge":
			it := attrItem{Kind: "charge", Label: derefStr(chargeDesc, "Zusatzkosten"), Amount: amt}
			if chargedOn != nil {
				it.Period = deDay(*chargedOn)
			}
			out[pid] = append(out[pid], it)
		}
	}
	arows.Close()
	if err := arows.Err(); err != nil {
		return nil, err
	}

	// (2) Pauschale period settlements. The 'extras' bundle is described by its charge
	// allocations (handled above), so skip it here.
	srows, err := h.Pool.Query(ctx,
		`SELECT p.id, p.settles_period, p.amount,
		        (SELECT string_agg(COALESCE(NULLIF(v.label,''), NULLIF(v.license_plate,''), 'Gefährt'), ', ' ORDER BY v.id)
		           FROM flat_rate_period_vehicles fv JOIN vehicles v ON v.id = fv.vehicle_id
		          WHERE fv.period_id = p.settles_ref)
		   FROM payments p
		  WHERE p.person_id=$1 AND NOT p.reversed AND p.settles_kind='agreement' AND p.settles_period <> 'extras'`, personID)
	if err != nil {
		return nil, err
	}
	for srows.Next() {
		var pid int64
		var period string
		var amt float64
		var vehLabels *string
		if err := srows.Scan(&pid, &period, &amt, &vehLabels); err != nil {
			srows.Close()
			return nil, err
		}
		label := "Pauschale"
		if vehLabels != nil && *vehLabels != "" {
			label += " · " + *vehLabels
		}
		out[pid] = append(out[pid], attrItem{Kind: "agreement", Label: label, Period: period, Amount: amt})
	}
	srows.Close()
	if err := srows.Err(); err != nil {
		return nil, err
	}

	// (3) Recurring Nebenkosten period settlements.
	rrows, err := h.Pool.Query(ctx,
		`SELECT p.id, p.settles_period, p.amount, rc.description
		   FROM payments p JOIN recurring_charges rc ON rc.id = p.settles_ref
		  WHERE p.person_id=$1 AND NOT p.reversed AND p.settles_kind='recurring'`, personID)
	if err != nil {
		return nil, err
	}
	for rrows.Next() {
		var pid int64
		var period, desc string
		var amt float64
		if err := rrows.Scan(&pid, &period, &amt, &desc); err != nil {
			rrows.Close()
			return nil, err
		}
		out[pid] = append(out[pid], attrItem{Kind: "recurring", Label: desc, Period: period, Amount: amt})
	}
	rrows.Close()
	if err := rrows.Err(); err != nil {
		return nil, err
	}

	// (4) Payments that settled an invoice.
	irows, err := h.Pool.Query(ctx,
		`SELECT ip.payment_id, i.number, ip.amount
		   FROM invoice_payments ip
		   JOIN invoices i ON i.id = ip.invoice_id
		   JOIN payments p ON p.id = ip.payment_id
		  WHERE p.person_id=$1 AND NOT p.reversed`, personID)
	if err != nil {
		return nil, err
	}
	for irows.Next() {
		var pid int64
		var number string
		var amt float64
		if err := irows.Scan(&pid, &number, &amt); err != nil {
			irows.Close()
			return nil, err
		}
		out[pid] = append(out[pid], attrItem{Kind: "invoice", Label: "Rechnung " + number, Amount: amt})
	}
	irows.Close()
	return out, irows.Err()
}

// resolveInvoiceItems returns, per invoice id, the positions it bills — resolved from
// invoice_source (the structured lock, not the free-text line descriptions): which
// Gefährt / Pauschale / Nebenkosten / Zusatzkosten and which Periode. Amounts are
// omitted (the invoice total is shown on the row); this feeds the one-line summary.
func (h *Handler) resolveInvoiceItems(ctx context.Context, personID int64) (map[int64][]attrItem, error) {
	out := map[int64][]attrItem{}
	rows, err := h.Pool.Query(ctx,
		`SELECT s.invoice_id, s.kind, s.period_key,
		        COALESCE(NULLIF(v.label,''), NULLIF(v.license_plate,''), 'Gefährt'),
		        c.description, rc.description,
		        (SELECT string_agg(COALESCE(NULLIF(v2.label,''), NULLIF(v2.license_plate,''), 'Gefährt'), ', ' ORDER BY v2.id)
		           FROM flat_rate_period_vehicles fv JOIN vehicles v2 ON v2.id = fv.vehicle_id
		          WHERE s.kind='agreement' AND fv.period_id = s.ref_id)
		   FROM invoice_source s
		   JOIN invoices i ON i.id = s.invoice_id
		   LEFT JOIN vehicles v ON s.kind='vehicle' AND v.id = s.ref_id
		   LEFT JOIN charges  c ON s.kind='charge'  AND c.id = s.ref_id
		   LEFT JOIN recurring_charges rc ON s.kind='recurring' AND rc.id = s.ref_id
		  WHERE i.person_id=$1 AND NOT i.canceled
		  ORDER BY s.invoice_id, s.kind, s.period_key`, personID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var invID int64
		var kind, period string
		var vLabel string
		var chargeDesc, recDesc, agLabels *string
		if err := rows.Scan(&invID, &kind, &period, &vLabel, &chargeDesc, &recDesc, &agLabels); err != nil {
			return nil, err
		}
		it := attrItem{Kind: kind, Period: period}
		switch kind {
		case "vehicle":
			it.Label = vLabel
		case "charge":
			it.Label = derefStr(chargeDesc, "Zusatzkosten")
		case "recurring":
			it.Label = derefStr(recDesc, "Nebenkosten")
		case "agreement":
			it.Label = "Pauschale"
			if agLabels != nil && *agLabels != "" {
				it.Label += " · " + *agLabels
			}
		default:
			it.Label = kind
		}
		out[invID] = append(out[invID], it)
	}
	return out, rows.Err()
}

func derefStr(s *string, fallback string) string {
	if s == nil || *s == "" {
		return fallback
	}
	return *s
}
