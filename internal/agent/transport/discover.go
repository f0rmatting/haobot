package transport

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// Model is a modem the agent recognises when discovering devices.
type Model struct {
	Name        string
	VendorID    uint16
	ProductID   uint16
	ATInterface int // USB interface number of the AT command port
}

// Models lists recognised modems. Only the EC20 has been tested on real
// hardware; the others share Quectel's or SIMCom's usual interface layout.
var Models = []Model{
	{"Quectel EC20/EC25/EG25", 0x2c7c, 0x0125, 2},
	{"Quectel EC21", 0x2c7c, 0x0121, 2},
	{"Quectel EG91/EG95", 0x2c7c, 0x0191, 2},
	{"Quectel BG96", 0x2c7c, 0x0296, 2},
	{"SIMCom SIM7600", 0x1e0e, 0x9001, 2},
}

func findModel(vendor, product uint16) (Model, bool) {
	for _, m := range Models {
		if m.VendorID == vendor && m.ProductID == product {
			return m, true
		}
	}
	return Model{}, false
}

// Key identifies the endpoint a config points at, so discovery can tell new
// ports from ones already in use.
func (cfg Config) Key() string {
	if cfg.Kind == KindUSB {
		return fmt.Sprintf("usb:%04x:%04x@%s#%d", cfg.VendorID, cfg.ProductID, cfg.Location, cfg.Interface)
	}
	return "serial:" + cfg.Device
}

// Discover lists the AT ports of every recognised modem reachable with the
// given transport kind, sorted by location.
func Discover(kind string) ([]Config, error) {
	var found []Config
	var err error
	switch kind {
	case KindUSB:
		found, err = discoverUSB()
	case KindSerial:
		found, err = discoverSerial()
	default:
		return nil, fmt.Errorf("unknown transport %q", kind)
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Key() < found[j].Key() })
	return found, err
}

const sysTTY = "/sys/class/tty"

// discoverSerial finds AT ports created by the Linux option driver: for each
// /dev/ttyUSB*, sysfs tells which USB device and interface it belongs to.
func discoverSerial() ([]Config, error) {
	if runtime.GOOS != "linux" {
		return nil, nil
	}
	return discoverSerialIn(sysTTY)
}

func discoverSerialIn(root string) ([]Config, error) {
	ttys, err := filepath.Glob(filepath.Join(root, "ttyUSB*"))
	if err != nil {
		return nil, err
	}
	var out []Config
	for _, tty := range ttys {
		port, err := filepath.EvalSymlinks(filepath.Join(tty, "device"))
		if err != nil {
			continue
		}
		iface := filepath.Dir(port) // .../1-1.2:1.2
		usb := filepath.Dir(iface)  // .../1-1.2
		vendor, okV := readHex(filepath.Join(usb, "idVendor"))
		product, okP := readHex(filepath.Join(usb, "idProduct"))
		number, okI := readHex(filepath.Join(iface, "bInterfaceNumber"))
		if !okV || !okP || !okI {
			continue
		}
		model, known := findModel(vendor, product)
		if !known || int(number) != model.ATInterface {
			continue
		}
		out = append(out, Config{
			Kind: KindSerial, Device: "/dev/" + filepath.Base(tty), BaudRate: 115200,
			VendorID: vendor, ProductID: product, Interface: model.ATInterface,
			Location: filepath.Base(usb), Model: model.Name,
		})
	}
	return out, nil
}

func readHex(path string) (uint16, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	n, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 16, 16)
	return uint16(n), err == nil
}
