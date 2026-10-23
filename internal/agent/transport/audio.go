package transport

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// defaultAudioInterface is the NMEA port of Quectel modules, which carries
// call audio once AT+QPCMV=1,0 is set.
const defaultAudioInterface = 1

func (cfg Config) audioInterface() int {
	if cfg.AudioInterface > 0 {
		return cfg.AudioInterface
	}
	return defaultAudioInterface
}

// OpenAudio opens the port that carries call audio for the modem whose AT
// port is main. Close it before main.
func OpenAudio(cfg Config, main io.ReadWriteCloser) (io.ReadWriteCloser, error) {
	if cfg.AudioDevice != "" {
		return openSerial(Config{Device: cfg.AudioDevice, BaudRate: cfg.BaudRate})
	}
	switch cfg.Kind {
	case KindUSB:
		return usbSibling(main, cfg.audioInterface())
	case KindSerial:
		path, err := siblingTTY(sysTTY, cfg.Device, cfg.audioInterface())
		if err != nil {
			return nil, err
		}
		return openSerial(Config{Device: path, BaudRate: cfg.BaudRate})
	}
	return nil, fmt.Errorf("unknown transport %q", cfg.Kind)
}

// siblingTTY finds the /dev/ttyUSB* of another interface of the USB device
// behind device, through sysfs (Linux only).
func siblingTTY(root, device string, iface int) (string, error) {
	if runtime.GOOS != "linux" && root == sysTTY {
		return "", errors.New("串口方式的通话音频只支持 Linux；可以用 -audio-device 指定音频串口")
	}
	usbOf := func(tty string) (string, int, bool) {
		port, err := filepath.EvalSymlinks(filepath.Join(root, tty, "device"))
		if err != nil {
			return "", 0, false
		}
		ifaceDir := filepath.Dir(port)
		n, ok := readHex(filepath.Join(ifaceDir, "bInterfaceNumber"))
		return filepath.Dir(ifaceDir), int(n), ok
	}
	want, _, ok := usbOf(filepath.Base(device))
	if !ok {
		return "", fmt.Errorf("找不到 %s 对应的 USB 设备", device)
	}
	ttys, _ := filepath.Glob(filepath.Join(root, "ttyUSB*"))
	for _, tty := range ttys {
		if usb, n, ok := usbOf(filepath.Base(tty)); ok && usb == want && n == iface {
			path := "/dev/" + filepath.Base(tty)
			if _, err := os.Stat(path); err != nil && root == sysTTY {
				return "", err
			}
			return path, nil
		}
	}
	return "", fmt.Errorf("没有找到 %s 所在模组的音频口（接口 %d）", device, iface)
}
