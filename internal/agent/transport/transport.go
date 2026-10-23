// Package transport opens the byte stream to the modem's AT port, either
// through a serial device node or directly over USB with libusb.
package transport

import (
	"fmt"
	"io"
	"runtime"
)

const (
	KindUSB    = "usb"
	KindSerial = "serial"
)

// Config selects and parameterises the transport.
type Config struct {
	Kind string

	// Serial transport.
	Device   string
	BaudRate int

	// USB transport. Interface is the USB interface number of the AT port.
	VendorID  uint16
	ProductID uint16
	Interface int
	// Location is the USB port path ("1-2.3"), which tells identical
	// modems apart. Empty opens the first matching device.
	Location string

	// Model is the recognised model name, set by Discover.
	Model string

	// AudioInterface is the USB interface carrying call audio (Quectel's
	// NMEA port with AT+QPCMV); 0 means the usual 1. AudioDevice names a
	// serial port for call audio directly, overriding the lookup.
	AudioInterface int
	AudioDevice    string
}

// DefaultKind returns the transport that works without extra setup on the
// current OS: macOS has no driver for the modem's vendor-specific interfaces,
// while Linux exposes them as /dev/ttyUSB* through the option driver.
func DefaultKind() string {
	if runtime.GOOS == "darwin" {
		return KindUSB
	}
	return KindSerial
}

// Open connects to the AT port described by cfg.
func Open(cfg Config) (io.ReadWriteCloser, error) {
	switch cfg.Kind {
	case KindUSB:
		return openUSB(cfg)
	case KindSerial:
		return openSerial(cfg)
	default:
		return nil, fmt.Errorf("unknown transport %q (want %q or %q)", cfg.Kind, KindUSB, KindSerial)
	}
}

// Describe returns a short human-readable description of the endpoint.
func (cfg Config) Describe() string {
	if cfg.Kind == KindUSB {
		if cfg.Location != "" {
			return fmt.Sprintf("usb %04x:%04x @%s interface %d", cfg.VendorID, cfg.ProductID, cfg.Location, cfg.Interface)
		}
		return fmt.Sprintf("usb %04x:%04x interface %d", cfg.VendorID, cfg.ProductID, cfg.Interface)
	}
	if cfg.Location != "" {
		return "serial " + cfg.Device + " @" + cfg.Location
	}
	return "serial " + cfg.Device
}
