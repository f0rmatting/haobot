package transport

import "fmt"

// Reset power-cycles the modem's USB connection, the way unplugging it would.
// It recovers modules whose AT port no longer answers, when a soft reboot
// (AT+CFUN=1,1) cannot even be sent. The port must be closed first; the
// device re-enumerates a few seconds later.
func Reset(cfg Config) error {
	switch cfg.Kind {
	case KindUSB:
		return resetUSB(cfg)
	case KindSerial:
		return resetSerial(cfg)
	default:
		return fmt.Errorf("unknown transport %q", cfg.Kind)
	}
}
