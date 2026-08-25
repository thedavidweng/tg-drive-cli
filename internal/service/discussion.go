package service

import (
	"context"

	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
)

// discussionChatID returns the linked discussion group's Telegram channel id
// for a channel row. Machine-record writes require it (ADR 0018); reads and
// scans work on legacy channels without one.
func (a *App) discussionChatID(ctx context.Context, channelRowID int64) (string, error) {
	tgID, _, _, err := a.DB.DiscussionGroup(ctx, channelRowID)
	if err != nil {
		return "", err
	}
	if tgID == "" {
		return "", apperr.New(apperr.ErrDiscussionMissing,
			"uploads need a linked discussion group for machine records; run: td channels link-discussion")
	}
	return tgID, nil
}
