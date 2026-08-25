package sqlitestore

import (
	"context"
	"database/sql"

	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
)

// DiscussionGroup returns the linked discussion group identity of a channel
// row: Telegram channel id, access hash, and title. All empty when the
// channel has no linked group (ADR 0018).
func (d *DB) DiscussionGroup(ctx context.Context, channelRowID int64) (tgID, accessHash, title string, err error) {
	err = d.sql.QueryRowContext(ctx, `
		select coalesce(discussion_tg_channel_id,''), coalesce(discussion_access_hash,''), coalesce(discussion_title,'')
		from channels where id=?`, channelRowID).Scan(&tgID, &accessHash, &title)
	if err != nil {
		return "", "", "", apperr.Wrap(apperr.ErrDB, "read discussion group", err)
	}
	return tgID, accessHash, title, nil
}

// SetDiscussionGroup stores the linked discussion group identity on a
// channel row. All values empty clears the link.
func (d *DB) SetDiscussionGroup(ctx context.Context, channelRowID int64, tgID, accessHash, title string) error {
	return d.WithTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			update channels set discussion_tg_channel_id=?, discussion_access_hash=?, discussion_title=?, updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')
			where id=?`, tgID, accessHash, title, channelRowID); err != nil {
			return apperr.Wrap(apperr.ErrDB, "store discussion group", err)
		}
		return nil
	})
}
