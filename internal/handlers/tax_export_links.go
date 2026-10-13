package handlers

import (
	"context"
	"encoding/csv"
	"io"
	"strconv"
)

// writeTaxAllocationCSV exports relationship amounts separately from receipts,
// avoiding the common mistake of multiplying money when joining invoice lines.
func writeTaxAllocationCSV(ctx context.Context, q dbQuerier, dst io.Writer, ids []int64) error {
	rows, err := q.Query(ctx, `SELECT 'payment_allocation',a.id,a.payment_id,a.kind,a.ref_id,''::text,
		0::bigint,''::text,a.amount FROM payment_allocations a WHERE a.payment_id=ANY($1)
		UNION ALL SELECT 'invoice_payment',ip.id,ip.payment_id,'invoice',ip.invoice_id,'',
		i.id,i.number,ip.amount FROM invoice_payments ip JOIN invoices i ON i.id=ip.invoice_id WHERE ip.payment_id=ANY($1)
		UNION ALL SELECT 'period_receipt',p.id,p.id,p.settles_kind,p.settles_ref,p.settles_period,
		0::bigint,'',p.amount FROM payments p WHERE p.id=ANY($1) AND p.settles_kind IS NOT NULL AND p.settles_period<>'extras'
		ORDER BY 3,1,2`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	if _, err := io.WriteString(dst, "\ufeff"); err != nil {
		return err
	}
	writer := csv.NewWriter(dst)
	writer.Comma = ';'
	if err := writer.Write([]string{"beziehung", "allocation_id", "payment_id", "art", "referenz_id", "periode", "invoice_id", "rechnungsnummer", "zugeordnet_eur"}); err != nil {
		return err
	}
	for rows.Next() {
		var relation, kind, period, number string
		var id, paymentID, refID, invoiceID int64
		var amount float64
		if err := rows.Scan(&relation, &id, &paymentID, &kind, &refID, &period, &invoiceID, &number, &amount); err != nil {
			return err
		}
		// csvSafe neutralizes potential spreadsheet formula injection (CWE-1236)
		if err := writer.Write([]string{csvSafe(relation), strconv.FormatInt(id, 10), strconv.FormatInt(paymentID, 10), csvSafe(kind),
			strconv.FormatInt(refID, 10), csvSafe(period), strconv.FormatInt(invoiceID, 10), csvSafe(number), csvMoney(amount)}); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	writer.Flush()
	return writer.Error()
}
