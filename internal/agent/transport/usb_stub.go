//go:build !cgo

package transport

import (
	"errors"
	"io"
)

func openUSB(Config) (io.ReadWriteCloser, error) {
	return nil, errors.New("usb transport needs a cgo build with libusb (CGO_ENABLED=1); use -transport serial instead")
}

func discoverUSB() ([]Config, error) {
	return nil, errors.New("usb discovery needs a cgo build with libusb (CGO_ENABLED=1); use -transport serial instead")
}

func resetUSB(Config) error {
	return errors.New("usb reset needs a cgo build with libusb")
}

func usbSibling(io.ReadWriteCloser, int) (io.ReadWriteCloser, error) {
	return nil, errors.New("usb audio needs a cgo build with libusb")
}
