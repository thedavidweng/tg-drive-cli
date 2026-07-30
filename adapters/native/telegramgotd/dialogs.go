package telegramgotd

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/query"
	"github.com/gotd/td/telegram/query/dialogs"
	"github.com/gotd/td/tg"
	tgtelegram "github.com/thedavidweng/tg-drive-cli/core/telegram"
)

// formatTDChannelTitle mirrors Telegram-Drive create_folder_inner title convention.
func formatTDChannelTitle(name string) string {
	if strings.Contains(strings.ToLower(name), "[td]") {
		return strings.TrimSpace(name)
	}
	return strings.TrimSpace(name) + " [TD]"
}

const tdChannelAbout = "Telegram Drive Storage Folder\n[telegram-drive-folder]"

var errResolveDone = errors.New("channel resolved")

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

// ListChannels returns channels accessible to the logged-in user.
func (c *Client) ListChannels(ctx context.Context, opts tgtelegram.ListChannelsOptions) ([]tgtelegram.Channel, error) {
	var out []tgtelegram.Channel
	err := c.run(ctx, func(ctx context.Context, api *tg.Client, _ *telegram.Client) error {
		return query.GetDialogs(api).ForEach(ctx, func(ctx context.Context, elem dialogs.Elem) error {
			pch, ok := elem.Peer.(*tg.InputPeerChannel)
			if !ok {
				return nil
			}
			c.rememberChannel(pch.ChannelID, pch.AccessHash)
			title := ""
			if ch, ok := elem.Entities.Channel(pch.ChannelID); ok {
				title = ch.Title
			}
			c.rememberChannelTitle(pch.ChannelID, title)
			if opts.OnlyDrive && !strings.Contains(strings.ToLower(title), "[td]") {
				return nil
			}
			out = append(out, tgtelegram.Channel{
				ID:         pch.ChannelID,
				AccessHash: pch.AccessHash,
				Title:      title,
			})
			return nil
		})
	})
	if err != nil {
		return nil, mapRPCError(err)
	}
	return out, nil
}

func (c *Client) resolveChannelPeer(ctx context.Context, api *tg.Client, titleOrID string) (*tg.InputPeerChannel, error) {
	titleOrID = strings.TrimSpace(titleOrID)
	if id, err := parseChannelID(titleOrID); err == nil {
		if hash, ok := c.channelAccessHash(id); ok {
			return &tg.InputPeerChannel{ChannelID: id, AccessHash: hash}, nil
		}
		titleOrID = strconv.FormatInt(id, 10)
	} else if id, ok := c.channelByTitle(titleOrID); ok {
		if hash, ok := c.channelAccessHash(id); ok {
			return &tg.InputPeerChannel{ChannelID: id, AccessHash: hash}, nil
		}
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
		c.rememberChannelTitle(pch.ChannelID, title)
		idStr := fmt.Sprintf("%d", pch.ChannelID)
		if idStr == titleOrID || channelTitleMatches(titleOrID, title) {
			found = &tg.InputPeerChannel{ChannelID: pch.ChannelID, AccessHash: pch.AccessHash}
			return errResolveDone
		}
		return nil
	})
	if err != nil && !errors.Is(err, errResolveDone) {
		return nil, mapRPCError(err)
	}
	if found != nil {
		return found, nil
	}
	return nil, errors.New("channel not found")
}
