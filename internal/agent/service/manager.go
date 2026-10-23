package service

import (
	"context"
	"log/slog"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/f0rmatting/haobot/internal/agent/store"
	"github.com/f0rmatting/haobot/internal/agent/transport"
)

const (
	discoverInterval = 5 * time.Second
	// goneAfter failed reconnects end a discovered device's service; a
	// rebooting module re-enumerates within that time.
	goneAfter = 6
	// adoptWait is how long startup waits for modems to identify themselves
	// before deciding who owns legacy messages.
	adoptWait = 30 * time.Second
)

// Manager runs one Service per modem: either a fixed list of ports, or every
// recognised modem found by discovery, started and dropped as they come and go.
type Manager struct {
	base  Options
	fixed []transport.Config
	kind  string // transport kind used for discovery
	store *store.Store
	log   *slog.Logger
	hub   *Hub

	mu        sync.RWMutex
	services  map[string]*Service           // by endpoint key
	selfTests map[string]chan store.Message // by token
	lastErr   string
}

// NewManager prepares a manager. With fixed ports it never discovers.
func NewManager(base Options, fixed []transport.Config, kind string, st *store.Store, log *slog.Logger) *Manager {
	if base.Hub == nil {
		base.Hub = NewHub()
	}
	if base.Budget == nil {
		base.Budget = NewBudget()
	}
	m := &Manager{base: base, fixed: fixed, kind: kind, store: st, log: log, hub: base.Hub,
		services: make(map[string]*Service), selfTests: make(map[string]chan store.Message)}
	// Self-test messages are taken out before anyone else sees them.
	next := base.OnMessages
	m.base.OnMessages = func(msgs []store.Message) {
		if rest := m.takeSelfTests(msgs); len(rest) > 0 && next != nil {
			next(rest)
		}
	}
	return m
}

// Hub delivers change notifications to the web UI.
func (m *Manager) Hub() *Hub { return m.hub }

// Run starts the services and keeps discovery going until ctx ends.
func (m *Manager) Run(ctx context.Context) {
	if err := m.store.FailStaleSending(ctx); err != nil {
		m.log.Warn("reset unfinished sends", "error", err)
	}
	if len(m.fixed) > 0 {
		for _, cfg := range m.fixed {
			m.start(ctx, cfg, 0)
		}
		go m.adoptLegacy(ctx)
		<-ctx.Done()
		return
	}
	ticker := time.NewTicker(discoverInterval)
	defer ticker.Stop()
	for first := true; ; first = false {
		m.discover(ctx)
		if first {
			go m.adoptLegacy(ctx)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (m *Manager) discover(ctx context.Context) {
	found, err := transport.Discover(m.kind)
	if err != nil {
		if err.Error() != m.lastErr {
			m.lastErr = err.Error()
			m.Note(ctx, "warn", "查找模组失败："+err.Error())
		}
		return
	}
	m.lastErr = ""
	for _, cfg := range found {
		m.mu.RLock()
		_, running := m.services[cfg.Key()]
		m.mu.RUnlock()
		if !running {
			m.start(ctx, cfg, goneAfter)
		}
	}
}

func (m *Manager) start(ctx context.Context, cfg transport.Config, giveUp int) {
	opts := m.base
	opts.Transport, opts.GiveUpAfter = cfg, giveUp
	svc := New(opts, m.store, m.log)
	m.mu.Lock()
	m.services[cfg.Key()] = svc
	m.mu.Unlock()
	m.log.Info("modem found", "endpoint", cfg.Describe(), "model", cfg.Model)
	go func() {
		svc.Run(ctx)
		m.mu.Lock()
		delete(m.services, cfg.Key())
		m.mu.Unlock()
		if ctx.Err() == nil {
			svc.note(ctx, "warn", "模组已断开（"+cfg.Describe()+"）")
		}
		m.hub.Publish(TopicStatus)
	}()
	m.hub.Publish(TopicStatus)
}

// adoptLegacy attributes messages stored before devices were tracked to the
// modem, but only when the agent starts with exactly one: with several it
// cannot know which one they came from, and the UI asks instead.
func (m *Manager) adoptLegacy(ctx context.Context) {
	orphans, err := m.store.Orphans(ctx)
	if err != nil || orphans == 0 {
		return
	}
	deadline := time.Now().Add(adoptWait)
	for time.Now().Before(deadline) {
		services := m.Services()
		identified := 0
		for _, s := range services {
			if s.DeviceID() != "" {
				identified++
			}
		}
		if len(services) > 0 && identified == len(services) {
			if len(services) == 1 {
				id := services[0].DeviceID()
				if n, err := m.store.AdoptOrphans(ctx, id); err == nil && n > 0 {
					services[0].note(ctx, "info", "已将升级前的 "+strconv.Itoa(n)+" 条短信归属到这台设备")
					m.hub.Publish(TopicMessages)
				}
			}
			return
		}
		if !sleep(ctx, time.Second) {
			return
		}
	}
}

// Services returns the running services ordered by when their device was
// first seen.
func (m *Manager) Services() []*Service {
	m.mu.RLock()
	out := make([]*Service, 0, len(m.services))
	for _, s := range m.services {
		out = append(out, s)
	}
	m.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].Snapshot().Device, out[j].Snapshot().Device
		if !a.FirstSeen.Equal(b.FirstSeen) {
			return a.FirstSeen.Before(b.FirstSeen)
		}
		return out[i].Endpoint() < out[j].Endpoint()
	})
	return out
}

// Device returns the service driving a device, or nil when it is not
// connected.
func (m *Manager) Device(id string) *Service {
	for _, s := range m.Services() {
		if s.DeviceID() == id {
			return s
		}
	}
	return nil
}

// Note records an event that belongs to no particular device.
func (m *Manager) Note(ctx context.Context, level, text string) {
	m.log.Info(text, "level", level)
	if err := m.store.AddEvent(context.WithoutCancel(ctx), level, text); err != nil {
		m.log.Warn("record event", "error", err)
	}
	m.hub.Publish(TopicEvents)
}
