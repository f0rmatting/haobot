//go:build !linux

package transport

import "errors"

func resetSerial(Config) error {
	return errors.New("串口方式的 USB 复位只支持 Linux")
}
