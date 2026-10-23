package transport

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeSysfs lays out two EC20s the way the option driver does: four ttyUSB
// ports each, of which interface 2 is the AT port.
func fakeSysfs(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	devices := filepath.Join(root, "devices")
	tty := filepath.Join(root, "class", "tty")
	if err := os.MkdirAll(tty, 0o755); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, loc := range []string{"1-1.2", "1-1.3"} {
		usb := filepath.Join(devices, loc)
		os.MkdirAll(usb, 0o755)
		os.WriteFile(filepath.Join(usb, "idVendor"), []byte("2c7c\n"), 0o644)
		os.WriteFile(filepath.Join(usb, "idProduct"), []byte("0125\n"), 0o644)
		for i := 0; i < 4; i++ {
			name := "ttyUSB" + string(rune('0'+n))
			iface := filepath.Join(usb, loc+":1."+string(rune('0'+i)))
			port := filepath.Join(iface, name)
			os.MkdirAll(port, 0o755)
			os.WriteFile(filepath.Join(iface, "bInterfaceNumber"), []byte("0"+string(rune('0'+i))+"\n"), 0o644)
			os.MkdirAll(filepath.Join(tty, name), 0o755)
			if err := os.Symlink(port, filepath.Join(tty, name, "device")); err != nil {
				t.Fatal(err)
			}
			n++
		}
	}
	return tty
}

func TestDiscoverSerialFindsEachATPort(t *testing.T) {
	found, err := discoverSerialIn(fakeSysfs(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 {
		t.Fatalf("found %d ports, want 2: %+v", len(found), found)
	}
	want := map[string]string{"/dev/ttyUSB2": "1-1.2", "/dev/ttyUSB6": "1-1.3"}
	for _, cfg := range found {
		if want[cfg.Device] != cfg.Location || cfg.Kind != KindSerial || cfg.Interface != 2 {
			t.Errorf("unexpected port %+v", cfg)
		}
	}
	if found[0].Key() == found[1].Key() {
		t.Fatal("two modems share a key")
	}
}

func TestSiblingTTYFindsAudioPort(t *testing.T) {
	root := fakeSysfs(t)
	got, err := siblingTTY(root, "/dev/ttyUSB6", 1)
	if err != nil || got != "/dev/ttyUSB5" {
		t.Fatalf("siblingTTY = %q, %v; want /dev/ttyUSB5 (interface 1 of the second modem)", got, err)
	}
}
