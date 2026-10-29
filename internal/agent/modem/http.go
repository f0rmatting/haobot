package modem

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Packet data and HTTP over the module's own TCP/IP stack (Quectel TCP/IP,
// HTTP(S) and FILE AT commands). It is used to fetch MMS through the
// carrier's MMS APN without touching the host's network.

const (
	// activateTimeout bounds AT+QIACT/AT+QIDEACT; the manual allows 150 s,
	// but a carrier that takes more than this is not going to answer.
	activateTimeout = 40 * time.Second
	inputTimeout    = 10 * time.Second
	// FileChunk is how much ReadChunk asks for at a time.
	FileChunk = 4096
	// firstOwnContext leaves context 1 (internet) and 2 (often IMS for
	// VoLTE) alone.
	firstOwnContext = 3
	lastContext     = 15
)

var apnPattern = regexp.MustCompile(`^[A-Za-z0-9.-]{1,62}$`)

// OpenContext makes sure a PDP context for apn is active and returns its
// id. A context already set to apn is reused; otherwise a free one from 3
// up is configured. opened reports whether this call activated it, so the
// caller knows whether to deactivate it again.
func (m *Modem) OpenContext(ctx context.Context, apn string) (cid int, opened bool, err error) {
	if !apnPattern.MatchString(apn) {
		return 0, false, fmt.Errorf("invalid APN %q", apn)
	}
	lines, err := m.s.Command(ctx, "AT+CGDCONT?", shortTimeout)
	if err != nil {
		return 0, false, err
	}
	used := map[int]bool{}
	for _, line := range lines {
		value, ok := strings.CutPrefix(line, "+CGDCONT:")
		if !ok {
			continue
		}
		parts := splitFields(value)
		n, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}
		used[n] = true
		if cid == 0 && len(parts) >= 3 && strings.EqualFold(parts[2], apn) {
			cid = n
		}
	}
	if cid == 0 {
		for n := firstOwnContext; n <= lastContext && cid == 0; n++ {
			if !used[n] {
				cid = n
			}
		}
		if cid == 0 {
			return 0, false, errors.New("模组没有空闲的数据连接配置")
		}
		if _, err := m.s.Command(ctx, fmt.Sprintf(`AT+QICSGP=%d,1,"%s","","",0`, cid, apn), shortTimeout); err != nil {
			return 0, false, err
		}
	}
	if active, err := m.contextActive(ctx, cid); err != nil || active {
		return cid, false, err
	}
	if _, err := m.s.Command(ctx, "AT+QIACT="+strconv.Itoa(cid), activateTimeout); err != nil {
		return cid, false, err
	}
	return cid, true, nil
}

func (m *Modem) contextActive(ctx context.Context, cid int) (bool, error) {
	lines, err := m.s.Command(ctx, "AT+QIACT?", shortTimeout)
	if err != nil {
		return false, err
	}
	for _, line := range lines {
		value, ok := strings.CutPrefix(line, "+QIACT:")
		if !ok {
			continue
		}
		parts := splitFields(value)
		if len(parts) >= 2 && parts[0] == strconv.Itoa(cid) && parts[1] == "1" {
			return true, nil
		}
	}
	return false, nil
}

// CloseContext deactivates a context OpenContext activated.
func (m *Modem) CloseContext(ctx context.Context, cid int) error {
	_, err := m.s.Command(ctx, "AT+QIDEACT="+strconv.Itoa(cid), activateTimeout)
	return err
}

// HTTPRequest is sent verbatim (AT+QHTTPCFG="requestheader",1), so it can
// go through a WAP proxy: Connect is where the module opens the connection,
// Raw the request line, headers and body.
type HTTPRequest struct {
	Context int
	Connect string
	Post    bool
	Raw     []byte
	// Wait is how long the server may take to answer.
	Wait time.Duration
}

// Pending is a result the module reports later as an unsolicited line.
type Pending struct {
	prefix string
	ch     <-chan string
	cancel func()
}

// Wait returns the result's value, the text after its prefix.
func (p *Pending) Wait(ctx context.Context, timeout time.Duration) (string, error) {
	defer p.cancel()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case line := <-p.ch:
		return strings.TrimSpace(strings.TrimPrefix(line, p.prefix)), nil
	case <-timer.C:
		return "", fmt.Errorf("%s 在 %s 内没有结果: %w", strings.TrimSuffix(p.prefix, ":"), timeout, ErrTimeout)
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// Cancel stops waiting for the result.
func (p *Pending) Cancel() { p.cancel() }

func (m *Modem) expect(prefix string) *Pending {
	ch, cancel := m.s.Expect(prefix)
	return &Pending{prefix: prefix, ch: ch, cancel: cancel}
}

// StartHTTP sends req. The response status arrives through the returned
// Pending; read it with ParseHTTPResult.
func (m *Modem) StartHTTP(ctx context.Context, req HTTPRequest) (*Pending, error) {
	seconds := max(int(req.Wait/time.Second), 1)
	for _, cmd := range []string{
		fmt.Sprintf(`AT+QHTTPCFG="contextid",%d`, req.Context),
		`AT+QHTTPCFG="requestheader",1`,
		`AT+QHTTPCFG="responseheader",0`,
	} {
		if _, err := m.s.Command(ctx, cmd, shortTimeout); err != nil {
			return nil, err
		}
	}
	input := int(inputTimeout / time.Second)
	if _, err := m.s.Upload(ctx, fmt.Sprintf("AT+QHTTPURL=%d,%d", len(req.Connect), input), []byte(req.Connect), inputTimeout); err != nil {
		return nil, err
	}
	cmd, prefix := fmt.Sprintf("AT+QHTTPGET=%d,%d,%d", seconds, len(req.Raw), input), "+QHTTPGET:"
	if req.Post {
		cmd, prefix = fmt.Sprintf("AT+QHTTPPOST=%d,%d,%d", len(req.Raw), input, seconds), "+QHTTPPOST:"
	}
	p := m.expect(prefix)
	if _, err := m.s.Upload(ctx, cmd, req.Raw, inputTimeout); err != nil {
		p.Cancel()
		return nil, err
	}
	return p, nil
}

// HTTPError is a failure the module's HTTP stack reports (codes 701-730).
type HTTPError struct{ Code int }

func (e *HTTPError) Error() string {
	if text, ok := httpErrors[e.Code]; ok {
		return fmt.Sprintf("%s（%d）", text, e.Code)
	}
	return fmt.Sprintf("模组 HTTP 错误 %d", e.Code)
}

var httpErrors = map[int]string{
	702: "连接超时",
	703: "模组 HTTP 忙",
	706: "网络忙",
	707: "数据连接打开失败",
	708: "数据连接未配置",
	709: "数据连接已断开",
	710: "网络错误",
	711: "地址格式错误",
	714: "域名解析失败",
	716: "连接服务器失败",
	717: "读取数据失败",
	719: "服务器断开了连接",
	722: "读取超时",
	723: "服务器没有响应",
	724: "有来电，HTTP 被中断",
	725: "通话中，HTTP 被中断",
	727: "等待数据超时",
	728: "等待响应超时",
}

// ParseHTTPResult reads "<err>,<status>,<length>" from +QHTTPGET or
// +QHTTPPOST.
func ParseHTTPResult(value string) (status int, err error) {
	parts := splitFields(value)
	code, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, fmt.Errorf("unexpected HTTP result %q", value)
	}
	if code != 0 {
		return 0, &HTTPError{Code: code}
	}
	if len(parts) < 2 {
		return 0, fmt.Errorf("unexpected HTTP result %q", value)
	}
	if status, err = strconv.Atoi(parts[1]); err != nil {
		return 0, fmt.Errorf("unexpected HTTP result %q", value)
	}
	return status, nil
}

var filePattern = regexp.MustCompile(`^(UFS|RAM):[A-Za-z0-9_.-]{1,60}$`)

// SaveHTTPResponse stores the body of the last response in the module's
// file system. Its outcome (+QHTTPREADFILE: <err>) arrives through Pending.
func (m *Modem) SaveHTTPResponse(ctx context.Context, file string, wait time.Duration) (*Pending, error) {
	if !filePattern.MatchString(file) {
		return nil, fmt.Errorf("invalid file name %q", file)
	}
	p := m.expect("+QHTTPREADFILE:")
	cmd := fmt.Sprintf(`AT+QHTTPREADFILE="%s",%d`, file, max(int(wait/time.Second), 1))
	if _, err := m.s.Command(ctx, cmd, shortTimeout); err != nil {
		p.Cancel()
		return nil, err
	}
	return p, nil
}

// OpenFile opens a file in the module's file system for reading.
func (m *Modem) OpenFile(ctx context.Context, file string) (int, error) {
	if !filePattern.MatchString(file) {
		return 0, fmt.Errorf("invalid file name %q", file)
	}
	lines, err := m.s.Command(ctx, fmt.Sprintf(`AT+QFOPEN="%s",2`, file), shortTimeout)
	if err != nil {
		return 0, err
	}
	value, _ := field(lines, "+QFOPEN:")
	handle, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("AT+QFOPEN: unexpected response %q", lines)
	}
	return handle, nil
}

// ReadChunk reads the next FileChunk bytes of an open file; a shorter
// block means the end of the file.
func (m *Modem) ReadChunk(ctx context.Context, handle int) ([]byte, error) {
	return m.s.ReadData(ctx, fmt.Sprintf("AT+QFREAD=%d,%d", handle, FileChunk), shortTimeout)
}

// CloseFile closes a file OpenFile opened.
func (m *Modem) CloseFile(ctx context.Context, handle int) error {
	_, err := m.s.Command(ctx, "AT+QFCLOSE="+strconv.Itoa(handle), shortTimeout)
	return err
}

// DeleteFile removes a file from the module's file system; a missing file
// is not an error.
func (m *Modem) DeleteFile(ctx context.Context, file string) error {
	if !filePattern.MatchString(file) {
		return fmt.Errorf("invalid file name %q", file)
	}
	_, err := m.s.Command(ctx, fmt.Sprintf(`AT+QFDEL="%s"`, file), shortTimeout)
	var cmdErr *CommandError
	if errors.As(err, &cmdErr) {
		return nil
	}
	return err
}
