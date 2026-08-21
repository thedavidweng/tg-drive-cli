package fake

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/thedavidweng/tg-drive-cli/core/telegram"
)

// Client is an in-memory fake Telegram client for tests.
type Client struct {
	mu        sync.Mutex
	statePath string // non-empty: persist state across processes (NewPersistent)
	user      *telegram.User
	loggedIn  bool
	channels  map[int64]*telegram.Channel
	messages  map[int64][]telegram.Message
	nextID    int
	nextChID  int64
	code      string
	password  string
	failCode  bool

	failUpload         bool
	failReply          bool
	failDelete         bool
	failDeleteAfterOne bool
	deleteCalls        int
	failEditText       bool
	denyPerms          bool

	// Resumable-upload simulation knobs. The resumable path only engages
	// above ResumableBigFileBytes, matching the real adapter's uploader
	// selection; resumableThreshold lowers that cutoff for tests. partSize
	// overrides the default part size so tests can exercise multi-part files
	// without large fixtures; failUploadAfterParts fails the upload once that
	// many parts are confirmed (state stays persisted for resume);
	// partSubmissions counts every part byte-transfer attempt across uploads.
	resumableThreshold   int
	partSize             int
	failUploadAfterParts int
	partSubmissions      int64

	// truncateHistory limits history reads to the newest N messages without
	// reporting completion, simulating a Telegram pagination quirk. 0 disables.
	truncateHistory int
}

// New creates a fake client.
func New() *Client {
	return &Client{
		channels: make(map[int64]*telegram.Channel),
		messages: make(map[int64][]telegram.Message),
		nextID:   1,
		nextChID: 1000,
		code:     "12345",
	}
}

func (c *Client) SetCredentials(code, password string) {
	c.code = code
	c.password = password
}

func (c *Client) SetFailCode(v bool) { c.failCode = v }

// SetFailUpload makes UploadMedia fail (test hook).
func (c *Client) SetFailUpload(v bool) { c.failUpload = v }

// SetFailReply makes SendTextReply fail (test hook).
func (c *Client) SetFailReply(v bool) { c.failReply = v }

// SetFailDelete makes DeleteMessage fail (test hook).
func (c *Client) SetFailDelete(v bool) { c.failDelete = v }

// SetFailDeleteAfterFirst lets the next DeleteMessage succeed, then fails.
func (c *Client) SetFailDeleteAfterFirst(v bool) {
	c.failDeleteAfterOne = v
	c.deleteCalls = 0
}

// SetFailEditText makes EditText fail (test hook).
func (c *Client) SetFailEditText(v bool) { c.failEditText = v }

// SetDenyPermissions makes mutating calls return PermissionDeniedError (test hook).
func (c *Client) SetDenyPermissions(v bool) { c.denyPerms = v }

// SetPartSize overrides the simulated resumable-upload part size in bytes.
func (c *Client) SetPartSize(n int) { c.partSize = n }

// SetResumableThreshold lowers the size above which uploads take the
// simulated resumable path (tests only).
func (c *Client) SetResumableThreshold(n int) { c.resumableThreshold = n }

// SetFailUploadAfterParts fails a resumable upload once n parts are confirmed.
func (c *Client) SetFailUploadAfterParts(n int) { c.failUploadAfterParts = n }

// PartSubmissions reports how many upload parts were submitted since the last
// reset. Tests use it to assert confirmed parts are not re-sent on resume.
func (c *Client) PartSubmissions() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.partSubmissions
}

// ResetPartSubmissions zeroes the part submission counter.
func (c *Client) ResetPartSubmissions() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.partSubmissions = 0
}

// SetTruncateHistory makes history reads return only the newest n messages
// and report the read as incomplete (simulates a Telegram pagination quirk).
func (c *Client) SetTruncateHistory(n int) { c.truncateHistory = n }

func (c *Client) Login(ctx context.Context, apiID int64, apiHash, phone string, codeFn telegram.CodeFunc, passwordFn telegram.PasswordFunc, opts telegram.LoginOptions) (*telegram.LoginResult, error) {
	if apiID == 0 || apiHash == "" || phone == "" {
		return nil, &telegram.AuthRequiredError{}
	}
	c.mu.Lock()
	if c.loggedIn {
		user := *c.user
		c.mu.Unlock()
		return &telegram.LoginResult{User: user, AlreadyAuthorized: true}, nil
	}
	c.mu.Unlock()
	code, err := codeFn(telegram.CodePrompt{Attempt: 1})
	if err != nil {
		return nil, err
	}
	if c.failCode || code != c.code {
		return nil, &telegram.CodeInvalidError{Attempts: 1}
	}
	if c.password != "" {
		pw, err := passwordFn()
		if err != nil {
			return nil, err
		}
		if pw != c.password {
			return nil, &telegram.PasswordInvalidError{}
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loggedIn = true
	c.user = &telegram.User{ID: 42, Phone: phone, DisplayName: "Test User"}
	c.save()
	return &telegram.LoginResult{User: *c.user}, nil
}

func (c *Client) Status(ctx context.Context) (*telegram.User, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.loggedIn {
		return nil, false, nil
	}
	return c.user, true, nil
}

func (c *Client) Logout(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loggedIn = false
	c.user = nil
	c.save()
	return nil
}

func (c *Client) CreateChannel(ctx context.Context, title string) (*telegram.Channel, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.loggedIn {
		return nil, &telegram.AuthRequiredError{}
	}
	c.nextChID++
	ch := &telegram.Channel{ID: c.nextChID, Title: title, InviteLink: fmt.Sprintf("https://t.me/+fake%d", c.nextChID)}
	c.channels[ch.ID] = ch
	c.save()
	return ch, nil
}

func (c *Client) ResolveChannel(ctx context.Context, titleOrID string) (*telegram.Channel, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, ch := range c.channels {
		if ch.Title == titleOrID || fmt.Sprintf("%d", ch.ID) == titleOrID {
			return ch, nil
		}
	}
	return nil, fmt.Errorf("channel not found")
}

func (c *Client) ListChannels(ctx context.Context, opts telegram.ListChannelsOptions) ([]telegram.Channel, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.loggedIn {
		return nil, &telegram.AuthRequiredError{}
	}
	var out []telegram.Channel
	for _, ch := range c.channels {
		if !opts.OnlyDrive || strings.Contains(ch.Title, "[TD]") {
			out = append(out, *ch)
		}
	}
	return out, nil
}

func (c *Client) GetInviteLink(ctx context.Context, channelID int64) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch, ok := c.channels[channelID]
	if !ok {
		return "", fmt.Errorf("channel not found")
	}
	return ch.InviteLink, nil
}

func (c *Client) UploadMedia(ctx context.Context, req telegram.UploadRequest) (*telegram.UploadResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.loggedIn {
		return nil, &telegram.AuthRequiredError{}
	}
	if c.denyPerms {
		return nil, &telegram.PermissionDeniedError{}
	}
	if c.failUpload {
		return nil, fmt.Errorf("upload failed")
	}
	if req.Size > 4*1024*1024*1024 {
		return nil, &telegram.FileTooLargeError{}
	}
	switch req.Kind {
	case telegram.KindNone, telegram.KindDocument, telegram.KindPhoto, telegram.KindVideo:
	default:
		return nil, fmt.Errorf("unsupported media kind %q", req.Kind)
	}
	threshold := c.resumableThreshold
	if threshold <= 0 {
		threshold = telegram.ResumableBigFileBytes
	}
	var data []byte
	if req.ResumableKey != "" && req.ResumableStore != nil && req.Size > int64(threshold) {
		var err error
		data, err = c.uploadResumable(ctx, req)
		if err != nil {
			return nil, err
		}
	} else {
		if req.Reader == nil {
			return nil, fmt.Errorf("upload requires a reader or a resumable path")
		}
		var err error
		data, err = io.ReadAll(req.Reader)
		if err != nil {
			return nil, err
		}
	}
	id := c.nextID
	c.nextID++
	kind := req.Kind
	if kind == telegram.KindNone {
		kind = telegram.KindDocument
	}
	msg := telegram.Message{ID: id, Caption: req.Caption, FileSize: req.Size, Kind: kind, Data: data, Video: req.Video, Thumb: req.Thumb}
	if kind == telegram.KindPhoto {
		// Native photos carry no filename; Telegram reports them as JPEG.
		msg.MIME = "image/jpeg"
	} else {
		msg.FileName = req.FileName
		msg.MIME = req.MIME
	}
	c.messages[req.ChannelID] = append(c.messages[req.ChannelID], msg)
	c.save()
	return &telegram.UploadResult{MessageID: id}, nil
}

// uploadResumable simulates Telegram's saveBigFilePart protocol: parts are
// confirmed one at a time against the persisted state, only unconfirmed parts
// are submitted on retry, and an injected failure keeps the state for resume.
// Callers hold c.mu.
func (c *Client) uploadResumable(ctx context.Context, req telegram.UploadRequest) ([]byte, error) {
	var data []byte
	var err error
	switch {
	case req.Reader != nil:
		data, err = io.ReadAll(req.Reader)
	case req.Path != "":
		data, err = os.ReadFile(req.Path)
	default:
		return nil, fmt.Errorf("upload requires a reader or a local path")
	}
	if err != nil {
		return nil, err
	}
	partSize := c.partSize
	if partSize <= 0 {
		partSize = req.PartSize
	}
	if partSize <= 0 {
		partSize = 128 * 1024
	}
	totalParts := (len(data) + partSize - 1) / partSize

	state, err := req.ResumableStore.LoadUploadState(ctx, req.ResumableKey)
	if err != nil {
		state = nil
	}
	if state != nil && (state.TotalBytes != req.Size || state.TotalParts != totalParts ||
		(req.ContentHash != "" && state.ContentHash != req.ContentHash)) {
		state = nil
	}
	if state == nil {
		state = &telegram.UploadState{
			FileID:      int64(time.Now().UnixNano()),
			PartSize:    partSize,
			TotalParts:  totalParts,
			TotalBytes:  req.Size,
			ContentHash: req.ContentHash,
		}
	}
	confirmed := map[int]bool{}
	for _, p := range state.ConfirmedParts {
		confirmed[p] = true
	}
	confirmedCount := len(confirmed)
	for i := 0; i < totalParts; i++ {
		if confirmed[i] {
			continue
		}
		c.partSubmissions++
		if c.failUploadAfterParts > 0 && confirmedCount >= c.failUploadAfterParts {
			// Interrupt: persist what is confirmed so far, then fail. The knob
			// fires once so a retry can get through.
			c.failUploadAfterParts = 0
			saveErr := c.saveUploadState(ctx, req, state, confirmed)
			if saveErr != nil {
				return nil, saveErr
			}
			return nil, fmt.Errorf("upload interrupted after %d confirmed parts", confirmedCount)
		}
		confirmed[i] = true
		confirmedCount++
		if err := c.saveUploadState(ctx, req, state, confirmed); err != nil {
			return nil, err
		}
		if req.Progress != nil {
			uploaded := int64(min((i+1)*partSize, len(data)))
			if err := req.Progress(ctx, telegram.UploadProgressState{
				FileName: req.FileName,
				Part:     i,
				PartSize: partSize,
				Uploaded: uploaded,
				Total:    req.Size,
			}); err != nil {
				return nil, err
			}
		}
	}
	if err := req.ResumableStore.DeleteUploadState(ctx, req.ResumableKey); err != nil {
		return nil, err
	}
	return data, nil
}

func (c *Client) saveUploadState(ctx context.Context, req telegram.UploadRequest, state *telegram.UploadState, confirmed map[int]bool) error {
	parts := make([]int, 0, len(confirmed))
	for p := range confirmed {
		parts = append(parts, p)
	}
	sort.Ints(parts)
	st := *state
	st.ConfirmedParts = parts
	st.ConfirmedBytes = int64(len(parts)) * int64(state.PartSize)
	return req.ResumableStore.SaveUploadState(ctx, req.ResumableKey, &st)
}

func (c *Client) SendTextReply(ctx context.Context, channelID int64, replyTo int, text string) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failReply {
		return 0, fmt.Errorf("reply failed")
	}
	id := c.nextID
	c.nextID++
	rt := replyTo
	c.messages[channelID] = append(c.messages[channelID], telegram.Message{ID: id, Text: text, ReplyTo: &rt})
	c.save()
	return id, nil
}

func (c *Client) EditCaption(ctx context.Context, channelID int64, messageID int, caption string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.denyPerms {
		return &telegram.PermissionDeniedError{}
	}
	for i, m := range c.messages[channelID] {
		if m.ID == messageID {
			if m.NotEditable {
				return &telegram.MessageNotEditableError{}
			}
			c.messages[channelID][i].Caption = caption
			c.save()
			return nil
		}
	}
	return &telegram.MessageNotFoundError{}
}

func (c *Client) EditText(ctx context.Context, channelID int64, messageID int, text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failEditText {
		return fmt.Errorf("edit text failed")
	}
	for i, m := range c.messages[channelID] {
		if m.ID == messageID {
			c.messages[channelID][i].Text = text
			c.save()
			return nil
		}
	}
	return &telegram.MessageNotFoundError{}
}

func (c *Client) DeleteMessage(ctx context.Context, channelID int64, messageID int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.denyPerms {
		return &telegram.PermissionDeniedError{}
	}
	c.deleteCalls++
	if c.failDelete || (c.failDeleteAfterOne && c.deleteCalls > 1) {
		return fmt.Errorf("delete failed")
	}
	msgs := c.messages[channelID]
	for i, m := range msgs {
		if m.ID == messageID {
			c.messages[channelID] = append(msgs[:i], msgs[i+1:]...)
			c.save()
			return nil
		}
	}
	return &telegram.MessageNotFoundError{}
}

func (c *Client) DownloadMedia(ctx context.Context, channelID int64, messageID int, dst io.Writer) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, m := range c.messages[channelID] {
		if m.ID != messageID {
			continue
		}
		if len(m.Data) > 0 {
			_, err := dst.Write(m.Data)
			return err
		}
		if m.Kind == telegram.KindText || m.Text != "" {
			body := m.Text
			if body == "" {
				body = m.Caption
			}
			_, err := dst.Write([]byte(body))
			return err
		}
		return fmt.Errorf("message has no downloadable content")
	}
	return &telegram.MessageNotFoundError{}
}

func (c *Client) GetMessage(ctx context.Context, channelID int64, messageID int) (telegram.Message, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, m := range c.messages[channelID] {
		if m.ID == messageID {
			return m, nil
		}
	}
	return telegram.Message{}, &telegram.MessageNotFoundError{}
}

// AddMessage injects a pre-built message (test hook for photos/text/unmanaged docs).
func (c *Client) AddMessage(channelID int64, msg telegram.Message) telegram.Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	if msg.ID == 0 {
		msg.ID = c.nextID
		c.nextID++
	} else if msg.ID >= c.nextID {
		c.nextID = msg.ID + 1
	}
	c.messages[channelID] = append(c.messages[channelID], msg)
	c.save()
	return msg
}

func (c *Client) Doctor(ctx context.Context, channelID int64) (*telegram.Capabilities, error) {
	return &telegram.Capabilities{
		AuthOK:           c.loggedIn,
		ChannelOK:        true,
		UploadOK:         true,
		DeleteOK:         true,
		InviteLinkOK:     true,
		EditOldCaptionOK: true,
		MaxUploadBytes:   2147483648,
		CheckedAt:        time.Now().UTC(),
	}, nil
}

// historyLocked returns messages newer than afterID, newest-first, applying
// the truncation knob. Callers hold c.mu.
func (c *Client) historyLocked(channelID int64, afterID int, limit int) ([]telegram.Message, bool) {
	var newer []telegram.Message
	for _, m := range c.messages[channelID] {
		if m.ID > afterID {
			newer = append(newer, m)
		}
	}
	// Contract: newest-first, matching messages.getHistory.
	sort.Slice(newer, func(i, j int) bool { return newer[i].ID > newer[j].ID })
	complete := true
	if c.truncateHistory > 0 && len(newer) > c.truncateHistory {
		newer = newer[:c.truncateHistory]
		complete = false
	}
	if limit > 0 && len(newer) > limit {
		newer = newer[:limit]
	}
	return newer, complete
}

func (c *Client) History(ctx context.Context, channelID int64, afterID int, limit int) ([]telegram.Message, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out, _ := c.historyLocked(channelID, afterID, limit)
	return out, nil
}

func (c *Client) StreamHistory(ctx context.Context, channelID int64, afterID int, fn func(telegram.Message) error) (telegram.HistoryMeta, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	msgs, complete := c.historyLocked(channelID, afterID, 0)
	meta := telegram.HistoryMeta{Complete: complete}
	for _, m := range msgs {
		if meta.OldestID == 0 || m.ID < meta.OldestID {
			meta.OldestID = m.ID
		}
		if err := fn(m); err != nil {
			return telegram.HistoryMeta{}, err
		}
	}
	return meta, nil
}

// Messages returns all messages for a channel in chronological order (test helper).
func (c *Client) Messages(channelID int64) []telegram.Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]telegram.Message(nil), c.messages[channelID]...)
}

// SetNotEditable marks a message as not editable.
func (c *Client) SetNotEditable(channelID int64, messageID int, v bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, m := range c.messages[channelID] {
		if m.ID == messageID {
			c.messages[channelID][i].NotEditable = v
		}
	}
}
