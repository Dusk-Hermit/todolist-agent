package main

import (
	"log"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	wmTrayIcon        = 0x8001 // WM_APP + 1; WebView2 uses WM_APP for dispatch.
	wmLeftDoubleClick = 0x0203
	wmRightButtonUp   = 0x0205
)

const (
	trayShow = iota + 1
	trayAdd
	trayPin
	trayOpenDSH
	trayExit
)

const (
	nimAdd      = 0
	nimDelete   = 2
	nimSetFocus = 3
	nifMessage  = 1
	nifIcon     = 2
	nifTip      = 4
)

var (
	procShellNotifyIconW       = shell32.NewProc("Shell_NotifyIconW")
	procRegisterWindowMessageW = user32.NewProc("RegisterWindowMessageW")
	procCreatePopupMenu        = user32.NewProc("CreatePopupMenu")
	procAppendMenuW            = user32.NewProc("AppendMenuW")
	procDestroyMenu            = user32.NewProc("DestroyMenu")
	procTrackPopupMenu         = user32.NewProc("TrackPopupMenu")
	procGetCursorPos           = user32.NewProc("GetCursorPos")
	procPostMessageW           = user32.NewProc("PostMessageW")
	procDestroyIcon            = user32.NewProc("DestroyIcon")
	procShellExecuteW          = shell32.NewProc("ShellExecuteW")
)

type notifyIconData struct {
	Size                uint32
	Window              uintptr
	ID, Flags, Callback uint32
	Icon                uintptr
	Tip                 [128]uint16
	State, StateMask    uint32
	Info                [256]uint16
	Version             uint32
	InfoTitle           [64]uint16
	InfoFlags           uint32
	Guid                windows.GUID
	BalloonIcon         uintptr
}

// The main HWND owns the notification icon and processes callbacks on
// WebView2's existing Windows message loop, even while the window is hidden.
type trayCtx struct {
	data           notifyIconData
	taskbarCreated uintptr
}

func startTray(a *App) {
	name, _ := windows.UTF16PtrFromString("TaskbarCreated")
	event, _, _ := procRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(name)))
	t := &trayCtx{taskbarCreated: event}
	t.data = notifyIconData{
		Size: uint32(unsafe.Sizeof(t.data)), Window: a.hwnd, ID: 1,
		Flags: nifMessage | nifIcon | nifTip, Callback: wmTrayIcon,
		Icon: loadImage(a.iconPath, 0, 0),
	}
	tip, _ := windows.UTF16FromString(appName + " · DSH 联动")
	copy(t.data.Tip[:], tip)
	a.tray = t
	t.add()
}

func (t *trayCtx) notify(action uintptr) (bool, error) {
	ok, _, err := procShellNotifyIconW.Call(action, uintptr(unsafe.Pointer(&t.data)))
	return ok != 0, err
}

func (t *trayCtx) add() {
	if ok, err := t.notify(nimAdd); !ok {
		log.Printf("register tray icon: %v", err)
	}
}

func (t *trayCtx) close() {
	_, _ = t.notify(nimDelete)
	if t.data.Icon != 0 {
		procDestroyIcon.Call(t.data.Icon)
	}
}

func (a *App) trayMenu() {
	const (
		mfChecked      = 0x0008
		mfSeparator    = 0x0800
		tpmRightButton = 0x0002
		tpmBottomAlign = 0x0020
		tpmReturnCmd   = 0x0100
	)
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)

	appendItem := func(id, flags uintptr, title string) {
		name, _ := windows.UTF16PtrFromString(title)
		procAppendMenuW.Call(menu, flags, id, uintptr(unsafe.Pointer(name)))
	}
	appendItem(trayShow, 0, "显示清单")
	appendItem(trayAdd, 0, "新建任务")
	procAppendMenuW.Call(menu, mfSeparator, 0, 0)
	var pin uintptr
	if a.getAlwaysOnTop() {
		pin = mfChecked
	}
	appendItem(trayPin, pin, "窗口置顶")
	appendItem(trayOpenDSH, 0, "打开 DeepSeek Harness")
	procAppendMenuW.Call(menu, mfSeparator, 0, 0)
	appendItem(trayExit, 0, "退出")

	var pos [2]int32
	if ok, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&pos))); ok == 0 {
		return
	}
	procSetForegroundWindow.Call(a.hwnd)
	command, _, _ := procTrackPopupMenu.Call(menu, tpmRightButton|tpmBottomAlign|tpmReturnCmd,
		uintptr(pos[0]), uintptr(pos[1]), 0, a.hwnd, 0)
	// Required by TrackPopupMenu for successive notification-area menus.
	procPostMessageW.Call(a.hwnd, 0, 0, 0)
	switch command {
	case 0:
		if a.tray != nil {
			_, _ = a.tray.notify(nimSetFocus)
		}
	case trayShow:
		a.showMainWindow()
	case trayAdd:
		a.showMainWindow()
		a.emit("new-task", nil)
	case trayPin:
		on := a.toggleAlwaysOnTop()
		a.toast("info", map[bool]string{true: "窗口已置顶", false: "已取消置顶"}[on])
	case trayOpenDSH:
		base := a.getStatus().BaseURL
		if base == "" {
			a.showMainWindow()
			a.toast("error", "还没有连接到 DSH")
			return
		}
		openURL(base)
	case trayExit:
		a.quitApp()
	}
}

func (a *App) getAlwaysOnTop() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state.Prefs.AlwaysOnTop
}

func openURL(u string) {
	verb, _ := windows.UTF16PtrFromString("open")
	target, err := windows.UTF16PtrFromString(u)
	if err != nil {
		return
	}
	procShellExecuteW.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(target)), 0, 0, 1)
}
