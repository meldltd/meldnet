//go:build android

// The JNI shape follows upstream wireguard-android's Apache-2.0 libwg-go API.
// This mobile adapter adds private DNS interception and intentionally omits
// plaintext UAPI listeners, backend logging and process signal stack dumps.
package main

/*
#cgo LDFLAGS: -landroid
#include <android/multinetwork.h>
#include <stdlib.h>
static int bind_underlay(unsigned long long network, int fd) {
 return android_setsocknetwork((net_handle_t)network, fd);
}
*/
import "C"

import (
	nativecontrol "meldnet/mobile/native/control"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun"
	"meldnet/mobile/native/dnsbridge"
)

var mu sync.Mutex
var handles = map[int32]*device.Device{}
var next int32
var policyMu sync.RWMutex
var policy dnsbridge.Policy

func currentPolicy() dnsbridge.Policy { policyMu.RLock(); defer policyMu.RUnlock(); return policy }

//export meldnetSetDNS
func meldnetSetDNS(value string) int32 {
	p, e := dnsbridge.Parse(value)
	policyMu.Lock()
	defer policyMu.Unlock()
	policy = p
	if e != nil {
		return -1
	}
	return 0
}
func control(network uint64) func(string, string, syscall.RawConn) error {
	return func(_, _ string, raw syscall.RawConn) error {
		result := 0
		err := raw.Control(func(fd uintptr) { result = int(C.bind_underlay(C.ulonglong(network), C.int(fd))) })
		if err != nil {
			return err
		}
		if result != 0 {
			return syscall.EPERM
		}
		return nil
	}
}

//export wgTurnOn
func wgTurnOn(_ string, fd int32, settings string) int32 {
	mu.Lock()
	defer mu.Unlock()
	if currentPolicy().Primary == "" {
		unix.Close(int(fd))
		return -1
	}
	base, _, e := tun.CreateUnmonitoredTUNFromFD(int(fd))
	if e != nil {
		unix.Close(int(fd))
		return -1
	}
	wrapped, e := dnsbridge.Wrap(base, currentPolicy, control)
	if e != nil {
		base.Close()
		return -1
	}
	dev := device.NewDevice(wrapped, conn.NewStdNetBind(), device.NewLogger(device.LogLevelSilent, ""))
	if e = dev.IpcSet(settings); e != nil {
		dev.Close()
		return -1
	}
	dev.DisableSomeRoamingForBrokenMobileSemantics()
	if e = dev.Up(); e != nil {
		dev.Close()
		return -1
	}
	next++
	if next <= 0 {
		next = 1
	}
	handles[next] = dev
	return next
}

//export wgTurnOff
func wgTurnOff(id int32) {
	mu.Lock()
	defer mu.Unlock()
	if d := handles[id]; d != nil {
		delete(handles, id)
		d.Close()
	}
}
func socket(id int32, v6 bool) int32 {
	mu.Lock()
	defer mu.Unlock()
	d := handles[id]
	if d == nil {
		return -1
	}
	b, ok := d.Bind().(conn.PeekLookAtSocketFd)
	if !ok {
		return -1
	}
	var fd int
	var e error
	if v6 {
		fd, e = b.PeekLookAtSocketFd6()
	} else {
		fd, e = b.PeekLookAtSocketFd4()
	}
	if e != nil {
		return -1
	}
	return int32(fd)
}

//export wgGetSocketV4
func wgGetSocketV4(id int32) int32 { return socket(id, false) }

//export wgGetSocketV6
func wgGetSocketV6(id int32) int32 { return socket(id, true) }

//export wgGetConfig
func wgGetConfig(id int32) *C.char {
	mu.Lock()
	defer mu.Unlock()
	d := handles[id]
	if d == nil {
		return nil
	}
	raw, e := d.IpcGet()
	if e != nil {
		return nil
	}
	var out strings.Builder
	for _, line := range strings.Split(raw, "\n") {
		key, _, _ := strings.Cut(line, "=")
		switch key {
		case "public_key", "last_handshake_time_sec", "last_handshake_time_nsec", "rx_bytes", "tx_bytes":
			out.WriteString(line)
			out.WriteByte('\n')
		}
	}
	return C.CString(out.String())
}

//export wgVersion
func wgVersion() *C.char { return C.CString("meldnet-dns-v1") }
func main()              {}

var _ unsafe.Pointer

//export meldnetControl
func meldnetControl(value string) *C.char {
	response := nativecontrol.Do([]byte(value), control)
	data := nativecontrol.Encode(response)
	return C.CString(string(data))
}
