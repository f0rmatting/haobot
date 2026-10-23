package transport

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// usbdevfsReset is USBDEVFS_RESET, _IO('U', 20).
const usbdevfsReset = 0x5514

// resetSerial finds the USB device behind a /dev/ttyUSB* port through sysfs
// and resets it with the usbfs ioctl. It needs write access to
// /dev/bus/usb/BBB/DDD (root, or the udev rule in deploy/agent).
func resetSerial(cfg Config) error {
	port, err := filepath.EvalSymlinks(filepath.Join(sysTTY, filepath.Base(cfg.Device), "device"))
	if err != nil {
		return fmt.Errorf("找不到 %s 对应的 USB 设备：%w", cfg.Device, err)
	}
	usb := filepath.Dir(filepath.Dir(port))
	bus, err1 := readInt(filepath.Join(usb, "busnum"))
	dev, err2 := readInt(filepath.Join(usb, "devnum"))
	if err1 != nil || err2 != nil {
		return fmt.Errorf("读取 %s 的 USB 总线号失败", cfg.Device)
	}
	node := fmt.Sprintf("/dev/bus/usb/%03d/%03d", bus, dev)
	f, err := os.OpenFile(node, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("打开 %s 失败（需要写权限，见 deploy/agent/99-ec20.rules）：%w", node, err)
	}
	defer f.Close()
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, f.Fd(), usbdevfsReset, 0); errno != 0 {
		return fmt.Errorf("USB 复位失败：%w", errno)
	}
	return nil
}

func readInt(path string) (int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(raw)))
}
