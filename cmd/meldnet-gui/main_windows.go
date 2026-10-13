// Meldnet's unprivileged Win32 frontend. The daemon owns all networking and keys.
package main

import (
	"context"
	"flag"
	"runtime"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"meldnet/internal/api"
	"meldnet/internal/desktop"
	"meldnet/internal/profiles"
)

var (
	user         = windows.NewLazySystemDLL("user32.dll")
	shell        = windows.NewLazySystemDLL("shell32.dll")
	gdi          = windows.NewLazySystemDLL("gdi32.dll")
	kernel       = windows.NewLazySystemDLL("kernel32.dll")
	createWindow = user.NewProc("CreateWindowExW")
	send         = user.NewProc("SendMessageW")
	setText      = user.NewProc("SetWindowTextW")
	show         = user.NewProc("ShowWindow")
	enable       = user.NewProc("EnableWindow")
	notify       = shell.NewProc("Shell_NotifyIconW")
	app          *window
)

const (
	wmCommand    = 0x111
	wmTray       = 0x8001
	wmResult     = 0x8002
	idNetworks   = 101
	idConnect    = 102
	idDisconnect = 103
	idAuto       = 104
	idPeers      = 105
	idCopy       = 106
	idJoin       = 107
	idRefresh    = 108
	idQuit       = 109
	idShow       = 110
)

type point struct{ X, Y int32 }
type message struct {
	Window         uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	Point          point
	Private        uint32
}
type windowClass struct {
	Size, Style                        uint32
	Proc                               uintptr
	ClassExtra, WindowExtra            int32
	Instance, Icon, Cursor, Background uintptr
	Menu, Name                         *uint16
	SmallIcon                          uintptr
}

// NOTIFYICONDATAW layout for both supported Windows 64-bit architectures.
type notification struct {
	Size                uint32
	Window              uintptr
	ID, Flags, Callback uint32
	Icon                uintptr
	Tip                 [128]uint16
	State, StateMask    uint32
	Info                [256]uint16
	Version             uint32
	Title               [64]uint16
	InfoFlags           uint32
	GUID                windows.GUID
	BalloonIcon         uintptr
}
type window struct {
	hwnd, instance, icon, font                               uintptr
	controls                                                 map[int]uintptr
	status, details, networkID, name, key, joinAuto, joinNow uintptr
	tray                                                     notification
	trayOK, busy, mutating                                   bool
	taskbar                                                  uint32
	backend                                                  desktop.Backend
	results                                                  chan desktop.Result
	profiles                                                 []profiles.Profile
	peers                                                    []desktop.Peer
	selected                                                 string
	dpi                                                      int
}

func str(s string) *uint16 {
	p, _ := windows.UTF16PtrFromString(strings.ReplaceAll(s, "\x00", ""))
	return p
}
func callText(h uintptr, s string) { setText.Call(h, uintptr(unsafe.Pointer(str(s)))) }

//go:uintptrescapes
func msg(h uintptr, m uint32, w, l uintptr) uintptr {
	r, _, _ := send.Call(h, uintptr(m), w, l)
	return r
}
func (a *window) scale(v int) uintptr { return uintptr(v * a.dpi / 96) }
func (a *window) control(class, text string, style uintptr, x, y, w, h, id int) uintptr {
	r, _, _ := createWindow.Call(0, uintptr(unsafe.Pointer(str(class))), uintptr(unsafe.Pointer(str(text))), 0x50000000|style, a.scale(x), a.scale(y), a.scale(w), a.scale(h), a.hwnd, uintptr(id), a.instance, 0)
	if r == 0 {
		panic("Cannot create Windows control")
	}
	msg(r, 0x30, a.font, 1)
	if id != 0 {
		a.controls[id] = r
	}
	return r
}
func (a *window) label(text string, x, y, w, h int) uintptr {
	return a.control("STATIC", text, 0, x, y, w, h, 0)
}
func (a *window) button(text string, x, y, w, id int) uintptr {
	return a.control("BUTTON", text, 0x10000, x, y, w, 30, id)
}
func (a *window) edit(x, y, w, limit int, password bool) uintptr {
	style := uintptr(0x10000 | 0x800000 | 0x80)
	if password {
		style |= 0x20
	}
	h := a.control("EDIT", "", style, x, y, w, 26, 0)
	msg(h, 0xc5, uintptr(limit), 0)
	return h
}
func readText(h uintptr) string {
	n, _, _ := user.NewProc("GetWindowTextLengthW").Call(h)
	if n > 16384 {
		return ""
	}
	b := make([]uint16, n+1)
	user.NewProc("GetWindowTextW").Call(h, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)))
	s := windows.UTF16ToString(b)
	clear(b)
	return s
}
func checked(h uintptr) bool { return msg(h, 0xf0, 0, 0) == 1 }
func (a *window) build() {
	a.label("Meldnet", 20, 16, 640, 26)
	a.label("Your networks · managed by the independent Meldnet service", 20, 46, 650, 24)
	a.label("&Network", 20, 85, 120, 24)
	a.control("COMBOBOX", "", 0x10000|0x200000|3, 145, 81, 515, 220, idNetworks)
	a.status = a.label("Loading network state…", 20, 121, 640, 26)
	a.button("&Connect", 20, 156, 110, idConnect)
	a.button("&Disconnect", 140, 156, 110, idDisconnect)
	a.control("BUTTON", "Connect at service &startup", 0x10000|3, 275, 156, 260, 30, idAuto)
	a.button("&Refresh", 550, 156, 110, idRefresh)
	a.details = a.label("", 20, 195, 640, 48)
	a.label("Peers · handshake and directory activity are separate evidence", 20, 248, 640, 24)
	a.control("LISTBOX", "", 0x10000|0x800000|0x200000|1, 20, 278, 640, 100, idPeers)
	a.button("Copy selected &hostname", 20, 387, 235, idCopy)
	a.label("Join a network", 20, 432, 640, 25)
	a.label("Network &ID", 20, 470, 125, 24)
	a.networkID = a.edit(150, 466, 185, 31, false)
	a.label("Device &name", 355, 470, 110, 24)
	a.name = a.edit(470, 466, 190, 63, false)
	a.label("Enrollment &key", 20, 508, 125, 24)
	a.key = a.edit(150, 504, 510, 8192, true)
	a.joinAuto = a.control("BUTTON", "Start with &service", 0x10000|3, 150, 544, 200, 26, 0)
	a.joinNow = a.control("BUTTON", "Connect &now", 0x10000|3, 370, 544, 180, 26, 0)
	msg(a.joinAuto, 0xf1, 1, 0)
	msg(a.joinNow, 0xf1, 1, 0)
	a.button("&Join network", 20, 585, 160, idJoin)
	a.button("&Quit app", 550, 585, 110, idQuit)
	a.label("Close hides to tray. Quitting this app leaves your VPN running.", 20, 632, 640, 24)
	a.render(desktop.Result{})
}
func (a *window) selectedProfile() *profiles.Profile {
	for i := range a.profiles {
		if a.profiles[i].ID == a.selected {
			return &a.profiles[i]
		}
	}
	return nil
}
func (a *window) refresh(action desktop.Action) {
	if a.busy {
		return
	}
	a.busy = true
	a.mutating = action.Kind != ""
	a.buttons()
	hwnd := a.hwnd
	go func() {
		r := desktop.Run(context.Background(), a.backend, action)
		a.results <- r
		user.NewProc("PostMessageW").Call(hwnd, wmResult, 0, 0)
	}()
}
func (a *window) buttons() {
	p := a.selectedProfile()
	for _, id := range []int{idConnect, idDisconnect, idAuto, idJoin, idRefresh} {
		yes := !a.busy
		if id == idConnect || id == idDisconnect || id == idAuto {
			yes = yes && p != nil
		}
		if p != nil && id == idConnect {
			yes = yes && !p.Status.Tunnel.Up
		}
		b := uintptr(0)
		if yes {
			b = 1
		}
		enable.Call(a.controls[id], b)
	}
	for _, h := range []uintptr{a.networkID, a.name, a.key, a.joinAuto, a.joinNow} {
		b := uintptr(1)
		if a.mutating {
			b = 0
		}
		enable.Call(h, b)
	}
}
func (a *window) render(r desktop.Result) {
	a.profiles = r.Profiles
	msg(a.controls[idNetworks], 0x14b, 0, 0)
	selected := 0
	for i, p := range a.profiles {
		msg(a.controls[idNetworks], 0x143, 0, uintptr(unsafe.Pointer(str(p.ID))))
		if p.ID == a.selected {
			selected = i
		}
	}
	a.selected = ""
	if len(a.profiles) > 0 {
		a.selected = a.profiles[selected].ID
		msg(a.controls[idNetworks], 0x14e, uintptr(selected), 0)
	}
	a.renderProfile()
	if r.Message != "" {
		callText(a.details, r.Message)
	}
}
func (a *window) renderProfile() {
	selectedHost := ""
	n := int(int32(msg(a.controls[idPeers], 0x188, 0, 0)))
	if n >= 0 && n < len(a.peers) {
		selectedHost = a.peers[n].Hostname
	}
	msg(a.controls[idPeers], 0x184, 0, 0)
	a.peers = nil
	callText(a.status, "Service unavailable or no networks loaded")
	callText(a.details, "")
	msg(a.controls[idAuto], 0xf1, 0, 0)
	if p := a.selectedProfile(); p != nil {
		callText(a.status, desktop.State(*p))
		check := uintptr(0)
		if p.AutoConnect {
			check = 1
		}
		msg(a.controls[idAuto], 0xf1, check, 0)
		warnings := []string{}
		if p.Warning != "" || p.Status.Error != "" {
			warnings = append(warnings, "Network needs attention. Review daemon status; disconnect can retry cleanup.")
		}
		if p.Status.DNS != nil && p.Status.DNS.Error != "" {
			warnings = append(warnings, "Private DNS needs attention.")
		}
		if p.Network.Error != "" {
			warnings = append(warnings, "Directory synchronization failed; entries may be stale.")
		}
		if p.Network.Pending {
			warnings = append(warnings, "Enrollment pending.")
		}
		callText(a.details, strings.Join(warnings, " "))
		a.peers = desktop.Peers(*p, time.Now())
		for _, p := range a.peers {
			msg(a.controls[idPeers], 0x180, 0, uintptr(unsafe.Pointer(str(p.Label+"  —  "+p.Evidence))))
		}
	}
	enable.Call(a.controls[idCopy], 0)
	for i, peer := range a.peers {
		if selectedHost != "" && peer.Hostname == selectedHost {
			msg(a.controls[idPeers], 0x186, uintptr(i), 0)
			enable.Call(a.controls[idCopy], 1)
			break
		}
	}
	a.buttons()
}
func (a *window) foreground() { show.Call(a.hwnd, 9); user.NewProc("SetForegroundWindow").Call(a.hwnd) }
func (a *window) command(id int) {
	switch id {
	case idShow:
		a.foreground()
	case idQuit:
		user.NewProc("DestroyWindow").Call(a.hwnd)
	case idRefresh:
		a.refresh(desktop.Action{})
	case idConnect, idDisconnect, idAuto:
		p := a.selectedProfile()
		if p == nil || a.busy {
			return
		}
		action := desktop.Action{ID: p.ID}
		switch id {
		case idConnect:
			action.Kind = "up"
		case idDisconnect:
			action.Kind = "down"
		case idAuto:
			action.Kind = "auto"
			action.Enabled = !p.AutoConnect
		}
		a.refresh(action)
	case idJoin:
		if a.busy {
			return
		}
		r := profiles.JoinRequest{ID: strings.TrimSpace(readText(a.networkID)), Name: strings.TrimSpace(readText(a.name)), Key: strings.TrimSpace(readText(a.key)), AutoConnect: checked(a.joinAuto), Connect: checked(a.joinNow)}
		if r.ID == "" || r.Name == "" || r.Key == "" {
			callText(a.details, "Enter a network ID, device name and enrollment key.")
			return
		}
		callText(a.key, "")
		msg(a.key, 0xcd, 0, 0) // Clear both the control and its undo buffer.
		a.selected = r.ID
		a.refresh(desktop.Action{Kind: "join", Enrollment: r})
	case idCopy:
		n := int(int32(msg(a.controls[idPeers], 0x188, 0, 0)))
		if n >= 0 && n < len(a.peers) && a.peers[n].Hostname != "" {
			if !a.copy(a.peers[n].Hostname) {
				callText(a.details, "Clipboard unavailable. Try copying again.")
			}
		}
	}
}
func (a *window) copy(s string) bool {
	ok, _, _ := user.NewProc("OpenClipboard").Call(a.hwnd)
	if ok == 0 {
		return false
	}
	defer user.NewProc("CloseClipboard").Call()
	data, err := windows.UTF16FromString(s)
	if err != nil {
		return false
	}
	h, _, _ := kernel.NewProc("GlobalAlloc").Call(2, uintptr(len(data)*2))
	if h == 0 {
		return false
	}
	p, _, _ := kernel.NewProc("GlobalLock").Call(h)
	if p == 0 {
		kernel.NewProc("GlobalFree").Call(h)
		return false
	}
	// Copy into the movable native clipboard allocation without converting its
	// address into a Go pointer. Ownership transfers only on SetClipboardData.
	kernel.NewProc("RtlMoveMemory").Call(p, uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)*2))
	kernel.NewProc("GlobalUnlock").Call(h)
	user.NewProc("EmptyClipboard").Call()
	ok, _, _ = user.NewProc("SetClipboardData").Call(13, h)
	if ok == 0 {
		kernel.NewProc("GlobalFree").Call(h)
	}
	return ok != 0
}
func (a *window) addTray() {
	a.tray = notification{Window: a.hwnd, ID: 1, Flags: 7, Callback: wmTray, Icon: a.icon}
	a.tray.Size = uint32(unsafe.Sizeof(a.tray))
	copy(a.tray.Tip[:], windows.StringToUTF16("Meldnet — open networks"))
	ok, _, _ := notify.Call(0, uintptr(unsafe.Pointer(&a.tray)))
	a.trayOK = ok != 0
	if a.trayOK {
		a.tray.Version = 4
		notify.Call(4, uintptr(unsafe.Pointer(&a.tray)))
	} else {
		a.foreground()
	}
}
func (a *window) menu() {
	menu, _, _ := user.NewProc("CreatePopupMenu").Call()
	if menu == 0 {
		return
	}
	defer user.NewProc("DestroyMenu").Call(menu)
	appendItem := func(id int, label string, disabled bool) {
		flags := uintptr(0)
		if disabled {
			flags = 3
		}
		user.NewProc("AppendMenuW").Call(menu, flags, uintptr(id), uintptr(unsafe.Pointer(str(label))))
	}
	appendItem(idShow, "Open Meldnet", false)
	p := a.selectedProfile()
	if p != nil {
		appendItem(0, p.ID+" — "+desktop.State(*p), true)
		appendItem(idConnect, "Connect", a.busy || p.Status.Tunnel.Up)
		appendItem(idDisconnect, "Disconnect", a.busy)
	}
	appendItem(idRefresh, "Refresh", a.busy)
	appendItem(idQuit, "Quit app (VPN keeps running)", false)
	var pos point
	user.NewProc("GetCursorPos").Call(uintptr(unsafe.Pointer(&pos)))
	user.NewProc("SetForegroundWindow").Call(a.hwnd)
	choice, _, _ := user.NewProc("TrackPopupMenu").Call(menu, 0x100|2, uintptr(pos.X), uintptr(pos.Y), 0, a.hwnd, 0)
	user.NewProc("PostMessageW").Call(a.hwnd, 0, 0, 0)
	if choice != 0 {
		a.command(int(choice))
	}
}
func windowProc(h uintptr, m uint32, w, l uintptr) uintptr {
	a := app
	if a != nil {
		if a.taskbar != 0 && m == a.taskbar {
			a.addTray()
			return 0
		}
		switch m {
		case 0x10: // WM_CLOSE never changes daemon state.
			callText(a.key, "")
			msg(a.key, 0xcd, 0, 0)
			if a.trayOK {
				show.Call(h, 0)
			} else {
				user.NewProc("DestroyWindow").Call(h)
			}
			return 0
		case 2:
			notify.Call(2, uintptr(unsafe.Pointer(&a.tray)))
			user.NewProc("PostQuitMessage").Call(0)
			return 0
		case 0x113:
			if !a.trayOK {
				a.addTray()
			}
			a.refresh(desktop.Action{})
			return 0
		case wmResult:
			a.busy = false
			a.mutating = false
			a.render(<-a.results)
			return 0
		case wmTray:
			switch uint32(l & 0xffff) {
			case 0x202, 0x203, 0x400, 0x401:
				a.foreground()
			case 0x205, 0x7b:
				a.menu()
			}
			return 0
		case wmCommand:
			id := int(w & 0xffff)
			event := w >> 16
			if id == idNetworks && event == 1 {
				n := int(int32(msg(a.controls[idNetworks], 0x147, 0, 0)))
				if n >= 0 && n < len(a.profiles) {
					a.selected = a.profiles[n].ID
					a.renderProfile()
				}
				return 0
			}
			if id == idPeers && event == 1 {
				n := int(int32(msg(a.controls[idPeers], 0x188, 0, 0)))
				b := uintptr(0)
				if n >= 0 && n < len(a.peers) && a.peers[n].Hostname != "" {
					b = 1
				}
				enable.Call(a.controls[idCopy], b)
				return 0
			}
			if event == 0 {
				a.command(id)
			}
			return 0
		}
	}
	r, _, _ := user.NewProc("DefWindowProcW").Call(h, uintptr(m), w, l)
	return r
}

// A small code-native Meldnet monogram: white m and accent on a blue tile.
func makeIcon(instance uintptr) uintptr {
	const n = 32
	and := make([]byte, n*n/8)
	pixels := make([]byte, n*n*4)
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			i := ((n-1-y)*n + x) * 4
			pixels[i] = 190
			pixels[i+1] = 88
			pixels[i+2] = 35
			pixels[i+3] = 255
			if (y >= 11 && y <= 24 && (x >= 6 && x <= 9 || x >= 14 && x <= 17 || x >= 22 && x <= 25)) || (y >= 9 && y <= 12 && x >= 6 && x <= 25) || (y >= 4 && y <= 6 && x >= 22 && x <= 25) {
				pixels[i] = 255
				pixels[i+1] = 255
				pixels[i+2] = 255
			}
		}
	}
	icon, _, _ := user.NewProc("CreateIcon").Call(instance, n, n, 1, 32, uintptr(unsafe.Pointer(&and[0])), uintptr(unsafe.Pointer(&pixels[0])))
	return icon
}
func main() {
	runtime.LockOSThread()
	socket := flag.String("socket", api.DefaultSocket, "authorized local daemon pipe")
	tray := flag.Bool("tray", false, "start in the notification area")
	flag.Parse()
	client := api.NewClient(*socket)
	defer client.Close()
	user.NewProc("SetProcessDPIAware").Call()
	app = &window{controls: map[int]uintptr{}, backend: desktop.Local{Client: client}, results: make(chan desktop.Result, 1), dpi: 96}
	a := app
	if proc := user.NewProc("GetDpiForSystem"); proc.Find() == nil {
		dpi, _, _ := proc.Call()
		if dpi != 0 {
			a.dpi = int(dpi)
		}
	}
	a.instance, _, _ = kernel.NewProc("GetModuleHandleW").Call(0)
	a.icon = makeIcon(a.instance)
	defer user.NewProc("DestroyIcon").Call(a.icon)
	a.font, _, _ = gdi.NewProc("CreateFontW").Call(uintptr(int32(-16*a.dpi/96)), 0, 0, 0, 400, 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(str("Segoe UI"))))
	defer gdi.NewProc("DeleteObject").Call(a.font)
	className := str("MeldnetDesktop")
	cursor, _, _ := user.NewProc("LoadCursorW").Call(0, 32512)
	wc := windowClass{Proc: windows.NewCallback(windowProc), Instance: a.instance, Icon: a.icon, SmallIcon: a.icon, Cursor: cursor, Background: 16, Name: className}
	wc.Size = uint32(unsafe.Sizeof(wc))
	if ok, _, _ := user.NewProc("RegisterClassExW").Call(uintptr(unsafe.Pointer(&wc))); ok == 0 {
		startupError()
		return
	}
	a.hwnd, _, _ = createWindow.Call(0x10000, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(str("Meldnet"))), 0xca0000, 0x80000000, 0x80000000, a.scale(700), a.scale(705), 0, 0, a.instance, 0)
	if a.hwnd == 0 {
		startupError()
		return
	}
	a.build()
	taskbar, _, _ := user.NewProc("RegisterWindowMessageW").Call(uintptr(unsafe.Pointer(str("TaskbarCreated"))))
	a.taskbar = uint32(taskbar)
	a.addTray()
	if !*tray || !a.trayOK {
		a.foreground()
	}
	user.NewProc("SetTimer").Call(a.hwnd, 1, 3000, 0)
	a.refresh(desktop.Action{})
	var m message
	for {
		ret, _, _ := user.NewProc("GetMessageW").Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(ret) <= 0 {
			break
		}
		handled, _, _ := user.NewProc("IsDialogMessageW").Call(a.hwnd, uintptr(unsafe.Pointer(&m)))
		if handled == 0 {
			user.NewProc("TranslateMessage").Call(uintptr(unsafe.Pointer(&m)))
			user.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&m)))
		}
	}
}

func startupError() {
	user.NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(str("Meldnet could not create its desktop window. The VPN service is unaffected."))), uintptr(unsafe.Pointer(str("Meldnet"))), 0x10)
}

// Both supported targets use the Windows LLP64 ABI. Fail the build on layout drift.
var _ [976]byte = [unsafe.Sizeof(notification{})]byte{}
var _ [80]byte = [unsafe.Sizeof(windowClass{})]byte{}
var _ [48]byte = [unsafe.Sizeof(message{})]byte{}
