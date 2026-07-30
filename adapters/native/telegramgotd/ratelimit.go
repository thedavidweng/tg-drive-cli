package telegramgotd

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	tgtelegram "github.com/thedavidweng/tg-drive-cli/core/telegram"
)

// RateLimiter paces outbound Telegram RPCs to reduce FLOOD_WAIT triggers and
// respect FLOOD_WAIT_X when it does occur. It is implemented as a gotd
// telegram.Middleware, so it intercepts every API call (message sends, history
// reads, file up/downloads, etc.) with a single mechanism.
//
// Design notes:
//   - Conservative base intervals for known chat-scoped mutation methods
//     (send/edit/delete) avoid the 1 msg/sec per chat limit.
//   - All other methods start with a small (or zero) base interval and adapt
//     after the first FLOOD_WAIT, then decay back toward their base.
//   - FLOOD_WAIT is handled reactively: the middleware sleeps the requested
//     duration and retries the same request, unless the wait would exceed the
//     configured maximum.
type RateLimiter struct {
	waitFlood  bool
	maxWait    time.Duration
	maxRetries int
	clock      func() time.Time

	mu      sync.Mutex
	methods map[methodKey]*methodState
}

// methodKey identifies a rate-limited stream. Methods can be global or
// per-chat: for message sends we scope by channel so unrelated channels do not
// pace each other.
type methodKey struct {
	method    string
	channelID int64
}

type methodState struct {
	mu          sync.Mutex
	base        time.Duration
	nextAllowed time.Time
	interval    time.Duration
}

// NewRateLimiter creates a rate limiter. waitFlood and maxWait mirror the CLI
// --wait/--no-wait behavior: when waitFlood is true, FLOOD_WAIT_X is slept and
// retried up to maxWait; otherwise it is returned as a FloodWaitError.
func NewRateLimiter(waitFlood bool, maxWait time.Duration) *RateLimiter {
	if maxWait <= 0 {
		maxWait = 300 * time.Second
	}
	return &RateLimiter{
		waitFlood:  waitFlood,
		maxWait:    maxWait,
		maxRetries: 10,
		clock:      time.Now,
		methods:    make(map[methodKey]*methodState),
	}
}

// Middleware returns a gotd telegram.Middleware that applies this rate limiter.
func (rl *RateLimiter) Middleware() telegram.Middleware {
	return telegram.MiddlewareFunc(func(next tg.Invoker) telegram.InvokeFunc {
		return rl.invoke(next)
	})
}

func (rl *RateLimiter) invoke(next tg.Invoker) telegram.InvokeFunc {
	return func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
		method := fmt.Sprintf("%T", input)
		key := methodKey{method: method, channelID: extractChatID(input)}
		if base, perChat := methodBaseInterval(method); perChat {
			if key.channelID == 0 {
				// Could not extract a chat scope; fall back to method-global.
				key.channelID = 0
				key.method = method
			} else {
				_ = base
			}
		} else {
			key.channelID = 0
		}

		state, base := rl.stateFor(key)
		if err := state.reserve(ctx, rl.clock(), base, rl.maxWait); err != nil {
			return err
		}

		totalWaited := time.Duration(0)
		for attempt := 0; attempt <= rl.maxRetries; attempt++ {
			err := next.Invoke(ctx, input, output)
			if err == nil {
				state.recordSuccess(base)
				return nil
			}

			wait, ok := tgerr.AsFloodWait(err)
			if !ok || wait <= 0 {
				return err
			}

			state.recordFlood(wait, rl.maxWait)
			if !rl.waitFlood || totalWaited+wait > rl.maxWait {
				return &tgtelegram.FloodWaitError{Seconds: int(wait.Seconds())}
			}

			select {
			case <-time.After(wait):
				totalWaited += wait
			case <-ctx.Done():
				return ctx.Err()
			}

			if err := state.reserve(ctx, rl.clock(), base, rl.maxWait); err != nil {
				return err
			}
		}
		return &tgtelegram.FloodWaitError{Seconds: int((rl.maxWait - totalWaited).Seconds())}
	}
}

func (rl *RateLimiter) stateFor(key methodKey) (*methodState, time.Duration) {
	base, _ := methodBaseInterval(key.method)

	rl.mu.Lock()
	defer rl.mu.Unlock()

	if s, ok := rl.methods[key]; ok {
		return s, base
	}
	s := &methodState{base: base}
	rl.methods[key] = s
	return s, base
}

func (s *methodState) reserve(ctx context.Context, now time.Time, base, maxInterval time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.interval == 0 {
		s.interval = base
	}
	if s.interval > maxInterval {
		s.interval = maxInterval
	}

	if !s.nextAllowed.IsZero() {
		for {
			d := s.nextAllowed.Sub(now)
			if d <= 0 {
				break
			}
			s.mu.Unlock()
			select {
			case <-time.After(d):
			case <-ctx.Done():
				s.mu.Lock()
				return ctx.Err()
			}
			s.mu.Lock()
			now = time.Now()
		}
	}

	s.nextAllowed = now.Add(s.interval)
	return nil
}

func (s *methodState) recordSuccess(base time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.interval <= base {
		s.interval = base
		return
	}
	// Exponential decay toward the base: halve the excess each success.
	s.interval = (s.interval + base) / 2
	if s.interval < base {
		s.interval = base
	}
}

func (s *methodState) recordFlood(wait, maxInterval time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if wait > s.interval {
		s.interval = wait
	}
	if s.interval > maxInterval {
		s.interval = maxInterval
	}
	s.nextAllowed = time.Now().Add(s.interval)
}

// methodBaseInterval returns the per-chat interval and whether this method
// should be scoped by chat. Base intervals are intentionally conservative for
// message mutations; the rest adapt on first flood.
func methodBaseInterval(method string) (time.Duration, bool) {
	switch method {
	case "*tg.MessagesSendMediaRequest",
		"*tg.MessagesSendMultiMediaRequest",
		"*tg.MessagesSendMessageRequest",
		"*tg.MessagesEditMessageRequest",
		"*tg.ChannelsDeleteMessagesRequest":
		return 1100 * time.Millisecond, true
	case "*tg.ChannelsGetMessagesRequest",
		"*tg.MessagesGetHistoryRequest":
		return 200 * time.Millisecond, true
	case "*tg.MessagesExportChatInviteRequest":
		return 5 * time.Second, true
	case "*tg.MessagesGetDialogsRequest":
		return 1 * time.Second, false
	case "*tg.AuthSendCodeRequest":
		return 60 * time.Second, false
	case "*tg.UploadGetFileRequest",
		"*tg.UploadGetCdnFileRequest",
		"*tg.UploadSaveFilePartRequest",
		"*tg.UploadSaveBigFilePartRequest",
		"*tg.UploadReuploadCdnFileRequest",
		"*tg.UploadGetCdnFileHashesRequest",
		"*tg.UploadGetFileHashesRequest":
		return 20 * time.Millisecond, false
	case "*tg.ChannelsGetChannelsRequest",
		"*tg.ChannelsGetFullChannelRequest":
		return 500 * time.Millisecond, true
	default:
		return 0, false
	}
}

// extractChatID returns the channel/chat ID for methods where rate limits are
// per conversation. Returns 0 for global methods or unsupported inputs.
func extractChatID(input bin.Encoder) int64 {
	switch req := input.(type) {
	case *tg.MessagesSendMediaRequest:
		return peerChannelID(req.Peer)
	case *tg.MessagesSendMultiMediaRequest:
		return peerChannelID(req.Peer)
	case *tg.MessagesSendMessageRequest:
		return peerChannelID(req.Peer)
	case *tg.MessagesEditMessageRequest:
		return peerChannelID(req.Peer)
	case *tg.MessagesGetHistoryRequest:
		return peerChannelID(req.Peer)
	case *tg.MessagesExportChatInviteRequest:
		return peerChannelID(req.Peer)
	case *tg.ChannelsDeleteMessagesRequest:
		return inputChannelID(req.Channel)
	case *tg.ChannelsGetMessagesRequest:
		return inputChannelID(req.Channel)
	case *tg.ChannelsGetChannelsRequest:
		if len(req.ID) == 1 {
			return inputChannelID(req.ID[0])
		}
	case *tg.ChannelsGetFullChannelRequest:
		return inputChannelID(req.Channel)
	case *tg.ChannelsExportMessageLinkRequest:
		return inputChannelID(req.Channel)
	}
	return 0
}

func peerChannelID(p tg.InputPeerClass) int64 {
	if p == nil {
		return 0
	}
	switch v := p.(type) {
	case *tg.InputPeerChannel:
		return v.ChannelID
	case *tg.InputPeerChannelFromMessage:
		return v.ChannelID
	}
	return 0
}

func inputChannelID(c tg.InputChannelClass) int64 {
	if c == nil {
		return 0
	}
	if v, ok := c.(*tg.InputChannel); ok {
		return v.ChannelID
	}
	return 0
}
