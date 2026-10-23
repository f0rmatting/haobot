package service

import "sync"

// Topics published on the Hub.
const (
	TopicStatus   = "status"
	TopicMessages = "messages"
	TopicEvents   = "events"
)

// Hub fans change notifications out to subscribers (the web UI's event
// stream). Slow subscribers miss notifications instead of blocking.
type Hub struct {
	mu   sync.Mutex
	subs map[chan string]struct{}
}

func NewHub() *Hub { return &Hub{subs: make(map[chan string]struct{})} }

// Subscribe returns a channel of topics and a function that ends the
// subscription.
func (h *Hub) Subscribe() (<-chan string, func()) {
	ch := make(chan string, 16)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}
}

// Publish notifies every subscriber that topic changed.
func (h *Hub) Publish(topic string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- topic:
		default:
		}
	}
}
