package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// conn is a Qwen-Omni-Realtime WebSocket session.
type conn struct {
	ws  *websocket.Conn
	seq atomic.Int64
}

func dial(ctx context.Context, endpoint, apiKey string) (*conn, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ws, resp, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + apiKey}},
	})
	if err != nil {
		if resp != nil && resp.Body != nil {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			if text := strings.TrimSpace(string(body)); text != "" {
				return nil, fmt.Errorf("连接实时语音接口失败（%d）：%s", resp.StatusCode, text)
			}
			return nil, fmt.Errorf("连接实时语音接口失败（%d）", resp.StatusCode)
		}
		return nil, fmt.Errorf("连接实时语音接口失败：%w", err)
	}
	ws.SetReadLimit(8 << 20)
	return &conn{ws: ws}, nil
}

func (c *conn) send(ctx context.Context, event map[string]any) error {
	event["event_id"] = fmt.Sprintf("event_haobot_%d_%d", time.Now().UnixMilli(), c.seq.Add(1))
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return c.ws.Write(ctx, websocket.MessageText, data)
}

func (c *conn) close() { c.ws.Close(websocket.StatusNormalClosure, "") }

// serverEvent holds the fields used from every server event type.
type serverEvent struct {
	Type       string `json:"type"`
	Delta      string `json:"delta"`
	Text       string `json:"text"`
	Stash      string `json:"stash"`
	Transcript string `json:"transcript"`
	Name       string `json:"name"`
	CallID     string `json:"call_id"`
	Error      *struct {
		Type    string `json:"type"`
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Session struct {
		Audio struct {
			Input struct {
				Format struct {
					SampleRate int `json:"sample_rate"`
				} `json:"format"`
			} `json:"input"`
			Output struct {
				Format struct {
					SampleRate int `json:"sample_rate"`
				} `json:"format"`
			} `json:"output"`
		} `json:"audio"`
	} `json:"session"`
	Response struct {
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	} `json:"response"`
}

func (e serverEvent) errorText() string {
	if e.Error == nil {
		return "未知错误"
	}
	if e.Error.Code != "" {
		return e.Error.Code + "：" + e.Error.Message
	}
	return e.Error.Message
}

// events reads server events until the connection closes.
func (c *conn) events(ctx context.Context) (<-chan serverEvent, <-chan error) {
	out := make(chan serverEvent, 64)
	errc := make(chan error, 1)
	go func() {
		defer close(out)
		for {
			_, data, err := c.ws.Read(ctx)
			if err != nil {
				errc <- err
				return
			}
			var e serverEvent
			if json.Unmarshal(data, &e) != nil {
				continue
			}
			select {
			case out <- e:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, errc
}

// errServerGone means the connection closed: retrying on it is pointless.
var errServerGone = errors.New("实时语音接口断开了连接")

// waitFor returns the first event of the given type, failing on an error
// event.
func waitFor(ctx context.Context, events <-chan serverEvent, kind string) (serverEvent, error) {
	for {
		select {
		case <-ctx.Done():
			return serverEvent{}, errors.New("实时语音接口没有响应")
		case e, ok := <-events:
			if !ok {
				return e, errServerGone
			}
			if e.Type == "error" {
				return e, errors.New("实时语音接口报错：" + e.errorText())
			}
			if e.Type == kind {
				return e, nil
			}
		}
	}
}
