//go:build cgo

package transport

/*
#cgo pkg-config: libusb-1.0
#include <libusb.h>
*/
import "C"

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"
)

const (
	// usbReadTimeoutMS bounds each bulk IN transfer so Close never waits long
	// for an in-flight read.
	usbReadTimeoutMS  = 250
	usbWriteTimeoutMS = 2000
	// usbAudioWriteTimeoutMS is for call audio frames, see usbSibling.
	usbAudioWriteTimeoutMS = 100
	// usbReadBufferSize is a multiple of the high-speed bulk packet size (512),
	// which libusb requires to avoid overflow errors on IN transfers.
	usbReadBufferSize = 4096
	usbTransferBulk   = 2 // LIBUSB_TRANSFER_TYPE_BULK in bmAttributes
)

var errPortClosed = errors.New("usb port closed")

type usbPort struct {
	ctx     *C.libusb_context
	handle  *C.libusb_device_handle
	iface   C.int
	in, out C.uchar

	// owner is false for a sibling interface sharing another port's
	// handle; closing it only releases its interface.
	owner bool
	// writeTimeoutMS bounds each bulk OUT transfer.
	writeTimeoutMS C.uint

	closed    atomic.Bool
	closeOnce sync.Once
	readMu    sync.Mutex
	writeMu   sync.Mutex
	buf       []byte
	pending   []byte
}

func openUSB(cfg Config) (io.ReadWriteCloser, error) {
	var ctx *C.libusb_context
	if rc := C.libusb_init(&ctx); rc != 0 {
		return nil, usbError("init libusb", rc)
	}
	var handle *C.libusb_device_handle
	if cfg.Location == "" {
		handle = C.libusb_open_device_with_vid_pid(ctx, C.uint16_t(cfg.VendorID), C.uint16_t(cfg.ProductID))
	} else {
		handle = openAt(ctx, cfg)
	}
	if handle == nil {
		C.libusb_exit(ctx)
		if cfg.Location != "" {
			return nil, fmt.Errorf("usb device %04x:%04x at %s not found or not accessible", cfg.VendorID, cfg.ProductID, cfg.Location)
		}
		return nil, fmt.Errorf("usb device %04x:%04x not found or not accessible", cfg.VendorID, cfg.ProductID)
	}
	in, out, err := bulkEndpoints(handle, cfg.Interface)
	if err != nil {
		C.libusb_close(handle)
		C.libusb_exit(ctx)
		return nil, err
	}
	// Linux binds the option driver to the AT ports; detach it while we hold
	// the interface. macOS reports this as unsupported, which is harmless.
	C.libusb_set_auto_detach_kernel_driver(handle, 1)
	if rc := C.libusb_claim_interface(handle, C.int(cfg.Interface)); rc != 0 {
		C.libusb_close(handle)
		C.libusb_exit(ctx)
		return nil, usbError(fmt.Sprintf("claim interface %d", cfg.Interface), rc)
	}
	return &usbPort{
		ctx:    ctx,
		handle: handle,
		iface:  C.int(cfg.Interface),
		in:     in,
		out:    out,
		buf:    make([]byte, usbReadBufferSize),
		owner:  true,

		writeTimeoutMS: usbWriteTimeoutMS,
	}, nil
}

// usbSibling claims another interface of the device behind main, sharing
// its handle: macOS does not let a process open the same device twice. It
// must be closed before main.
func usbSibling(main io.ReadWriteCloser, iface int) (io.ReadWriteCloser, error) {
	p, ok := main.(*usbPort)
	if !ok {
		return nil, errors.New("not a usb port")
	}
	in, out, err := bulkEndpoints(p.handle, iface)
	if err != nil {
		return nil, err
	}
	if rc := C.libusb_claim_interface(p.handle, C.int(iface)); rc != 0 {
		return nil, usbError(fmt.Sprintf("claim interface %d", iface), rc)
	}
	// Audio frames last 20 ms: a write stuck longer is better dropped than
	// allowed to stall the pacing.
	return &usbPort{ctx: p.ctx, handle: p.handle, iface: C.int(iface), in: in, out: out, buf: make([]byte, usbReadBufferSize), writeTimeoutMS: usbAudioWriteTimeoutMS}, nil
}

// openAt opens the device with cfg's IDs plugged into cfg.Location.
func openAt(ctx *C.libusb_context, cfg Config) *C.libusb_device_handle {
	var list **C.libusb_device
	count := C.libusb_get_device_list(ctx, &list)
	if count < 0 {
		return nil
	}
	defer C.libusb_free_device_list(list, 1)
	for _, dev := range unsafe.Slice(list, int(count)) {
		var desc C.struct_libusb_device_descriptor
		if C.libusb_get_device_descriptor(dev, &desc) != 0 {
			continue
		}
		if uint16(desc.idVendor) != cfg.VendorID || uint16(desc.idProduct) != cfg.ProductID || location(dev) != cfg.Location {
			continue
		}
		var handle *C.libusb_device_handle
		if C.libusb_open(dev, &handle) == 0 {
			return handle
		}
		return nil
	}
	return nil
}

// location formats a device's bus and port path the way Linux sysfs does.
func location(dev *C.libusb_device) string {
	var ports [8]C.uint8_t
	n := C.libusb_get_port_numbers(dev, &ports[0], C.int(len(ports)))
	if n <= 0 {
		return fmt.Sprintf("%d", C.libusb_get_bus_number(dev))
	}
	path := make([]string, n)
	for i := range path {
		path[i] = fmt.Sprint(ports[i])
	}
	return fmt.Sprintf("%d-%s", C.libusb_get_bus_number(dev), strings.Join(path, "."))
}

// resetUSB re-enumerates the device, like unplugging and plugging it in.
func resetUSB(cfg Config) error {
	var ctx *C.libusb_context
	if rc := C.libusb_init(&ctx); rc != 0 {
		return usbError("init libusb", rc)
	}
	defer C.libusb_exit(ctx)
	var handle *C.libusb_device_handle
	if cfg.Location == "" {
		handle = C.libusb_open_device_with_vid_pid(ctx, C.uint16_t(cfg.VendorID), C.uint16_t(cfg.ProductID))
	} else {
		handle = openAt(ctx, cfg)
	}
	if handle == nil {
		return fmt.Errorf("usb device %04x:%04x not found or not accessible", cfg.VendorID, cfg.ProductID)
	}
	defer C.libusb_close(handle)
	// NOT_FOUND after a reset means the device already re-enumerated.
	if rc := C.libusb_reset_device(handle); rc != 0 && rc != C.LIBUSB_ERROR_NOT_FOUND {
		return usbError("reset", rc)
	}
	return nil
}

func discoverUSB() ([]Config, error) {
	var ctx *C.libusb_context
	if rc := C.libusb_init(&ctx); rc != 0 {
		return nil, usbError("init libusb", rc)
	}
	defer C.libusb_exit(ctx)
	var list **C.libusb_device
	count := C.libusb_get_device_list(ctx, &list)
	if count < 0 {
		return nil, usbError("list devices", C.int(count))
	}
	defer C.libusb_free_device_list(list, 1)
	var out []Config
	for _, dev := range unsafe.Slice(list, int(count)) {
		var desc C.struct_libusb_device_descriptor
		if C.libusb_get_device_descriptor(dev, &desc) != 0 {
			continue
		}
		model, known := findModel(uint16(desc.idVendor), uint16(desc.idProduct))
		if !known {
			continue
		}
		out = append(out, Config{
			Kind: KindUSB, VendorID: model.VendorID, ProductID: model.ProductID,
			Interface: model.ATInterface, Location: location(dev), Model: model.Name,
		})
	}
	return out, nil
}

func bulkEndpoints(handle *C.libusb_device_handle, iface int) (in, out C.uchar, err error) {
	var config *C.struct_libusb_config_descriptor
	if rc := C.libusb_get_active_config_descriptor(C.libusb_get_device(handle), &config); rc != 0 {
		return 0, 0, usbError("read config descriptor", rc)
	}
	defer C.libusb_free_config_descriptor(config)
	if iface < 0 || iface >= int(config.bNumInterfaces) {
		return 0, 0, fmt.Errorf("usb interface %d does not exist (device has %d)", iface, config.bNumInterfaces)
	}
	interfaces := unsafe.Slice(config._interface, config.bNumInterfaces)
	if interfaces[iface].num_altsetting < 1 {
		return 0, 0, fmt.Errorf("usb interface %d has no settings", iface)
	}
	setting := interfaces[iface].altsetting
	for _, endpoint := range unsafe.Slice(setting.endpoint, setting.bNumEndpoints) {
		if endpoint.bmAttributes&3 != usbTransferBulk {
			continue
		}
		if endpoint.bEndpointAddress&0x80 != 0 {
			in = C.uchar(endpoint.bEndpointAddress)
		} else {
			out = C.uchar(endpoint.bEndpointAddress)
		}
	}
	if in == 0 || out == 0 {
		return 0, 0, fmt.Errorf("usb interface %d has no bulk IN/OUT endpoint pair", iface)
	}
	return in, out, nil
}

func (p *usbPort) Read(b []byte) (int, error) {
	p.readMu.Lock()
	defer p.readMu.Unlock()
	for len(p.pending) == 0 {
		if p.closed.Load() {
			return 0, errPortClosed
		}
		var n C.int
		rc := C.libusb_bulk_transfer(p.handle, p.in, (*C.uchar)(unsafe.Pointer(&p.buf[0])), C.int(len(p.buf)), &n, usbReadTimeoutMS)
		if n > 0 {
			p.pending = p.buf[:n]
			break
		}
		if rc == C.LIBUSB_ERROR_TIMEOUT {
			continue
		}
		if rc != 0 {
			return 0, usbError("read", rc)
		}
	}
	n := copy(b, p.pending)
	p.pending = p.pending[n:]
	return n, nil
}

func (p *usbPort) Write(b []byte) (int, error) {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	written := 0
	for written < len(b) {
		if p.closed.Load() {
			return written, errPortClosed
		}
		var n C.int
		rc := C.libusb_bulk_transfer(p.handle, p.out, (*C.uchar)(unsafe.Pointer(&b[written])), C.int(len(b)-written), &n, p.writeTimeoutMS)
		written += int(n)
		if rc != 0 {
			return written, usbError("write", rc)
		}
	}
	return written, nil
}

func (p *usbPort) Close() error {
	p.closeOnce.Do(func() {
		p.closed.Store(true)
		// Wait for in-flight transfers; reads give up within usbReadTimeoutMS.
		p.readMu.Lock()
		p.writeMu.Lock()
		C.libusb_release_interface(p.handle, p.iface)
		if p.owner {
			C.libusb_close(p.handle)
			C.libusb_exit(p.ctx)
		}
		p.writeMu.Unlock()
		p.readMu.Unlock()
	})
	return nil
}

func usbError(op string, rc C.int) error {
	return fmt.Errorf("%s: %s", op, C.GoString(C.libusb_error_name(rc)))
}
