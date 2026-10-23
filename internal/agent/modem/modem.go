package modem

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	shortTimeout = 3 * time.Second
	// listTimeout covers AT+CMGL on a full 255-message store, which the
	// EC20 lists in a few seconds. Longer would hide a hanging modem, and
	// a call ringing meanwhile, behind the wait.
	listTimeout = 12 * time.Second
	pinTimeout  = 10 * time.Second
)

// SIM states as reported by AT+CPIN?.
const (
	SIMReady  = "READY"
	SIMPIN    = "SIM PIN"
	SIMPUK    = "SIM PUK"
	SIMAbsent = "NOT INSERTED"
)

// ErrNoAnswer means the port is open but the module does not respond to AT,
// the usual sign of a hung module.
var ErrNoAnswer = errors.New("模组不响应 AT 指令")

// Modem wraps a Session with typed helpers.
type Modem struct {
	s *Session
}

func New(s *Session) *Modem { return &Modem{s: s} }

// Ping sends a bare AT and reports how long the answer took.
func (m *Modem) Ping(ctx context.Context, timeout time.Duration) (time.Duration, error) {
	start := time.Now()
	_, err := m.s.Command(ctx, "AT", timeout)
	return time.Since(start), err
}

// Init waits for the AT port to answer and sets a predictable dialogue:
// no echo and verbose error results.
func (m *Modem) Init(ctx context.Context) error {
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		if _, err = m.s.Command(ctx, "AT", shortTimeout); err == nil {
			break
		}
		if errors.Is(err, ErrClosed) || ctx.Err() != nil {
			return err
		}
	}
	if err != nil {
		return fmt.Errorf("%w（%w）", ErrNoAnswer, err)
	}
	for _, cmd := range []string{"ATE0", "AT+CMEE=2"} {
		if _, err := m.s.Command(ctx, cmd, shortTimeout); err != nil {
			return err
		}
	}
	return nil
}

// SIMState returns READY, SIM PIN, SIM PUK, NOT INSERTED, ...
func (m *Modem) SIMState(ctx context.Context) (string, error) {
	lines, err := m.s.Command(ctx, "AT+CPIN?", shortTimeout)
	if err != nil {
		var cmdErr *CommandError
		// +CME ERROR: 10 (or its verbose text) means no SIM.
		if errors.As(err, &cmdErr) && (strings.HasSuffix(cmdErr.Result, " 10") || strings.Contains(cmdErr.Result, "not inserted")) {
			return SIMAbsent, nil
		}
		return "", err
	}
	value, ok := field(lines, "+CPIN:")
	if !ok {
		return "", errors.New("AT+CPIN?: missing +CPIN line")
	}
	return value, nil
}

// PINRetries returns the remaining PIN and PUK attempts (Quectel AT+QPINC).
func (m *Modem) PINRetries(ctx context.Context) (pin, puk int, err error) {
	lines, err := m.s.Command(ctx, `AT+QPINC="SC"`, shortTimeout)
	if err != nil {
		return 0, 0, err
	}
	value, ok := field(lines, "+QPINC:")
	parts := splitFields(value)
	if !ok || len(parts) < 3 {
		return 0, 0, errors.New("AT+QPINC: unexpected response")
	}
	if pin, err = strconv.Atoi(parts[1]); err != nil {
		return 0, 0, fmt.Errorf("AT+QPINC: %w", err)
	}
	if puk, err = strconv.Atoi(parts[2]); err != nil {
		return 0, 0, fmt.Errorf("AT+QPINC: %w", err)
	}
	return pin, puk, nil
}

// EnterPIN submits the SIM PIN. A wrong PIN consumes one attempt.
func (m *Modem) EnterPIN(ctx context.Context, pin string) error {
	for _, r := range pin {
		if r < '0' || r > '9' {
			return errors.New("SIM PIN must contain digits only")
		}
	}
	if len(pin) < 4 || len(pin) > 8 {
		return errors.New("SIM PIN must be 4-8 digits")
	}
	_, err := m.s.Command(ctx, `AT+CPIN="`+pin+`"`, pinTimeout)
	return err
}

// ConfigureSMS selects PDU mode, the message store, and "store, then notify
// with +CMTI" delivery so every message lands in storage before we read it.
func (m *Modem) ConfigureSMS(ctx context.Context, storage string) error {
	commands := []string{
		"AT+CMGF=0",
		fmt.Sprintf(`AT+CPMS="%s","%s","%s"`, storage, storage, storage),
		"AT+CNMI=2,1,0,0,0",
	}
	for _, cmd := range commands {
		if _, err := m.s.Command(ctx, cmd, shortTimeout); err != nil {
			return err
		}
	}
	return nil
}

// SelectReadStorage points listing, reading and deleting (CPMS mem1) at
// storage. Where new messages are received stays unchanged.
func (m *Modem) SelectReadStorage(ctx context.Context, storage string) error {
	_, err := m.s.Command(ctx, fmt.Sprintf(`AT+CPMS="%s"`, storage), shortTimeout)
	return err
}

// StoredPDU is one entry of AT+CMGL in PDU mode.
type StoredPDU struct {
	Index int
	// Stat: 0 received unread, 1 received read, 2 stored unsent, 3 stored sent.
	Stat int
	PDU  string
}

// ListSMS returns every message in the selected store. Reading marks unread
// messages as read on the modem.
func (m *Modem) ListSMS(ctx context.Context) ([]StoredPDU, error) {
	lines, err := m.s.Command(ctx, "AT+CMGL=4", listTimeout)
	if err != nil {
		return nil, err
	}
	var out []StoredPDU
	for i := 0; i < len(lines); i++ {
		header, ok := strings.CutPrefix(lines[i], "+CMGL:")
		if !ok {
			continue
		}
		parts := splitFields(header)
		if len(parts) < 2 || i+1 >= len(lines) {
			return out, fmt.Errorf("AT+CMGL: malformed entry %q", lines[i])
		}
		index, err1 := strconv.Atoi(parts[0])
		stat, err2 := strconv.Atoi(parts[1])
		if err1 != nil || err2 != nil {
			return out, fmt.Errorf("AT+CMGL: malformed entry %q", lines[i])
		}
		i++
		out = append(out, StoredPDU{Index: index, Stat: stat, PDU: strings.TrimSpace(lines[i])})
	}
	return out, nil
}

// DeleteSMS removes the message at index from the selected store.
func (m *Modem) DeleteSMS(ctx context.Context, index int) error {
	_, err := m.s.Command(ctx, "AT+CMGD="+strconv.Itoa(index), shortTimeout)
	return err
}

// Status is a best-effort snapshot; fields the modem did not answer keep
// their zero value (or -1 for counters).
type Status struct {
	Model        string
	Revision     string
	SIM          string
	PINRetries   int
	PUKRetries   int
	Operator     string
	AccessTech   string
	Band         string
	Registration string
	Registered   bool
	CSQ          int
	SignalDBM    int
	StorageUsed  int
	StorageTotal int

	Functionality int // AT+CFUN level, -1 unknown
	PINLock       int // 1 the SIM asks for a PIN at power-up, 0 not, -1 unknown
	Attached      int // packet domain attached: 1, 0, -1 unknown
	APN           string
	IP            string
	ScanMode      int // Quectel nwscanmode, -1 unknown
	Cell          ServingCell

	UpdatedAt time.Time
}

// ReadStatus collects radio, SIM and storage information. Individual query
// failures are tolerated; only a dead connection is returned as an error.
func (m *Modem) ReadStatus(ctx context.Context) (Status, error) {
	st := Status{PINRetries: -1, PUKRetries: -1, CSQ: 99, StorageUsed: -1, StorageTotal: -1,
		Functionality: -1, PINLock: -1, Attached: -1, ScanMode: -1}
	query := func(cmd string) []string { return query(ctx, m, cmd) }

	if lines := query("ATI"); len(lines) >= 2 {
		st.Model = strings.TrimSpace(lines[0] + " " + lines[1])
		for _, line := range lines[2:] {
			if rev, ok := strings.CutPrefix(line, "Revision:"); ok {
				st.Revision = strings.TrimSpace(rev)
			}
		}
	}
	sim, err := m.SIMState(ctx)
	if errors.Is(err, ErrClosed) {
		return st, err
	}
	st.SIM = sim
	if pin, puk, err := m.PINRetries(ctx); err == nil {
		st.PINRetries, st.PUKRetries = pin, puk
	}
	if value, ok := field(query("AT+CSQ"), "+CSQ:"); ok {
		if parts := splitFields(value); len(parts) > 0 {
			if csq, err := strconv.Atoi(parts[0]); err == nil {
				st.CSQ = csq
				if csq != 99 {
					st.SignalDBM = -113 + 2*csq
				}
			}
		}
	}
	if value, ok := field(query("AT+COPS?"), "+COPS:"); ok {
		if parts := splitFields(value); len(parts) >= 3 {
			st.Operator = parts[2]
		}
	}
	if value, ok := field(query("AT+QNWINFO"), "+QNWINFO:"); ok {
		if parts := splitFields(value); len(parts) >= 3 {
			st.AccessTech, st.Band = parts[0], parts[2]
		}
	}
	st.Registration = "未注册"
	for _, cmd := range []string{"AT+CEREG?", "AT+CREG?"} {
		name := commandName(cmd) + ":"
		value, ok := field(query(cmd), name)
		parts := splitFields(value)
		if !ok || len(parts) < 2 {
			continue
		}
		switch parts[1] {
		case "1":
			st.Registration, st.Registered = "已注册（本地）", true
		case "5":
			st.Registration, st.Registered = "已注册（漫游）", true
		case "2":
			st.Registration = "搜网中"
		case "3":
			st.Registration = "注册被拒绝"
		}
		if st.Registered {
			break
		}
	}
	if value, ok := field(query("AT+CPMS?"), "+CPMS:"); ok {
		if parts := splitFields(value); len(parts) >= 3 {
			st.StorageUsed, _ = strconv.Atoi(parts[1])
			st.StorageTotal, _ = strconv.Atoi(parts[2])
		}
	}
	if value, ok := field(query("AT+CFUN?"), "+CFUN:"); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			st.Functionality = n
		}
	}
	if st.SIM == SIMReady {
		if locked, err := m.PINLock(ctx); err == nil {
			st.PINLock = 0
			if locked {
				st.PINLock = 1
			}
		}
	}
	if value, ok := field(query("AT+CGATT?"), "+CGATT:"); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			st.Attached = n
		}
	}
	if value, ok := field(query("AT+CGDCONT?"), "+CGDCONT:"); ok {
		if parts := splitFields(value); len(parts) >= 3 {
			st.APN = parts[2]
		}
	}
	if value, ok := field(query("AT+CGPADDR=1"), "+CGPADDR:"); ok {
		if parts := splitFields(value); len(parts) >= 2 && parts[1] != "0.0.0.0" {
			st.IP = parts[1]
		}
	}
	if value, ok := field(query(`AT+QCFG="nwscanmode"`), "+QCFG:"); ok {
		if parts := splitFields(value); len(parts) >= 2 {
			if n, err := strconv.Atoi(parts[1]); err == nil {
				st.ScanMode = n
			}
		}
	}
	st.Cell = parseServingCell(query(`AT+QENG="servingcell"`))
	st.UpdatedAt = time.Now()
	return st, nil
}

// field returns the text after prefix on the first matching line.
func field(lines []string, prefix string) (string, bool) {
	for _, line := range lines {
		if value, ok := strings.CutPrefix(line, prefix); ok {
			return strings.TrimSpace(value), true
		}
	}
	return "", false
}

// splitFields splits a comma-separated response, honouring quotes and
// stripping them from the values.
func splitFields(value string) []string {
	var out []string
	var current strings.Builder
	quoted := false
	for _, r := range value {
		switch {
		case r == '"':
			quoted = !quoted
		case r == ',' && !quoted:
			out = append(out, strings.TrimSpace(current.String()))
			current.Reset()
		default:
			current.WriteRune(r)
		}
	}
	return append(out, strings.TrimSpace(current.String()))
}
