package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	dwmapi   = windows.NewLazySystemDLL("dwmapi.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
	procGetWindowThreadProcessID = user32.NewProc("GetWindowThreadProcessId")
	procAttachThreadInput        = user32.NewProc("AttachThreadInput")
	procBringWindowToTop         = user32.NewProc("BringWindowToTop")
	procGetCurrentThreadID       = kernel32.NewProc("GetCurrentThreadId")

	procSetWindowLongPtrW   = user32.NewProc("SetWindowLongPtrW")
	procCallWindowProcW     = user32.NewProc("CallWindowProcW")
	procDefWindowProcW      = user32.NewProc("DefWindowProcW")
	procGetDpiForWindow     = user32.NewProc("GetDpiForWindow")
	procShowWindow          = user32.NewProc("ShowWindow")
	procIsWindowVisible     = user32.NewProc("IsWindowVisible")
	procSetWindowPos        = user32.NewProc("SetWindowPos")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procGetWindowRect       = user32.NewProc("GetWindowRect")
	procGetSystemMetrics    = user32.NewProc("GetSystemMetrics")
	procSendMessageW        = user32.NewProc("SendMessageW")
	procLoadImageW          = user32.NewProc("LoadImageW")
	procFindWindowW         = user32.NewProc("FindWindowW")
	procIsZoomed            = user32.NewProc("IsZoomed")
	procIsIconic            = user32.NewProc("IsIconic")
	procSetWindowTextW      = user32.NewProc("SetWindowTextW")
	procMessageBoxW         = user32.NewProc("MessageBoxW")
	procGetWindowPlacement  = user32.NewProc("GetWindowPlacement")
	procSetWindowPlacement  = user32.NewProc("SetWindowPlacement")
	procFlashWindowEx       = user32.NewProc("FlashWindowEx")

	procDwmSetWindowAttribute = dwmapi.NewProc("DwmSetWindowAttribute")
	procSetAppUserModelID     = shell32.NewProc("SetCurrentProcessExplicitAppUserModelID")
)

const (
	gwlpWndProc = ^uintptr(3) // GWLP_WNDPROC == -4

	swHide     = 0
	swShow     = 5
	swMinimize = 6
	swRestore  = 9
	swShowNa   = 8

	swpNoSize     = 0x0001
	swpNoMove     = 0x0002
	swpNoZOrder   = 0x0004
	swpNoActivate = 0x0010

	hwndTopMost    = ^uintptr(0) // (HWND)-1
	hwndNotTopMost = ^uintptr(1) // (HWND)-2

	wmSetIcon = 0x0080
	iconSmall = 0
	iconBig   = 1

	imageIcon       = 1
	loadFromFile    = 0x0010
	smCxIcon        = 11
	smCyIcon        = 12
	smCxSmIcon      = 49
	smCySmIcon      = 50
	wmClose         = 0x0010
	wmQueryEnd      = 0x0011
	wmEndSession    = 0x0016
	wmSysCommand    = 0x0112
	scMinimize      = 0xF020
	wmGetMinMaxInfo = 0x0024

	flashwStop = 0x00000000
)

type winRect struct {
	Left, Top, Right, Bottom int32
}

type minMaxInfo struct {
	PtReserved     [2]int32
	PtMaxSize      [2]int32
	PtMaxPosition  [2]int32
	PtMinTrackSize [2]int32
	PtMaxTrackSize [2]int32
}

type windowPlacement struct {
	Length           uint32
	Flags            uint32
	ShowCmd          uint32
	PtMinPosition    [2]int32
	PtMaxPosition    [2]int32
	RcNormalPosition winRect
}

func setWindowLongPtr(hwnd uintptr, index uintptr, value uintptr) uintptr {
	r, _, _ := procSetWindowLongPtrW.Call(hwnd, index, value)
	return r
}

func callWindowProc(prev uintptr, hwnd, msg, wp, lp uintptr) uintptr {
	if prev == 0 {
		r, _, _ := procDefWindowProcW.Call(hwnd, msg, wp, lp)
		return r
	}
	r, _, _ := procCallWindowProcW.Call(prev, hwnd, msg, wp, lp)
	return r
}

func windowScale(hwnd uintptr) float64 {
	r, _, _ := procGetDpiForWindow.Call(hwnd)
	if r < 72 {
		return 1
	}
	return float64(r) / 96
}

func showWindow(hwnd uintptr, cmd int) bool {
	r, _, _ := procShowWindow.Call(hwnd, uintptr(cmd))
	return r != 0
}

func isWindowVisible(hwnd uintptr) bool {
	r, _, _ := procIsWindowVisible.Call(hwnd)
	return r != 0
}

func hideWindow(hwnd uintptr) {
	showWindow(hwnd, swHide)
}

func showAndFocus(hwnd uintptr) {
	if hwnd == 0 {
		return
	}
	if isMinimized(hwnd) {
		showWindow(hwnd, swRestore)
	} else {
		showWindow(hwnd, swShow)
	}
	fg, _, _ := procGetForegroundWindow.Call()
	cur, _, _ := procGetCurrentThreadID.Call()
	var pid uint32
	fgThread, _, _ := procGetWindowThreadProcessID.Call(fg, uintptr(unsafe.Pointer(&pid)))
	if fg != 0 && fgThread != 0 && fgThread != cur {
		_, _, _ = procAttachThreadInput.Call(cur, fgThread, 1)
		_, _, _ = procBringWindowToTop.Call(hwnd)
		_, _, _ = procSetForegroundWindow.Call(hwnd)
		_, _, _ = procAttachThreadInput.Call(cur, fgThread, 0)
	} else {
		_, _, _ = procBringWindowToTop.Call(hwnd)
		_, _, _ = procSetForegroundWindow.Call(hwnd)
	}
}

func isMinimized(hwnd uintptr) bool {
	r, _, _ := procIsIconic.Call(hwnd)
	return r != 0
}

func isMaximized(hwnd uintptr) bool {
	r, _, _ := procIsZoomed.Call(hwnd)
	return r != 0
}

func moveWindow(hwnd uintptr, x, y, w, h int) {
	_, _, _ = procSetWindowPos.Call(hwnd, 0, uintptr(int32(x)), uintptr(int32(y)), uintptr(int32(w)), uintptr(int32(h)), swpNoZOrder|swpNoActivate)
}

func applyTopMost(hwnd uintptr, on bool) {
	insertAfter := hwndNotTopMost
	if on {
		insertAfter = hwndTopMost
	}
	_, _, _ = procSetWindowPos.Call(hwnd, insertAfter, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate)
}

func getWindowRect(hwnd uintptr) (WindowRect, bool) {
	var r winRect
	ok, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	if ok == 0 {
		return WindowRect{}, false
	}
	return WindowRect{X: int(r.Left), Y: int(r.Top), W: int(r.Right - r.Left), H: int(r.Bottom - r.Top)}, true
}

func screenSize() (int, int) {
	w, _, _ := procGetSystemMetrics.Call(0)
	h, _, _ := procGetSystemMetrics.Call(1)
	return int(w), int(h)
}

func sendMessage(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
	r, _, _ := procSendMessageW.Call(hwnd, uintptr(msg), wp, lp)
	return r
}

func loadImage(path string, cx, cy int) uintptr {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0
	}
	h, _, _ := procLoadImageW.Call(0, uintptr(unsafe.Pointer(p)), imageIcon, uintptr(cx), uintptr(cy), loadFromFile)
	return h
}

func setWindowIcon(hwnd uintptr, path string) {
	if hwnd == 0 || path == "" {
		return
	}
	cx, _, _ := procGetSystemMetrics.Call(smCxIcon)
	cy, _, _ := procGetSystemMetrics.Call(smCyIcon)
	scx, _, _ := procGetSystemMetrics.Call(smCxSmIcon)
	scy, _, _ := procGetSystemMetrics.Call(smCySmIcon)
	big := loadImage(path, int(cx), int(cy))
	small := loadImage(path, int(scx), int(scy))
	if big != 0 {
		sendMessage(hwnd, wmSetIcon, iconBig, big)
	}
	if small != 0 {
		sendMessage(hwnd, wmSetIcon, iconSmall, small)
	}
}

func setLightTitleBar(hwnd uintptr) {
	v := int32(0)
	// Force the immersive light title bar even when Windows is in dark mode.
	// DWMWA_USE_IMMERSIVE_DARK_MODE: 20 on current Windows 10/11, 19 on older.
	hr, _, _ := procDwmSetWindowAttribute.Call(hwnd, 20, uintptr(unsafe.Pointer(&v)), unsafe.Sizeof(v))
	if int32(hr) != 0 {
		_, _, _ = procDwmSetWindowAttribute.Call(hwnd, 19, uintptr(unsafe.Pointer(&v)), unsafe.Sizeof(v))
	}
	// DWMWA_WINDOW_CORNER_PREFERENCE = 33; DWMWCP_ROUND = 2.
	corner := int32(2)
	_, _, _ = procDwmSetWindowAttribute.Call(hwnd, 33, uintptr(unsafe.Pointer(&corner)), unsafe.Sizeof(corner))
}

func setAppUserModelID(id string) {
	p, err := windows.UTF16PtrFromString(id)
	if err != nil {
		return
	}
	_, _, _ = procSetAppUserModelID.Call(uintptr(unsafe.Pointer(p)))
}

func setWindowTitle(hwnd uintptr, title string) {
	p, err := windows.UTF16PtrFromString(title)
	if err != nil {
		return
	}
	_, _, _ = procSetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(p)))
}

func findWindowByTitle(title string) uintptr {
	p, err := windows.UTF16PtrFromString(title)
	if err != nil {
		return 0
	}
	h, _, _ := procFindWindowW.Call(0, uintptr(unsafe.Pointer(p)))
	return h
}

func messageBox(title, text string) {
	t, _ := windows.UTF16PtrFromString(title)
	m, _ := windows.UTF16PtrFromString(text)
	_, _, _ = procMessageBoxW.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), 0x10)
}

// acquireSingleInstance returns true when this process owns the named mutex.
// When another instance exists, it brings that window forward and returns false.
func acquireSingleInstance() (windows.Handle, bool) {
	name, _ := windows.UTF16PtrFromString("Local\\TodoListAgent.SingleInstance")
	h, err := windows.CreateMutex(nil, false, name)
	if err == windows.ERROR_ALREADY_EXISTS {
		if hwnd := findWindowByTitle(appName); hwnd != 0 {
			showAndFocus(hwnd)
		}
		if h != 0 {
			_ = windows.CloseHandle(h)
		}
		return 0, false
	}
	return h, true
}

func clampWindowRect(hwnd uintptr, r WindowRect) WindowRect {
	sw, sh := screenSize()
	scale := windowScale(hwnd)
	if r.W <= 0 || r.H <= 0 {
		r.W = int(460 * scale)
		r.H = int(800 * scale)
	}
	minW := int(380 * scale)
	minH := int(540 * scale)
	if r.W < minW {
		r.W = minW
	}
	if r.H < minH {
		r.H = minH
	}
	maxH := sh - 70
	if maxH < minH {
		maxH = sh
	}
	if sw > 0 && r.W > sw {
		r.W = sw
	}
	if maxH > 0 && r.H > maxH {
		r.H = maxH
	}
	if r.X == -9999 && r.Y == -9999 {
		r.X = (sw - r.W) / 2
		r.Y = (sh - r.H) / 2
	}
	if sw > 0 && r.X+r.W > sw-10 {
		r.X = sw - r.W - 10
	}
	if r.X < 10 {
		r.X = 10
	}
	if r.Y+r.H > sh-30 {
		r.Y = sh - r.H - 30
	}
	if r.Y < 0 {
		r.Y = 0
	}
	return r
}

func (a *App) windowProc(hwnd, msg, wp, lp uintptr) uintptr {
	if t := a.tray; t != nil {
		switch {
		case t.taskbarCreated != 0 && msg == t.taskbarCreated:
			t.add()
			return 0
		case msg == wmTrayIcon && wp == uintptr(t.data.ID):
			switch lp {
			case wmLeftDoubleClick:
				a.showMainWindow()
			case wmRightButtonUp:
				a.trayMenu()
			}
			return 0
		}
	}
	switch msg {
	case wmClose:
		a.mu.Lock()
		quitting := a.quitting
		a.mu.Unlock()
		if !quitting {
			a.saveWindowState()
			hideWindow(hwnd)
			return 0
		}
	case wmQueryEnd:
		a.saveWindowState()
		a.saveNow()
		return 1
	case wmEndSession:
		if wp != 0 {
			a.saveWindowState()
			a.saveNow()
		}
	case wmGetMinMaxInfo:
		mmi := (*minMaxInfo)(unsafe.Pointer(lp))
		scale := windowScale(hwnd)
		mmi.PtMinTrackSize[0] = int32(380 * scale)
		mmi.PtMinTrackSize[1] = int32(540 * scale)
		return 0
	}
	return callWindowProc(a.oldProc, hwnd, msg, wp, lp)
}

func (a *App) saveWindowState() {
	hwnd := a.hwnd
	if hwnd == 0 {
		return
	}
	if isMinimized(hwnd) || isMaximized(hwnd) {
		return
	}
	r, ok := getWindowRect(hwnd)
	if !ok || r.W <= 0 || r.H <= 0 {
		return
	}
	a.mu.Lock()
	a.state.Prefs.Window = r
	a.mu.Unlock()
	a.markDirty()
}

func (a *App) applySavedWindowRect() {
	hwnd := a.hwnd
	if hwnd == 0 {
		return
	}
	a.mu.Lock()
	r := a.state.Prefs.Window
	a.mu.Unlock()
	r = clampWindowRect(hwnd, r)
	moveWindow(hwnd, r.X, r.Y, r.W, r.H)
}

func (a *App) restoreWindowProc() {
	if a.hwnd == 0 || a.oldProc == 0 {
		return
	}
	setWindowLongPtr(a.hwnd, gwlpWndProc, a.oldProc)
	a.oldProc = 0
}

func (a *App) installWindowProc() {
	hwnd := a.hwnd
	if hwnd == 0 {
		return
	}
	a.wndProc = windows.NewCallback(a.windowProc)
	old := setWindowLongPtr(hwnd, gwlpWndProc, a.wndProc)
	if old != 0 {
		a.oldProc = old
	}
}
