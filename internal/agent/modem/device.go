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
	cfunTimeout = 20 * time.Second
	// ConsoleTimeout is the default for commands typed in the AT console.
	ConsoleTimeout = 10 * time.Second
)

// Functionality levels of AT+CFUN.
const (
	FunMinimum  = 0 // radio and SIM off
	FunFull     = 1
	FunAirplane = 4 // radio off, SIM still readable
)

// Network scan modes of Quectel AT+QCFG="nwscanmode".
var ScanModes = map[int]string{0: "自动", 1: "仅 2G（GSM）", 2: "仅 3G（WCDMA）", 3: "仅 4G（LTE）"}

// Identity is information that does not change while a SIM is inserted.
type Identity struct {
	IMEI   string
	IMSI   string
	ICCID  string
	Number string // often not stored on the SIM
	SMSC   string
}

// ReadIdentity queries IMEI, IMSI, ICCID, own number and SMS centre. Fields
// the modem cannot answer stay empty.
func (m *Modem) ReadIdentity(ctx context.Context) (Identity, error) {
	var id Identity
	first := func(cmd string) string {
		lines, err := m.s.Command(ctx, cmd, shortTimeout)
		if err != nil || len(lines) == 0 {
			return ""
		}
		return strings.TrimSpace(lines[0])
	}
	id.IMEI = strings.TrimPrefix(first("AT+CGSN"), "+CGSN: ")
	if err := m.s.Err(); err != nil {
		return id, err
	}
	id.IMSI = first("AT+CIMI")
	if value, ok := field(query(ctx, m, "AT+QCCID"), "+QCCID:"); ok {
		id.ICCID = value
	} else if value, ok := field(query(ctx, m, "AT+CCID"), "+CCID:"); ok {
		id.ICCID = strings.Trim(value, `"`)
	}
	if value, ok := field(query(ctx, m, "AT+CNUM"), "+CNUM:"); ok {
		if parts := splitFields(value); len(parts) >= 2 {
			id.Number = parts[1]
		}
	}
	if value, ok := field(query(ctx, m, "AT+CSCA?"), "+CSCA:"); ok {
		if parts := splitFields(value); len(parts) >= 1 {
			id.SMSC = parts[0]
		}
	}
	return id, nil
}

func query(ctx context.Context, m *Modem, cmd string) []string {
	lines, err := m.s.Command(ctx, cmd, shortTimeout)
	if err != nil {
		return nil
	}
	return lines
}

// ServingCell is the radio measurement of the current cell (Quectel
// AT+QENG="servingcell"). Measurements are only parsed for LTE.
type ServingCell struct {
	State  string // SEARCH, LIMSRV, NOCONN, CONNECT
	RAT    string // LTE, WCDMA, GSM, ...
	MCC    string
	MNC    string
	CellID string
	PCI    int
	EARFCN int
	Band   int
	TAC    string
	RSRP   int // dBm
	RSRQ   int // dB
	RSSI   int // dBm
	SINR   int // dB
	Valid  bool
	Raw    string
}

func parseServingCell(lines []string) ServingCell {
	var cell ServingCell
	for _, line := range lines {
		value, ok := strings.CutPrefix(line, "+QENG:")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if cell.Raw != "" {
			cell.Raw += "\n"
		}
		cell.Raw += value
		parts := splitFields(value)
		if len(parts) < 3 || parts[0] != "servingcell" {
			continue
		}
		cell.State, cell.RAT = parts[1], parts[2]
		if cell.RAT != "LTE" || len(parts) < 17 {
			continue
		}
		atoi := func(i int) int { n, _ := strconv.Atoi(parts[i]); return n }
		cell.MCC, cell.MNC, cell.CellID = parts[4], parts[5], parts[6]
		cell.PCI, cell.EARFCN, cell.Band = atoi(7), atoi(8), atoi(9)
		cell.TAC = parts[12]
		cell.RSRP, cell.RSRQ, cell.RSSI, cell.SINR = atoi(13), atoi(14), atoi(15), atoi(16)
		// Some firmware reports SINR as 0-250 in 1/5 dB steps from -20 dB.
		if cell.SINR > 40 {
			cell.SINR = cell.SINR/5 - 20
		}
		cell.Valid = true
	}
	return cell
}

func validatePIN(pin, name string) error {
	for _, r := range pin {
		if r < '0' || r > '9' {
			return fmt.Errorf("%s只能包含数字", name)
		}
	}
	if len(pin) < 4 || len(pin) > 8 {
		return fmt.Errorf("%s需要 4 到 8 位数字", name)
	}
	return nil
}

// SetPINLock turns the SIM PIN requirement on or off. It needs the current
// PIN; a wrong PIN consumes one attempt.
func (m *Modem) SetPINLock(ctx context.Context, enabled bool, pin string) error {
	if err := validatePIN(pin, "PIN "); err != nil {
		return err
	}
	mode := 0
	if enabled {
		mode = 1
	}
	_, err := m.s.Command(ctx, fmt.Sprintf(`AT+CLCK="SC",%d,"%s"`, mode, pin), pinTimeout)
	return err
}

// PINLock reports whether the SIM asks for a PIN at power-up.
func (m *Modem) PINLock(ctx context.Context) (bool, error) {
	lines, err := m.s.Command(ctx, `AT+CLCK="SC",2`, shortTimeout)
	if err != nil {
		return false, err
	}
	value, ok := field(lines, "+CLCK:")
	if !ok {
		return false, errors.New("AT+CLCK: missing +CLCK line")
	}
	return strings.TrimSpace(value) == "1", nil
}

// ChangePIN replaces the SIM PIN. The PIN lock must be enabled.
func (m *Modem) ChangePIN(ctx context.Context, oldPIN, newPIN string) error {
	if err := validatePIN(oldPIN, "当前 PIN "); err != nil {
		return err
	}
	if err := validatePIN(newPIN, "新 PIN "); err != nil {
		return err
	}
	_, err := m.s.Command(ctx, fmt.Sprintf(`AT+CPWD="SC","%s","%s"`, oldPIN, newPIN), pinTimeout)
	return err
}

// EnterPUK unblocks a SIM with its PUK and sets a new PIN. A wrong PUK
// consumes one of ten attempts; after that the SIM is unusable.
func (m *Modem) EnterPUK(ctx context.Context, puk, newPIN string) error {
	if len(puk) != 8 || strings.Trim(puk, "0123456789") != "" {
		return errors.New("PUK 码是 8 位数字")
	}
	if err := validatePIN(newPIN, "新 PIN "); err != nil {
		return err
	}
	_, err := m.s.Command(ctx, fmt.Sprintf(`AT+CPIN="%s","%s"`, puk, newPIN), pinTimeout)
	return err
}

// SetFunctionality switches the radio: FunFull, FunAirplane or FunMinimum.
func (m *Modem) SetFunctionality(ctx context.Context, fun int) error {
	switch fun {
	case FunMinimum, FunFull, FunAirplane:
	default:
		return fmt.Errorf("unsupported functionality %d", fun)
	}
	_, err := m.s.Command(ctx, "AT+CFUN="+strconv.Itoa(fun), cfunTimeout)
	return err
}

// Reboot restarts the module. The USB connection drops right after, which
// is reported as success.
func (m *Modem) Reboot(ctx context.Context) error {
	_, err := m.s.Command(ctx, "AT+CFUN=1,1", cfunTimeout)
	if errors.Is(err, ErrClosed) {
		return nil
	}
	return err
}

// SetScanMode selects which radio technologies the module may use (Quectel).
func (m *Modem) SetScanMode(ctx context.Context, mode int) error {
	if _, ok := ScanModes[mode]; !ok {
		return fmt.Errorf("unsupported scan mode %d", mode)
	}
	_, err := m.s.Command(ctx, fmt.Sprintf(`AT+QCFG="nwscanmode",%d,1`, mode), shortTimeout)
	return err
}

// CheckConsole validates a command typed by the user: one AT command, no
// control characters that could start an SMS body or abort input.
func CheckConsole(cmd string) error {
	if !strings.HasPrefix(strings.ToUpper(cmd), "AT") {
		return errors.New("指令需要以 AT 开头")
	}
	if strings.ContainsAny(cmd, "\r\n\x1a\x1b") || len(cmd) > 512 {
		return errors.New("指令包含不允许的字符或过长")
	}
	return nil
}

// Raw sends a command typed by the user and returns all response lines,
// including the final result.
func (m *Modem) Raw(ctx context.Context, cmd string, timeout time.Duration) ([]string, error) {
	cmd = strings.TrimSpace(cmd)
	if err := CheckConsole(cmd); err != nil {
		return nil, err
	}
	lines, err := m.s.Command(ctx, cmd, timeout)
	var cmdErr *CommandError
	switch {
	case errors.As(err, &cmdErr):
		return append(lines, cmdErr.Result), nil
	case err != nil:
		return lines, err
	}
	return append(lines, "OK"), nil
}
