package fake

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"

	"github.com/thedavidweng/tg-drive-cli/core/telegram"
)

// persistedState is the JSON image of a persistent fake client.
type persistedState struct {
	LoggedIn      bool                         `json:"logged_in"`
	User          *telegram.User               `json:"user,omitempty"`
	Channels      map[int64]*telegram.Channel  `json:"channels"`
	Messages      map[int64][]telegram.Message `json:"messages"`
	NextID        int                          `json:"next_id"`
	NextChID      int64                        `json:"next_ch_id"`
	NextGroupedID int64                        `json:"next_grouped_id,omitempty"`
	// ADR 0018 state: linked discussion groups and forwarded thread roots.
	Discussion  map[int64]int64 `json:"discussion,omitempty"`
	ThreadRoots map[int64]int64 `json:"thread_roots,omitempty"`
}

// NewPersistent creates a fake client whose state survives process restarts
// by loading from and saving to path. It lets the CLI run complete workflows
// (login, init, cp, ls, get, ...) across separate invocations without a real
// Telegram account: set TD_FAKE_TELEGRAM=1 and TD_FAKE_TELEGRAM_STATE=<path>.
// The login code is the fake default ("12345").
//
// Test knobs read from the environment (each applies once at construction):
//   - TD_FAKE_PART_SIZE: simulated resumable-upload part size in bytes.
//   - TD_FAKE_FAIL_UPLOAD_AFTER_PARTS: fail the first resumable upload once
//     this many parts are confirmed (state stays persisted for resume); the
//     knob disables itself after firing once so a retry can succeed.
func NewPersistent(path string) *Client {
	c := New()
	c.statePath = path
	if v := os.Getenv("TD_FAKE_PART_SIZE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.partSize = n
		}
	}
	if v := os.Getenv("TD_FAKE_FAIL_UPLOAD_AFTER_PARTS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.failUploadAfterParts = n
		}
	}
	c.load()
	return c
}

// load restores state from the state file, ignoring a missing or corrupt file.
// Only called from NewPersistent, before any concurrent access.
func (c *Client) load() {
	data, err := os.ReadFile(c.statePath)
	if err != nil {
		return
	}
	var st persistedState
	if err := json.Unmarshal(data, &st); err != nil {
		return
	}
	c.loggedIn = st.LoggedIn
	c.user = st.User
	if st.Channels != nil {
		c.channels = st.Channels
	}
	if st.Messages != nil {
		c.messages = st.Messages
	}
	if st.NextID > 0 {
		c.nextID = st.NextID
	}
	if st.NextChID > 0 {
		c.nextChID = st.NextChID
	}
	if st.NextGroupedID > 0 {
		c.nextGroupedID = st.NextGroupedID
	}
	if st.Discussion != nil {
		c.discussion = st.Discussion
	}
	if st.ThreadRoots != nil {
		c.threadRoots = st.ThreadRoots
	}
}

// save writes state best-effort. Callers must hold c.mu.
func (c *Client) save() {
	if c.statePath == "" {
		return
	}
	st := persistedState{
		LoggedIn:      c.loggedIn,
		User:          c.user,
		Channels:      c.channels,
		Messages:      c.messages,
		NextID:        c.nextID,
		NextChID:      c.nextChID,
		NextGroupedID: c.nextGroupedID,
		Discussion:    c.discussion,
		ThreadRoots:   c.threadRoots,
	}
	data, err := json.Marshal(st)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(c.statePath), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(c.statePath, data, 0o600)
}
