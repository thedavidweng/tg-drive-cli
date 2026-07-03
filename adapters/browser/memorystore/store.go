package memorystore

import (
	"context"
	"sync"

	"github.com/thedavidweng/tg-drive-cli/core/model"
	"github.com/thedavidweng/tg-drive-cli/core/ports"
)

// Store is an in-memory ports.Store for browser/WASM contract tests.
type Store struct {
	mu    sync.Mutex
	slugs map[model.ChannelID]map[string]string
}

var _ ports.Store = (*Store)(nil)

// New creates an empty in-memory store.
func New() *Store {
	return &Store{slugs: make(map[model.ChannelID]map[string]string)}
}

// LoadSlugMap returns slug mappings for a channel.
func (s *Store) LoadSlugMap(ctx context.Context, channelID model.ChannelID) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	src := s.slugs[channelID]
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out, nil
}

// PutSlug seeds slug data for tests.
func (s *Store) PutSlug(channelID model.ChannelID, parentSegmentKey, slug string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.slugs[channelID] == nil {
		s.slugs[channelID] = make(map[string]string)
	}
	s.slugs[channelID][parentSegmentKey] = slug
}
