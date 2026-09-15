package telegram

import (
	"context"
	"fmt"
	"strconv"
)

// CarrierClient is the slice of the Telegram port a ManifestCarrier needs.
// Both the real adapter and the fake satisfy it.
type CarrierClient interface {
	SendTextReply(ctx context.Context, channelID int64, replyTo int, text string) (int, error)
	EditText(ctx context.Context, channelID int64, messageID int, text string) error
	DeleteMessage(ctx context.Context, channelID int64, messageID int) error
	GetMessage(ctx context.Context, channelID int64, messageID int) (Message, error)
	SendThreadReply(ctx context.Context, channelID int64, postMsgID int, text string) (int, error)
	EditThreadMessage(ctx context.Context, channelID int64, msgID int, text string) error
	DeleteThreadMessage(ctx context.Context, channelID int64, msgID int) error
}

// ManifestCarrier routes machine-record reads and writes to the Telegram
// destination that carries the record (ADR 0018): the linked discussion
// group's comment threads when the drive channel has one, the drive channel
// itself for legacy in-channel records. An empty chat id selects the legacy
// carrier.
type ManifestCarrier struct {
	tg CarrierClient
	// chatID is the linked discussion group's Telegram channel id, or ""
	// for the legacy in-channel carrier. It is the value persisted in
	// files.manifest_chat_tg_id.
	chatID string
}

// NewManifestCarrier binds a carrier to its client and carrier chat id. An
// empty chatID selects the legacy in-channel carrier.
func NewManifestCarrier(tg CarrierClient, chatID string) ManifestCarrier {
	return ManifestCarrier{tg: tg, chatID: chatID}
}

// ChatID returns the carrier chat id persisted on rows ("" for the legacy
// carrier).
func (c ManifestCarrier) ChatID() string { return c.chatID }

// Comment reports whether this carrier writes records as comments on the
// post's comment thread (ADR 0018) rather than as legacy in-channel replies.
func (c ManifestCarrier) Comment() bool { return c.chatID != "" }

// chatIDValue parses the carrier chat id into the int64 form the Telegram
// port uses.
func (c ManifestCarrier) chatIDValue() (int64, error) {
	id, err := strconv.ParseInt(c.chatID, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid discussion chat id %q", c.chatID)
	}
	return id, nil
}

// Send posts a new machine record for the drive-channel post postMsgID and
// returns the record's message id. Operations are addressed by the drive
// channel id; the carrier resolves the destination.
func (c ManifestCarrier) Send(ctx context.Context, driveChannelID int64, postMsgID int, text string) (int, error) {
	if c.Comment() {
		return c.tg.SendThreadReply(ctx, driveChannelID, postMsgID, text)
	}
	return c.tg.SendTextReply(ctx, driveChannelID, postMsgID, text)
}

// Edit rewrites the machine record msgID on its carrier.
func (c ManifestCarrier) Edit(ctx context.Context, driveChannelID int64, msgID int, text string) error {
	if c.Comment() {
		return c.tg.EditThreadMessage(ctx, driveChannelID, msgID, text)
	}
	return c.tg.EditText(ctx, driveChannelID, msgID, text)
}

// Delete removes the machine record msgID from its carrier.
func (c ManifestCarrier) Delete(ctx context.Context, driveChannelID int64, msgID int) error {
	if c.Comment() {
		return c.tg.DeleteThreadMessage(ctx, driveChannelID, msgID)
	}
	return c.tg.DeleteMessage(ctx, driveChannelID, msgID)
}

// Get reads the machine record msgID from its carrier.
func (c ManifestCarrier) Get(ctx context.Context, driveChannelID int64, msgID int) (Message, error) {
	if !c.Comment() {
		return c.tg.GetMessage(ctx, driveChannelID, msgID)
	}
	id, err := c.chatIDValue()
	if err != nil {
		return Message{}, err
	}
	return c.tg.GetMessage(ctx, id, msgID)
}
