package transport

import (
	"fmt"
	"io"

	"go.bug.st/serial"
)

func openSerial(cfg Config) (io.ReadWriteCloser, error) {
	baud := cfg.BaudRate
	if baud == 0 {
		baud = 115200
	}
	port, err := serial.Open(cfg.Device, &serial.Mode{
		BaudRate: baud,
		DataBits: 8,
		Parity:   serial.NoParity,
		StopBits: serial.OneStopBit,
	})
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", cfg.Device, err)
	}
	return port, nil
}
