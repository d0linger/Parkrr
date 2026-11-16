package handlers

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
)

// Restoring an earlier snapshot rolls every table back, including the revocation
// state of the capability links Parkrr hands out: customer portal links
// (self_service_tokens) and staff calendar feeds (calendar_feeds). A link revoked
// after the snapshot would work again until it expires. Sessions are simply purged
// after a restore; these links cannot be without cutting off every customer.
// Instead the links usable right BEFORE the restore are captured and reapplied
// afterwards: a restored link stays usable only if it was usable before.

// LiveLinks is the set of capability links usable immediately before a restore.
// The zero value means "unknown" and makes ReapplyLiveLinks revoke every link.
type LiveLinks struct {
	portal []string
	feeds  [][]byte
}

// CaptureLiveLinks reads the currently usable links. Any failure (an empty or
// foreign database) leaves the set empty, so every restored link is revoked
// afterwards — fail closed.
//
// "Usable" is resolvePortalPerson's full test, including the person: a link of an
// anonymized person does not resolve even while unrevoked (older data), and a
// backup from before the anonymization must not bring it back to life.
func CaptureLiveLinks(ctx context.Context, q dbQuerier) LiveLinks {
	portal, err := collectLinks[string](ctx, q,
		`SELECT token_hash FROM self_service_tokens
		  WHERE NOT revoked AND expires_at > now()
		    AND person_id IN (SELECT id FROM persons WHERE NOT anonymized)`)
	if err != nil {
		slog.Warn("restore: current portal links unreadable, all restored links will be revoked", "err", err)
		return LiveLinks{}
	}
	feeds, err := collectLinks[[]byte](ctx, q, `SELECT token_hash FROM calendar_feeds`)
	if err != nil {
		slog.Warn("restore: current calendar feeds unreadable, all restored links will be revoked", "err", err)
		return LiveLinks{}
	}
	return LiveLinks{portal: portal, feeds: feeds}
}

func collectLinks[T any](ctx context.Context, q dbQuerier, sql string) ([]T, error) {
	rows, err := q.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[T])
}

// ReapplyLiveLinks revokes every portal link and removes every calendar feed that
// was not usable before the restore. Run it after Migrate (an older archive may
// lack the tables) and before the application serves again.
func (h *Handler) ReapplyLiveLinks(ctx context.Context, live LiveLinks) error {
	portal, feeds := live.portal, live.feeds
	if portal == nil {
		portal = []string{}
	}
	if feeds == nil {
		feeds = [][]byte{}
	}
	revoked, err := h.Pool.Exec(ctx,
		`UPDATE self_service_tokens SET revoked = TRUE WHERE NOT revoked AND token_hash <> ALL($1::text[])`, portal)
	if err != nil {
		return fmt.Errorf("revoke restored portal links: %w", err)
	}
	removed, err := h.Pool.Exec(ctx, `DELETE FROM calendar_feeds WHERE token_hash <> ALL($1::bytea[])`, feeds)
	if err != nil {
		return fmt.Errorf("remove restored calendar feeds: %w", err)
	}
	if revoked.RowsAffected() > 0 || removed.RowsAffected() > 0 {
		h.AuditSystem(ctx, "revoke", "portal-link", 0,
			fmt.Sprintf("Nach dem Restore: %d Portal-Link(s) widerrufen, %d Kalender-Abo(s) entfernt, die vor dem Restore nicht mehr galten",
				revoked.RowsAffected(), removed.RowsAffected()),
			auditSnapshot(map[string]any{"portal_links": revoked.RowsAffected(), "calendar_feeds": removed.RowsAffected()}))
	}
	return nil
}
