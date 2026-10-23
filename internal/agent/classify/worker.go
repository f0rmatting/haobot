package classify

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/f0rmatting/haobot/internal/agent/store"
)

// Category sources stored on messages.
const (
	SourceRule   = "rule"
	SourceLLM    = "llm"
	SourceManual = "manual"
)

const (
	batchSize    = 20
	idleInterval = time.Minute
	minBackoff   = time.Minute
	maxBackoff   = 15 * time.Minute
	// classifyTimeout bounds one model request; a short answer that takes
	// longer means the model is in trouble.
	classifyTimeout = 15 * time.Second
)

// Worker classifies new messages in the background.
type Worker struct {
	store  *store.Store
	config func(context.Context) (Config, error)
	client *Client
	log    *slog.Logger
	// notify is called after messages were classified.
	notify func()
	// event records an operational event for the log page.
	event func(level, text string)

	wake chan struct{}

	mu     sync.Mutex
	status WorkerStatus
	// modelDown is set when the last model request failed and cleared by
	// the next success; meanwhile new SMS are pushed without waiting.
	modelDown bool
}

// WorkerStatus is shown on the settings page.
type WorkerStatus struct {
	LastRun     time.Time `json:"lastRun"`
	LastError   string    `json:"lastError"`
	LastErrorAt time.Time `json:"lastErrorAt"`
	Classified  int       `json:"classified"` // since start
	ByModel     int       `json:"byModel"`    // of which the model decided
}

func NewWorker(st *store.Store, config func(context.Context) (Config, error), client *Client, log *slog.Logger, notify func(), event func(level, text string)) *Worker {
	return &Worker{store: st, config: config, client: client, log: log, notify: notify, event: event, wake: make(chan struct{}, 1)}
}

// Wake asks the worker to look for work now, e.g. after a new message or a
// configuration change. It also cancels an error backoff.
func (w *Worker) Wake() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// ModelUp reports whether the model answered the last request, so a new
// SMS is worth holding back for its category.
func (w *Worker) ModelUp() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return !w.modelDown
}

// setModelDown records whether a model request failed, and notes when the
// model is back.
func (w *Worker) setModelDown(down bool) {
	w.mu.Lock()
	was := w.modelDown
	w.modelDown = down
	w.mu.Unlock()
	if was && !down && w.event != nil {
		w.event("info", "短信分类已恢复，大模型可以正常调用")
	}
}

func (w *Worker) Status() WorkerStatus {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status
}

// Run processes the queue until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	backoff := time.Duration(0)
	for ctx.Err() == nil {
		n, err := w.batch(ctx)
		wait := idleInterval
		switch {
		case err != nil:
			backoff = min(max(backoff*2, minBackoff), maxBackoff)
			wait = backoff
		case n > 0:
			backoff = 0
			continue
		default:
			backoff = 0
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-w.wake:
			backoff = 0
		case <-timer.C:
		}
		timer.Stop()
	}
}

func (w *Worker) batch(ctx context.Context) (int, error) {
	cfg, err := w.config(ctx)
	if err != nil {
		return 0, w.fail(err)
	}
	pending, err := w.store.Unclassified(ctx, batchSize)
	if err != nil {
		return 0, w.fail(err)
	}
	done, byModel, waiting := 0, 0, false
	defer func() {
		w.mu.Lock()
		w.status.LastRun = time.Now()
		w.status.Classified += done
		w.status.ByModel += byModel
		w.mu.Unlock()
		if done > 0 && w.notify != nil {
			w.notify()
		}
	}()
	for _, m := range pending {
		d, err := w.Classify(ctx, cfg, m)
		if errors.Is(err, ErrNoModel) {
			// Not a failure: the message waits for a configured model,
			// while the ones that need none (binary) are still settled.
			waiting = true
			continue
		}
		w.logDecision(ctx, m.ID, d, err)
		if d.Call != nil {
			w.setModelDown(err != nil)
		}
		if err != nil {
			return done, w.fail(err)
		}
		if err := w.store.SetCategory(ctx, m.ID, d.Category, d.Source, time.Now()); err != nil {
			return done, w.fail(err)
		}
		done++
		if d.Source == SourceLLM {
			byModel++
		}
	}
	switch {
	case waiting:
		w.mu.Lock()
		w.status.LastError = ErrNoModel.Error()
		w.mu.Unlock()
	case done > 0:
		w.mu.Lock()
		w.status.LastError = ""
		w.mu.Unlock()
	}
	return done, nil
}

// logDecision keeps every attempt, failed ones included, for the UI.
func (w *Worker) logDecision(ctx context.Context, messageID int64, d Decision, err error) {
	entry := DecisionLog(messageID, d)
	if err != nil {
		entry.Category = ""
		if entry.Error == "" {
			entry.Error = err.Error()
		}
	}
	if err := w.store.AddClassifyLog(ctx, entry); err != nil {
		w.log.Warn("record classification", "error", err)
	}
}

// DecisionLog turns a decision into a log row.
func DecisionLog(messageID int64, d Decision) store.ClassifyLog {
	entry := store.ClassifyLog{MessageID: messageID, At: time.Now(), Source: d.Source, Category: d.Category, Rule: d.Rule}
	if c := d.Call; c != nil {
		entry.Provider, entry.Model, entry.Endpoint = c.Provider, c.Model, c.Endpoint
		entry.System, entry.Prompt, entry.Reply = c.System, c.Prompt, c.Reply
		entry.Confidence, entry.LatencyMs, entry.Error = c.Confidence, c.Latency.Milliseconds(), c.Err
	}
	return entry
}

func (w *Worker) fail(err error) error {
	w.mu.Lock()
	repeated := w.status.LastError == err.Error()
	w.status.LastError, w.status.LastErrorAt = err.Error(), time.Now()
	down := w.modelDown
	w.mu.Unlock()
	if !repeated {
		w.log.Warn("classify messages", "error", err)
		if w.event != nil {
			text := "短信分类失败：" + err.Error()
			if down {
				text += "。新短信先不等分类直接推送，大模型恢复后会自动补上分类"
			}
			w.event("warn", text)
		}
	}
	return err
}

// Decision says how a message got its category.
type Decision struct {
	Category string
	Source   string // SourceRule or SourceLLM
	Rule     string // why a rule decided; empty for the model
	Call     *Call  // the model request, when one was made
}

// ErrNoModel means classification waits for a model to be configured.
var ErrNoModel = errors.New("大模型未启用或配置不完整，新短信会保持“待分类”，配置好后自动分类")

// Classify asks the model for the category of one message; only redacted
// text ever reaches it. Binary messages carry no text to judge and get a
// fixed category. When the model fails, the returned Decision still carries
// the failed Call for the log.
func (w *Worker) Classify(ctx context.Context, cfg Config, m store.Message) (Decision, error) {
	if m.Binary {
		name := Other
		if !cfg.Has(name) {
			name = cfg.Categories[len(cfg.Categories)-1].Name
		}
		return Decision{Category: name, Source: SourceRule, Rule: "二进制数据短信没有文字内容，固定归类"}, nil
	}
	if m.Kind == store.KindMMS {
		// Only the subject arrives, often none, so the model has nothing
		// to go on; MMS that reach a personal number are nearly all
		// advertising.
		name := Marketing
		if !cfg.Has(name) {
			name = Other
		}
		if !cfg.Has(name) {
			name = cfg.Categories[len(cfg.Categories)-1].Name
		}
		return Decision{Category: name, Source: SourceRule, Rule: "彩信基本都是营销广告，固定归类"}, nil
	}
	if !cfg.Ready() {
		return Decision{}, ErrNoModel
	}
	redacted, _ := Redact(m.Body)
	ctx, cancel := context.WithTimeout(ctx, classifyTimeout)
	defer cancel()
	result, call, err := w.client.Classify(ctx, cfg, RedactSender(m.Sender), redacted)
	return Decision{Category: result.Category, Source: SourceLLM, Call: &call}, err
}
