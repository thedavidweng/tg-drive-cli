package mtproto

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram/query"
	"github.com/gotd/td/telegram/query/dialogs"
	"github.com/gotd/td/tg"
)

// formatTDChannelTitle mirrors Telegram-Drive create_folder_inner title convention.
func formatTDChannelTitle(name string) string {
	if strings.Contains(strings.ToLower(name), "[td]") {
		return strings.TrimSpace(name)
	}
	return strings.TrimSpace(name) + " [TD]"
}

const tdChannelAbout = "Telegram Drive Storage Folder\n[telegram-drive-folder]"

func channelTitleMatches(want, actual string) bool {
	want = strings.TrimSpace(want)
	actual = strings.TrimSpace(actual)
	if want == "" || actual == "" {
		return false
	}
	if want == actual || strings.EqualFold(want, actual) {
		return true
	}
	return formatTDChannelTitle(want) == actual
}

func (c *Client) resolveChannelPeer(ctx context.Context, api *tg.Client, titleOrID string) (*tg.InputPeerChannel, error) {
	titleOrID = strings.TrimSpace(titleOrID)
	if id, err := parseChannelID(titleOrID); err == nil {
		if hash, ok := c.channelAccessHash(id); ok {
			return &tg.InputPeerChannel{ChannelID: id, AccessHash: hash}, nil
		}
		titleOrID = strconv.FormatInt(id, 10)
	}

	var found *tg.InputPeerChannel
	err := query.GetDialogs(api).ForEach(ctx, func(ctx context.Context, elem dialogs.Elem) error {
		pch, ok := elem.Peer.(*tg.InputPeerChannel)
		if !ok {
			return nil
		}
		c.rememberChannel(pch.ChannelID, pch.AccessHash)

		title := ""
		if ch, ok := elem.Entities.Channel(pch.ChannelID); ok {
			title = ch.Title
		}
		idStr := fmt.Sprintf("%d", pch.ChannelID)
		if idStr == titleOrID || channelTitleMatches(titleOrID, title) {
			found = &tg.InputPeerChannel{ChannelID: pch.ChannelID, AccessHash: pch.AccessHash}
		}
		return nil
	})
	if err != nil {
		return nil, mapRPCError(err)
	}
	if found != nil {
		return found, nil
	}
	return nil, errors.New("channel not found")
}
