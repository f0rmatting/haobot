// Package modem speaks the 3GPP TS 27.007/27.005 AT command set to a Quectel
// EC20-class module over an already-open byte stream.
package modem

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ErrClosed reports that the connection to the modem is gone.
var ErrClosed = errors.New("modem connection closed")

// CommandError is a final error result (ERROR, +CME ERROR, +CMS ERROR) for a
// command. The connection itself is still usable.
type CommandError struct {
	Command string
	Result  string
}

func (e *CommandError) Error() string {
	return fmt.Sprintf("%s: %s", e.Command, e.Result)
}

// ErrTimeout matches every error for a command the modem did not finish in
// time; a run of them means it is hanging.
var ErrTimeout = errors.New("modem did not respond")

// TimeoutError is a command that got no final result in time.
type TimeoutError struct {
	Command string
	Timeout time.Duration
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("%s 在 %s 内没有响应", e.Command, e.Timeout)
}

func (e *TimeoutError) Is(target error) bool { return target == ErrTimeout }

// resyncTimeout bounds the wait for the modem to answer the probe sent
// after a command timed out, or the command's own timeout if shorter.
const resyncTimeout = 3 * time.Second

// unsolicitedPrefixes are result codes the modem may emit at any time. A line
// with one of these prefixes is a response only while a command of the same
// name is running (e.g. "+CPIN: READY" answering AT+CPIN?).
var unsolicitedPrefixes = []string{
	"+CMTI:", "+CMT:", "+CDSI:", "+CDS:", "+CBM:",
	"RING", "+CRING:", "+CLIP:", "NO CARRIER", "BUSY", "NO ANSWER", "NO DIALTONE",
	"+CREG:", "+CEREG:", "+CGREG:", "+CPIN:", "+QIND:", "+QUSIM:",
	"+CUSD:", "+CFUN:", "RDY", "POWERED DOWN",
}

// Session serialises commands on one AT port and routes unsolicited result
// codes to URCs().
type Session struct {
	port   io.ReadWriteCloser
	lines  chan string
	urcs   chan string
	done   chan struct{}
	err    error        // set before done is closed
	busy   atomic.Bool  // a command is waiting for its final result
	active atomic.Value // extended name of that command, "" for basic ones
	cmdMu  sync.Mutex
	close  sync.Once
	// stale is set when a command was given up on: its late reply may still
	// come and must not be taken for the next command's.
	stale atomic.Bool

	// wantPrompt is set while AT+CMGS waits for the "> " input prompt,
	// which arrives without a line terminator.
	wantPrompt atomic.Bool
	prompt     chan struct{}
}

// NewSession starts reading from port. The caller owns port's lifetime only
// through Session.Close.
func NewSession(port io.ReadWriteCloser) *Session {
	s := &Session{
		port:   port,
		lines:  make(chan string, 256),
		urcs:   make(chan string, 64),
		done:   make(chan struct{}),
		prompt: make(chan struct{}, 1),
	}
	s.active.Store("")
	go s.readLoop()
	return s
}

// URCs delivers unsolicited result codes such as "+CMTI: "ME",3".
func (s *Session) URCs() <-chan string { return s.urcs }

// Done is closed when the connection fails or is closed.
func (s *Session) Done() <-chan struct{} { return s.done }

// Err returns why the session ended. It is valid after Done is closed.
func (s *Session) Err() error {
	select {
	case <-s.done:
		return s.err
	default:
		return nil
	}
}

// Stale reports that a command timed out and the modem has not answered
// since.
func (s *Session) Stale() bool { return s.stale.Load() }

// Close releases the underlying port.
func (s *Session) Close() error {
	var err error
	s.close.Do(func() { err = s.port.Close() })
	return err
}

func (s *Session) readLoop() {
	defer close(s.done)
	reader := bufio.NewReaderSize(s.port, 4096)
	var line []byte
	for {
		b, err := reader.ReadByte()
		if err != nil {
			s.err = fmt.Errorf("%w: %v", ErrClosed, err)
			return
		}
		if b == '\r' || b == '\n' {
			if len(line) > 0 {
				s.dispatch(string(line))
				line = line[:0]
			}
			continue
		}
		line = append(line, b)
		if len(line) == 2 && line[0] == '>' && line[1] == ' ' && s.wantPrompt.Load() {
			line = line[:0]
			select {
			case s.prompt <- struct{}{}:
			default:
			}
		}
	}
}

func (s *Session) dispatch(line string) {
	if !s.busy.Load() || isUnsolicited(line, s.active.Load().(string)) {
		select {
		case s.urcs <- line:
		default: // nobody is draining URCs; dropping beats blocking the reader
		}
		return
	}
	select {
	case s.lines <- line:
	default:
	}
}

func isUnsolicited(line, active string) bool {
	for _, prefix := range unsolicitedPrefixes {
		if strings.HasPrefix(line, prefix) {
			return prefix != active+":"
		}
	}
	return false
}

// Command sends one AT command and returns its intermediate response lines.
// A final error result is returned as *CommandError.
func (s *Session) Command(ctx context.Context, cmd string, timeout time.Duration) ([]string, error) {
	s.cmdMu.Lock()
	defer s.cmdMu.Unlock()
	label := redact(cmd)

	for drained := false; !drained; {
		select {
		case <-s.lines:
		default:
			drained = true
		}
	}
	if err := s.resync(ctx, timeout); err != nil {
		return nil, err
	}
	s.active.Store(commandName(cmd))
	s.busy.Store(true)
	defer s.busy.Store(false)

	if _, err := io.WriteString(s.port, cmd+"\r"); err != nil {
		return nil, fmt.Errorf("%w: write %s: %v", ErrClosed, label, err)
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	return s.await(ctx, cmd, label, timeout, timer)
}

// Submit runs a command that asks for input, such as AT+CMGS: it sends cmd,
// waits for the "> " prompt, then sends payload terminated by Ctrl-Z and
// waits for the final result.
func (s *Session) Submit(ctx context.Context, cmd, payload string, timeout time.Duration) ([]string, error) {
	s.cmdMu.Lock()
	defer s.cmdMu.Unlock()
	label := redact(cmd)
	for drained := false; !drained; {
		select {
		case <-s.lines:
		case <-s.prompt:
		default:
			drained = true
		}
	}
	if err := s.resync(ctx, timeout); err != nil {
		return nil, err
	}
	s.active.Store(commandName(cmd))
	s.busy.Store(true)
	defer s.busy.Store(false)
	s.wantPrompt.Store(true)
	defer s.wantPrompt.Store(false)

	if _, err := io.WriteString(s.port, cmd+"\r"); err != nil {
		return nil, fmt.Errorf("%w: write %s: %v", ErrClosed, label, err)
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	// ESC leaves input mode without sending, should we give up waiting.
	abort := func() { io.WriteString(s.port, "\x1b") }
	for waiting := true; waiting; {
		select {
		case <-s.prompt:
			waiting = false
		case line := <-s.lines:
			if isFinalError(line) {
				return nil, &CommandError{Command: label, Result: line}
			}
		case <-s.done:
			return nil, s.err
		case <-timer.C:
			abort()
			s.stale.Store(true)
			return nil, fmt.Errorf("%s 在 %s 内没有给出输入提示: %w", label, timeout, ErrTimeout)
		case <-ctx.Done():
			abort()
			return nil, ctx.Err()
		}
	}
	s.wantPrompt.Store(false)
	if _, err := io.WriteString(s.port, payload+"\x1a"); err != nil {
		return nil, fmt.Errorf("%w: write %s payload: %v", ErrClosed, label, err)
	}
	return s.await(ctx, cmd, label, timeout, timer)
}

// await collects response lines until the final result of cmd.
func (s *Session) await(ctx context.Context, cmd, label string, timeout time.Duration, timer *time.Timer) ([]string, error) {
	var out []string
	for {
		select {
		case line := <-s.lines:
			switch {
			case line == cmd: // echo, if ATE0 has not taken effect yet
			case line == "OK":
				return out, nil
			case isFinalError(line):
				return out, &CommandError{Command: label, Result: line}
			default:
				out = append(out, line)
			}
		case <-s.done:
			return nil, s.err
		case <-timer.C:
			s.stale.Store(true)
			return out, &TimeoutError{Command: label, Timeout: timeout}
		case <-ctx.Done():
			s.stale.Store(true)
			return out, ctx.Err()
		}
	}
}

// resync lines replies up with commands again after one was given up on.
// It asks AT+CMEE?, whose "+CMEE:" line no late reply carries, and drops
// everything up to that answer's OK.
func (s *Session) resync(ctx context.Context, timeout time.Duration) error {
	if !s.stale.Load() {
		return nil
	}
	timeout = min(timeout, resyncTimeout)
	s.active.Store("+CMEE")
	s.busy.Store(true)
	defer s.busy.Store(false)
	if _, err := io.WriteString(s.port, "AT+CMEE?\r"); err != nil {
		return fmt.Errorf("%w: write AT+CMEE?: %v", ErrClosed, err)
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	answered := false
	for {
		select {
		case line := <-s.lines:
			switch {
			case strings.HasPrefix(line, "+CMEE:"):
				answered = true
			case answered && line == "OK":
				s.stale.Store(false)
				return nil
			}
		case <-s.done:
			return s.err
		case <-timer.C:
			return &TimeoutError{Command: "AT+CMEE?", Timeout: timeout}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func isFinalError(line string) bool {
	return line == "ERROR" ||
		strings.HasPrefix(line, "+CME ERROR:") ||
		strings.HasPrefix(line, "+CMS ERROR:")
}

// commandName extracts the extended command name, e.g. "+CPMS" from
// `AT+CPMS="ME"`. Basic commands such as ATI have no name.
func commandName(cmd string) string {
	upper := strings.ToUpper(cmd)
	if !strings.HasPrefix(upper, "AT+") {
		return ""
	}
	name := upper[2:]
	if i := strings.IndexAny(name, "=?"); i >= 0 {
		name = name[:i]
	}
	return name
}

// redact hides secrets in commands that end up in errors and logs.
func redact(cmd string) string {
	upper := strings.ToUpper(cmd)
	for _, secret := range []string{"AT+CPIN=", "AT+CLCK=", "AT+CPWD="} {
		if strings.HasPrefix(upper, secret) {
			return cmd[:len(secret)] + "***"
		}
	}
	return cmd
}

// Redact hides secrets in a command, for logging commands typed by users.
func Redact(cmd string) string { return redact(cmd) }
