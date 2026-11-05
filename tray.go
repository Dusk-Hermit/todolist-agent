package main

import (
	"log"
	"sync"
	"unsafe"

	"github.com/getlantern/systray"
	"golang.org/x/sys/windows"
)

type trayCtx struct {
	app *App

	mu    sync.Mutex
	pin   *systray.MenuItem
	ready bool
	once  sync.Once
}

func startTray(a *App) {
	t := &trayCtx{app: a}
	a.tray = t
	go systray.Run(t.onReady, func() {
		t.mu.Lock()
		t.ready = false
		t.mu.Unlock()
	})
}

func (t *trayCtx) onReady() {
	systray.SetIcon(iconICO)
	systray.SetTitle("")
	systray.SetTooltip(appName + " · DSH 联动")

	show := systray.AddMenuItem("显示清单", "打开 TodoList Agent")
	add := systray.AddMenuItem("新建任务", "快速添加一个本地任务")
	systray.AddSeparator()
	pin := systray.AddMenuItemCheckbox("窗口置顶", "让清单保持在其他窗口之上", t.app.getAlwaysOnTop())
	openDSH := systray.AddMenuItem("打开 DeepSeek Harness", "在浏览器中打开当前 DSH 会话")
	systray.AddSeparator()
	quit := systray.AddMenuItem("退出", "退出 TodoList Agent")

	t.mu.Lock()
	t.pin = pin
	t.ready = true
	t.mu.Unlock()

	go func() {
		for range show.ClickedCh {
			t.app.showMainWindow()
		}
	}()
	go func() {
		for range add.ClickedCh {
			t.app.showMainWindow()
			t.app.emit("new-task", nil)
		}
	}()
	go func() {
		for range pin.ClickedCh {
			on := t.app.toggleAlwaysOnTop()
			if on {
				pin.Check()
			} else {
				pin.Uncheck()
			}
			t.app.toast("info", map[bool]string{true: "窗口已置顶", false: "已取消置顶"}[on])
		}
	}()
	go func() {
		for range openDSH.ClickedCh {
			base := t.app.getStatus().BaseURL
			if base == "" {
				t.app.showMainWindow()
				t.app.toast("error", "还没有连接到 DSH")
				continue
			}
			openURL(base)
		}
	}()
	go func() {
		for range quit.ClickedCh {
			t.app.quitApp()
		}
	}()
	log.Printf("tray ready")
}

func (t *trayCtx) setPin(on bool) {
	t.mu.Lock()
	pin := t.pin
	ready := t.ready
	t.mu.Unlock()
	if !ready || pin == nil {
		return
	}
	if on {
		pin.Check()
	} else {
		pin.Uncheck()
	}
}

func (t *trayCtx) quit() {
	t.once.Do(func() {
		systray.Quit()
	})
}

func (a *App) getAlwaysOnTop() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state.Prefs.AlwaysOnTop
}

var procShellExecuteW = shell32.NewProc("ShellExecuteW")

func openURL(u string) {
	verb, _ := windows.UTF16PtrFromString("open")
	target, err := windows.UTF16PtrFromString(u)
	if err != nil {
		return
	}
	_, _, _ = procShellExecuteW.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(target)), 0, 0, 1)
}
