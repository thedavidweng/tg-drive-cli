package fake

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/thedavidweng/tg-drive-cli/core/telegram"
)

// persistedState is the JSON image of a persistent fake client.
type persistedState struct {
	LoggedIn bool                         `json:"logged_in"`
	User     *telegram.User               `json:"user,omitempty"`
	Channels map[int64]*telegram.Channel  `json:"channels"`
	Messages map[int64][]telegram.Message `json:"messages"`
	NextID   int                          `json:"next_id"`
	NextChID int64                        `json:"next_ch_id"`
}

// NewPersistent creates a fake client whose state survives process restarts
// by loading from and saving to path. It lets the CLI run complete workflows
// (login, init, cp, ls, get, ...) across separate invocations without a real
// Telegram account: set TD_FAKE_TELEGRAM=1 and TD_FAKE_TELEGRAM_STATE=<path>.
// The login code is the fake default ("12345").
func NewPersistent(path string) *Client {
	c := New()
	c.statePath = path
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
}

// save writes state best-effort. Callers must hold c.mu.
func (c *Client) save() {
	if c.statePath == "" {
		return
	}
	st := persistedState{
		LoggedIn: c.loggedIn,
		User:     c.user,
		Channels: c.channels,
		Messages: c.messages,
		NextID:   c.nextID,
		NextChID: c.nextChID,
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
