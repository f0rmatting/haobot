package notify

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/f0rmatting/haobot/internal/agent/service"
	"github.com/f0rmatting/haobot/internal/agent/store"
)

const (
	// classifyWait is how long a new SMS waits for its category before it
	// is pushed without one.
	classifyWait = 20 * time.Second
	queueSize    = 200
)

// Notifier turns new messages and call events into notes and sends them to
// every enabled channel. Each channel has its own queue, so one that is
// down does not hold up the others, and retries through network trouble.
type Notifier struct {
	Config func(context.Context) (Config, error)
	Store  *store.Store
	// ClassifierReady reports whether new SMS get a category soon, worth
	// waiting for.
	ClassifierReady func(context.Context) bool
	Log             *slog.Logger
	// Note records an event in the log the UI shows.
	Note func(ctx context.Context, level, text string)

	Telegram Telegram
	Bots     Bots

	once   sync.Once
	queues map[Kind]chan Note

	mu      sync.Mutex
	failing map[Kind]bool // the last send failed; logged once until it recovers
}

func (n *Notifier) init() {
	n.once.Do(func() {
		n.queues = map[Kind]chan Note{}
		for _, k := range Kinds {
			n.queues[k] = make(chan Note, queueSize)
		}
		n.failing = map[Kind]bool{}
	})
}

// Run sends queued notes until ctx ends.
func (n *Notifier) Run(ctx context.Context) {
	n.init()
	var wg sync.WaitGroup
	for kind, queue := range n.queues {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case note := <-queue:
					n.deliver(ctx, kind, note)
				}
			}
		}()
	}
	wg.Wait()
}

// enqueue queues a note for every channel that is ready.
func (n *Notifier) enqueue(cfg Config, note Note) {
	n.init()
	for _, k := range Kinds {
		if !cfg.Ready(k) {
			continue
		}
		select {
		case n.queues[k] <- note:
		default:
			n.Log.Warn("push queue full, message dropped", "channel", k)
		}
	}
}

// Send delivers a note to one channel now, without retrying; the settings
// page's test button uses it.
func (n *Notifier) Send(ctx context.Context, kind Kind, cfg Config, note Note) error {
	if kind == KindTelegram {
		return n.Telegram.Send(ctx, cfg.Telegram, note.HTML())
	}
	return n.Bots.Send(ctx, kind, cfg.Webhook(kind), note)
}

// retryable tells how long to wait before retrying err, or false when
// retrying cannot help.
func retryable(err error, attempt int) (time.Duration, bool) {
	delays := []time.Duration{2 * time.Second, 5 * time.Second, 15 * time.Second, time.Minute, 3 * time.Minute}
	if attempt >= len(delays) {
		return 0, false
	}
	wait := delays[attempt]
	var api *apiError
	if errors.As(err, &api) {
		if api.permanent() {
			return 0, false
		}
		if api.RetryAfter > 0 {
			wait = api.RetryAfter
		}
	}
	var bot *botError
	if errors.As(err, &bot) {
		if bot.permanent {
			return 0, false
		}
		if bot.limited && wait < time.Minute {
			wait = time.Minute // the group bots count per minute
		}
	}
	return wait, true
}

// deliver sends one note, retrying transient failures for a few minutes.
func (n *Notifier) deliver(ctx context.Context, kind Kind, note Note) {
	var err error
	for attempt := 0; ; attempt++ {
		cfg, cerr := n.Config(ctx)
		if cerr != nil || !cfg.Ready(kind) {
			return // turned off meanwhile
		}
		if err = n.Send(ctx, kind, cfg, note); err == nil {
			n.setFailing(ctx, kind, nil)
			return
		}
		wait, ok := retryable(err, attempt)
		if !ok {
			break
		}
		if attempt == 0 {
			n.setFailing(ctx, kind, err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
	n.setFailing(ctx, kind, err)
}

// setFailing logs the first failure of an outage and its recovery.
func (n *Notifier) setFailing(ctx context.Context, kind Kind, err error) {
	n.init()
	n.mu.Lock()
	was := n.failing[kind]
	n.failing[kind] = err != nil
	n.mu.Unlock()
	switch {
	case err != nil && !was:
		n.note(ctx, "warn", kind.Label()+" 推送失败，会自动重试："+err.Error())
	case err == nil && was:
		n.note(ctx, "info", kind.Label()+" 推送已恢复")
	}
}

func (n *Notifier) note(ctx context.Context, level, text string) {
	if n.Note != nil {
		n.Note(ctx, level, text)
	} else {
		n.Log.Warn(text)
	}
}

// Messages pushes newly received SMS once they are classified, or after a
// short wait when classification is slow or off.
func (n *Notifier) Messages(msgs []store.Message) {
	ctx := context.Background()
	cfg, err := n.Config(ctx)
	if err != nil || !cfg.Any() || !cfg.Rules.SMS {
		return
	}
	for _, m := range msgs {
		if m.Direction == store.DirectionOut || m.Binary {
			continue
		}
		go n.pushMessage(ctx, m.ID)
	}
}

func (n *Notifier) pushMessage(ctx context.Context, id int64) {
	deadline := time.Now().Add(classifyWait)
	wait := n.ClassifierReady != nil && n.ClassifierReady(ctx)
	for {
		m, err := n.Store.GetMessage(ctx, id)
		if err != nil {
			return
		}
		if m.Category != "" || !wait || time.Now().After(deadline) {
			cfg, err := n.Config(ctx)
			if err != nil || !cfg.Rules.wantsSMS(m.Category) {
				return
			}
			n.enqueue(cfg, MessageNote(m, n.deviceName(ctx, m.DeviceID), cfg.Rules.MaskCodes))
			return
		}
		time.Sleep(time.Second)
	}
}

// Call pushes a call event when the rules ask for it.
func (n *Notifier) Call(e service.CallEvent) {
	cfg, err := n.Config(context.Background())
	if err != nil {
		return
	}
	switch e.Kind {
	case service.CallEventRinging:
		if !cfg.Rules.Ringing {
			return
		}
	case service.CallEventMissed:
		if !cfg.Rules.Missed {
			return
		}
	case service.CallEventNote:
		var s summary
		json.Unmarshal([]byte(e.Record.Summary), &s)
		if !cfg.Rules.wantsSummary(s.Category, s.Urgency) {
			return
		}
	default:
		return
	}
	n.enqueue(cfg, CallNote(e))
}

func (n *Notifier) deviceName(ctx context.Context, id string) string {
	if dev, err := n.Store.GetDevice(ctx, id); err == nil {
		return dev.Name
	}
	return ""
}
