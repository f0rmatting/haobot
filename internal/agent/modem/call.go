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
	dialTimeout = 30 * time.Second
	// answerTimeout bounds ATA, which a working modem finishes within a
	// second; waiting longer only holds up everything else.
	answerTimeout = 10 * time.Second
	ssTimeout     = 30 * time.Second // supplementary services ask the network
)

// CallState is the <stat> field of AT+CLCC.
type CallState int

const (
	CallActive   CallState = 0
	CallHeld     CallState = 1
	CallDialing  CallState = 2 // outgoing, not yet ringing
	CallAlerting CallState = 3 // outgoing, the other phone rings
	CallIncoming CallState = 4 // incoming, ringing here
	CallWaiting  CallState = 5 // incoming while another call is up
)

// Call is one voice call from AT+CLCC.
type Call struct {
	ID       int
	Incoming bool
	State    CallState
	Number   string
}

// parseCalls reads AT+CLCC lines. EC20 also lists its data connections
// there (mode 1); only voice calls (mode 0) are returned.
func parseCalls(lines []string) []Call {
	var out []Call
	for _, line := range lines {
		value, ok := strings.CutPrefix(line, "+CLCC:")
		if !ok {
			continue
		}
		parts := splitFields(strings.TrimSpace(value))
		if len(parts) < 5 || parts[3] != "0" {
			continue
		}
		id, err1 := strconv.Atoi(parts[0])
		state, err2 := strconv.Atoi(parts[2])
		if err1 != nil || err2 != nil {
			continue
		}
		c := Call{ID: id, Incoming: parts[1] == "1", State: CallState(state)}
		if len(parts) >= 6 {
			c.Number = parts[5]
		}
		out = append(out, c)
	}
	return out
}

// Calls lists the current voice calls.
func (m *Modem) Calls(ctx context.Context) ([]Call, error) {
	lines, err := m.s.Command(ctx, "AT+CLCC", shortTimeout)
	if err != nil {
		return nil, err
	}
	return parseCalls(lines), nil
}

// EnableCallReports asks for the caller's number with RING (+CLIP) and,
// where the firmware supports it, a Quectel report on every call state
// change. Failures are ignored: polling AT+CLCC still works.
func (m *Modem) EnableCallReports(ctx context.Context) {
	m.s.Command(ctx, "AT+CLIP=1", shortTimeout)
	m.s.Command(ctx, `AT+QINDCFG="ccinfo",1`, shortTimeout)
}

// URC ports of Quectel modules: which USB interface carries unsolicited
// results such as RING and +CMTI.
const (
	URCPortAT    = "usbat"    // the AT interface (EC20 interface 2, ttyUSB2)
	URCPortModem = "usbmodem" // the modem interface (interface 3, ttyUSB3)
)

// RouteURCs makes the module send unsolicited results to port, so RING
// reaches the interface being read. Some modules ship sending them to the
// modem interface, which leaves incoming calls to be found by polling. It
// reports the previous setting when it changed one.
func (m *Modem) RouteURCs(ctx context.Context, port string) (previous string, err error) {
	lines, err := m.s.Command(ctx, `AT+QURCCFG="urcport"`, shortTimeout)
	if err != nil {
		return "", err
	}
	current := ""
	for _, l := range lines {
		if rest, ok := strings.CutPrefix(l, "+QURCCFG:"); ok {
			fields := strings.Split(rest, ",")
			if len(fields) >= 2 {
				current = strings.Trim(strings.TrimSpace(fields[1]), `"`)
			}
		}
	}
	if current == "" || current == port {
		return "", nil
	}
	if _, err := m.s.Command(ctx, `AT+QURCCFG="urcport","`+port+`"`, shortTimeout); err != nil {
		return "", err
	}
	return current, nil
}

// Dial starts a voice call. The trailing ";" makes it voice, not data.
func (m *Modem) Dial(ctx context.Context, number string) error {
	number, err := CleanNumber(number)
	if err != nil {
		return err
	}
	_, err = m.s.Command(ctx, "ATD"+number+";", dialTimeout)
	return err
}

// Answer picks up a ringing call.
func (m *Modem) Answer(ctx context.Context) error {
	_, err := m.s.Command(ctx, "ATA", answerTimeout)
	return err
}

// Hangup ends every call, or rejects a ringing one.
func (m *Modem) Hangup(ctx context.Context) error {
	_, err := m.s.Command(ctx, "AT+CHUP", shortTimeout)
	return err
}

// SendDTMF plays one key tone into the active call.
func (m *Modem) SendDTMF(ctx context.Context, key string) error {
	if len(key) != 1 || !strings.Contains("0123456789*#ABCD", key) {
		return errors.New("按键只能是 0-9、*、# 或 A-D")
	}
	_, err := m.s.Command(ctx, "AT+VTS="+key, shortTimeout)
	return err
}

// SetVoiceOverUSB switches call audio to 8 kHz 16-bit PCM on the USB NMEA
// port (Quectel AT+QPCMV), or back off.
func (m *Modem) SetVoiceOverUSB(ctx context.Context, on bool) error {
	cmd := "AT+QPCMV=0"
	if on {
		cmd = "AT+QPCMV=1,0"
	}
	_, err := m.s.Command(ctx, cmd, shortTimeout)
	return err
}

// Forwarding reasons of AT+CCFC.
const (
	ForwardAlways      = 0
	ForwardBusy        = 1
	ForwardNoReply     = 2
	ForwardUnreachable = 3
)

// ForwardReasons names the reasons for the UI.
var ForwardReasons = map[int]string{
	ForwardAlways:      "无条件转移",
	ForwardBusy:        "遇忙转移",
	ForwardNoReply:     "无应答转移",
	ForwardUnreachable: "无法接通时转移",
}

// Forwarding is the network's call forwarding setting for one reason.
type Forwarding struct {
	Reason  int
	Enabled bool
	Number  string
}

// QueryForwarding asks the network for one reason's setting.
func (m *Modem) QueryForwarding(ctx context.Context, reason int) (Forwarding, error) {
	if _, ok := ForwardReasons[reason]; !ok {
		return Forwarding{}, fmt.Errorf("unknown forwarding reason %d", reason)
	}
	lines, err := m.s.Command(ctx, fmt.Sprintf("AT+CCFC=%d,2", reason), ssTimeout)
	if err != nil {
		return Forwarding{}, err
	}
	f := Forwarding{Reason: reason}
	for _, line := range lines {
		value, ok := strings.CutPrefix(line, "+CCFC:")
		if !ok {
			continue
		}
		parts := splitFields(strings.TrimSpace(value))
		if len(parts) < 2 {
			continue
		}
		// One line per service class; voice is class 1.
		class, _ := strconv.Atoi(parts[1])
		if class&1 == 0 && class != 0 {
			continue
		}
		f.Enabled = parts[0] == "1"
		if len(parts) >= 3 {
			f.Number = parts[2]
		}
	}
	return f, nil
}

// SetForwarding turns forwarding for a reason on (to number) or off.
func (m *Modem) SetForwarding(ctx context.Context, reason int, enabled bool, number string) error {
	if _, ok := ForwardReasons[reason]; !ok {
		return fmt.Errorf("unknown forwarding reason %d", reason)
	}
	cmd := fmt.Sprintf("AT+CCFC=%d,0", reason)
	if enabled {
		n, err := CleanNumber(number)
		if err != nil {
			return err
		}
		toa := 129
		if strings.HasPrefix(n, "+") {
			toa = 145
		}
		// Mode 3 registers the number and enables forwarding in one step.
		cmd = fmt.Sprintf(`AT+CCFC=%d,3,"%s",%d`, reason, n, toa)
	}
	_, err := m.s.Command(ctx, cmd, ssTimeout)
	return err
}
