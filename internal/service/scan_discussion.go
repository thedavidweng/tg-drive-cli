package service

import (
	"context"
	"errors"
	"fmt"
	"sort"

	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
	"github.com/thedavidweng/tg-drive-cli/core/manifest"
	"github.com/thedavidweng/tg-drive-cli/core/telegram"
)

// collectDiscussionComments runs the ADR 0018 pass of a scan: walk the
// linked discussion group and fold its records into the run state —
// forwarded headers map thread roots back to channel posts, comments carry
// the machine records (td-manifest:v1 per file, td-album:v1 per group). The
// walk has its own completeness proof: a truncated thread read aborts the
// scan exactly like a truncated channel read. The returned value is the
// highest discussion-group message id seen, for the scan cursor.
func (a *App) collectDiscussionComments(ctx context.Context, r *scanRun) (int, error) {
	discTGID, _, _, err := a.DB.DiscussionGroup(ctx, r.channelID)
	if err != nil {
		return 0, err
	}
	if discTGID == "" {
		return 0, nil
	}
	r.manifestChat = discTGID
	discAfter := 0
	if !r.opts.Full {
		_ = a.DB.Raw().QueryRowContext(ctx,
			`select coalesce(discussion_last_scanned_message_id,0) from scan_state where channel_id=?`, r.channelID).Scan(&discAfter)
	}
	discMaxID := 0
	rootPost := map[int]int{}
	type pendingComment struct {
		rootID, msgID int
		text          string
	}
	var comments []pendingComment
	threadMeta, threadErr := a.TG.StreamThreadHistory(ctx, r.tgChID, discAfter, func(tm telegram.ThreadMessage) error {
		tm.Data = nil
		if tm.ID > discMaxID {
			discMaxID = tm.ID
		}
		if tm.RootMsgID != 0 {
			rootPost[tm.RootMsgID] = tm.PostID
			return nil
		}
		if tm.Text == "" {
			return nil
		}
		root := 0
		if tm.ReplyTo != nil {
			root = *tm.ReplyTo
		}
		comments = append(comments, pendingComment{rootID: root, msgID: tm.ID, text: tm.Text})
		return nil
	})
	if threadErr != nil {
		var missing *telegram.DiscussionMissingError
		if !errors.As(threadErr, &missing) {
			return 0, telegram.MapError(threadErr)
		}
		// The group vanished between the DB read and the walk; treat the
		// channel as legacy and continue without comment records.
		r.manifestChat = ""
		return 0, nil
	}
	if !threadMeta.Complete {
		return 0, apperr.New(apperr.ErrScanIncomplete,
			fmt.Sprintf("discussion thread read stopped early (oldest message seen: %d, group reports %d messages); "+
				"the index was not modified — rerun the scan", threadMeta.OldestID, threadMeta.TotalMessages))
	}
	// Oldest first, so the newest comment per post overwrites into
	// commentByMedia (newest-comment-wins).
	sort.Slice(comments, func(i, j int) bool { return comments[i].msgID < comments[j].msgID })
	for _, c := range comments {
		postID, ok := rootPost[c.rootID]
		if !ok || postID == 0 {
			// Human chatter or a root outside this pass's window.
			continue
		}
		if manifest.IsAlbumReply(c.text) {
			album, err := manifest.ParseAlbumReply(c.text)
			if err != nil {
				r.deferredErrors = append(r.deferredErrors, deferredScanError{
					messageID: c.msgID,
					code:      apperr.ErrAlbumInventoryInvalid,
					message:   "album inventory comment unparseable: " + err.Error(),
					excerpt:   truncate(c.text, 200),
				})
				continue
			}
			r.albumInventories = append(r.albumInventories, albumInventory{replyID: c.msgID, chat: discTGID, meta: album})
			r.coveredGroups[album.GroupedID] = true
			continue
		}
		if meta, err := manifest.ParseManifestReply(c.text); err == nil {
			r.commentByMedia[postID] = manifestReply{meta: meta, msgID: c.msgID}
		}
	}
	return discMaxID, nil
}
