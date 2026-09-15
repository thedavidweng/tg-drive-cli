package service

import (
	"context"
	"fmt"

	"github.com/thedavidweng/tg-drive-cli/core/telegram"
)

// ListChannels returns channels visible to the logged-in user.
func (a *App) ListChannels(ctx context.Context, onlyDrive bool) ([]telegram.Channel, error) {
	return a.TG.ListChannels(ctx, telegram.ListChannelsOptions{OnlyDrive: onlyDrive})
}

// LinkDiscussionGroup ensures the bound channel has a linked discussion
// group (creating one when needed) and records it on the channel row
// (ADR 0018).
func (a *App) LinkDiscussionGroup(ctx context.Context) (map[string]any, error) {
	channelRowID, _, err := a.channelID(ctx)
	if err != nil {
		return nil, err
	}
	tgChID, err := a.tgChannelID(ctx)
	if err != nil {
		return nil, err
	}
	group, err := a.TG.EnsureDiscussionGroup(ctx, tgChID)
	if err != nil {
		return nil, telegram.MapError(err)
	}
	if err := a.DB.SetDiscussionGroup(ctx, channelRowID, fmt.Sprintf("%d", group.ID), fmt.Sprintf("%d", group.AccessHash), group.Title); err != nil {
		return nil, err
	}
	return map[string]any{
		"discussion_channel_id": group.ID,
		"discussion_title":      group.Title,
	}, nil
}
