package handlers

import (
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

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

// reTokenHash is the shape of hashPortalToken's output; anything else is not
// embedded as a literal (and so counts as not live — fail closed).
var reTokenHash = regexp.MustCompile(`^[0-9a-f]{64}$`)

// RestoreEpilogue renders the set as SQL for the restore transaction itself
// (backup.RestoreFileThen): restored links outside the set are revoked or removed
// BEFORE the restore commits. Pruning afterwards left a gap — a crash or a failed
// migration between commit and pruning kept revoked links usable, and a retry would
// then capture them as live. Tables an older archive lacks are skipped (PL/pgSQL
// plans a statement only when it runs). pg_restore empties the search_path, hence
// the qualified names.
func (l LiveLinks) RestoreEpilogue() string {
	portal := make([]string, 0, len(l.portal))
	for _, h := range l.portal {
		if reTokenHash.MatchString(h) {
			portal = append(portal, "'"+h+"'")
		}
	}
	feeds := make([]string, 0, len(l.feeds))
	for _, f := range l.feeds {
		feeds = append(feeds, "decode('"+hex.EncodeToString(f)+"', 'hex')")
	}
	return `DO $parkrr_links$
BEGIN
    IF to_regclass('public.self_service_tokens') IS NOT NULL THEN
        UPDATE public.self_service_tokens SET revoked = TRUE
         WHERE NOT revoked AND token_hash <> ALL (ARRAY[` + strings.Join(portal, ", ") + `]::text[]);
    END IF;
    IF to_regclass('public.calendar_feeds') IS NOT NULL THEN
        DELETE FROM public.calendar_feeds
         WHERE token_hash <> ALL (ARRAY[` + strings.Join(feeds, ", ") + `]::bytea[]);
    END IF;
END
$parkrr_links$;`
}

func collectLinks[T any](ctx context.Context, q dbQuerier, sql string) ([]T, error) {
	rows, err := q.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[T])
}

// ReapplyLiveLinks revokes every portal link and removes every calendar feed that
// was not usable before the restore. RestoreEpilogue already did this inside the
// restore; this second pass after Migrate is the safety net (a no-op normally) and
// the only pass for a recovered restore, where the set is unknown.
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
