package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	webview2 "github.com/jchv/go-webview2"
)

func createMainWindow(a *App) webview2.WebView {
	dir, _ := dataDir()
	dataPath := ""
	if dir != "" {
		dataPath = filepath.Join(dir, "WebView2")
		_ = os.MkdirAll(dataPath, 0o755)
	}
	debug := os.Getenv("TODOLIST_AGENT_DEBUG") == "1"
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     debug,
		AutoFocus: true,
		DataPath:  dataPath,
		WindowOptions: webview2.WindowOptions{
			Title:  appName,
			Width:  460,
			Height: 800,
			Center: true,
		},
	})
	if w == nil {
		return nil
	}
	a.mu.Lock()
	a.web = w
	a.mu.Unlock()
	a.hwnd = uintptr(w.Window())
	hideWindow(a.hwnd)

	scale := windowScale(a.hwnd)
	w.SetSize(int(460*scale), int(800*scale), webview2.HintNone)
	w.SetSize(int(380*scale), int(600*scale), webview2.HintMin)
	a.applySavedWindowRect()
	a.installWindowProc()
	a.installWindowChrome()

	bindings := map[string]any{
		"todo_ready":           a.bindingReady,
		"todo_getState":        a.bindingGetState,
		"todo_sendInput":       a.bindingSendInput,
		"todo_addTodo":         a.bindingAddTodo,
		"todo_updateTodo":      a.bindingUpdateTodo,
		"todo_deleteTodo":      a.bindingDeleteTodo,
		"todo_setTodoStatus":   a.bindingSetTodoStatus,
		"todo_moveTodo":        a.bindingMoveTodo,
		"todo_setFocus":        a.bindingSetFocus,
		"todo_journal":         a.bindingJournal,
		"todo_savePrefs":       a.bindingSavePrefs,
		"todo_setAlwaysOnTop":  a.bindingSetAlwaysOnTop,
		"todo_windowAction":    a.bindingWindowAction,
		"todo_connectDSH":      a.bindingConnectDSH,
		"todo_refreshSessions": a.bindingRefreshSessions,
		"todo_recheckPrompts":  a.bindingRecheckPrompts,
		"todo_selectSession":   a.bindingSelectSession,
		"todo_newSession":      a.bindingNewSession,
		"todo_sendOnboarding":  a.bindingSendOnboarding,
		"todo_writeAgents":     a.bindingWriteAgents,
		"todo_diagnostics":     a.bindingDiagnostics,
		"todo_quit":            a.bindingQuit,
		"todo_lanState":        a.bindingLANState,
		"todo_lanConfigure":    a.bindingLANConfigure,
		"todo_lanRefresh":      a.bindingLANRefresh,
		"todo_updateState":     a.bindingUpdateState,
		"todo_updateCheck":     a.bindingUpdateCheck,
		"todo_updateInstall":   a.bindingUpdateInstall,
		"todo_openUpdatePage":  func() { openURL(updateReleasePage) },
	}
	for name, fn := range bindings {
		if err := w.Bind(name, fn); err != nil {
			log.Printf("bind %s: %v", name, err)
		}
	}
	html, err := buildHTML()
	if err != nil {
		log.Printf("build html: %v", err)
		html = "<html><body style='background:#0b0d12;color:#fff;font-family:sans-serif;padding:32px'>UI assets missing.</body></html>"
	}
	w.SetHtml(html)
	// Safety net: if the page never reports ready, still show the window.
	time.AfterFunc(4*time.Second, func() {
		a.mu.Lock()
		ready := a.pageReady
		hidden := a.startHidden
		a.mu.Unlock()
		if !ready && !hidden {
			a.showMainWindow()
		}
	})
	return w
}

func (a *App) installWindowChrome() {
	if a.hwnd == 0 {
		return
	}
	setWindowTitle(a.hwnd, appName)
	setWindowIcon(a.hwnd, a.iconPath)
	setLightTitleBar(a.hwnd)
	a.mu.Lock()
	onTop := a.state.Prefs.AlwaysOnTop
	a.mu.Unlock()
	applyTopMost(a.hwnd, onTop)
}

func (a *App) showMainWindow() {
	if a.hwnd == 0 {
		return
	}
	showAndFocus(a.hwnd)
}

func (a *App) hideMainWindow() {
	if a.hwnd == 0 {
		return
	}
	a.saveWindowState()
	hideWindow(a.hwnd)
}

func (a *App) setAlwaysOnTop(on bool) {
	a.mu.Lock()
	a.state.Prefs.AlwaysOnTop = on
	a.mu.Unlock()
	if a.hwnd != 0 {
		applyTopMost(a.hwnd, on)
	}
	a.markDirty()
	a.emitState()
}

func (a *App) toggleAlwaysOnTop() bool {
	a.mu.Lock()
	on := !a.state.Prefs.AlwaysOnTop
	a.mu.Unlock()
	a.setAlwaysOnTop(on)
	return on
}

func (a *App) quitApp() {
	a.mu.Lock()
	if a.quitting {
		a.mu.Unlock()
		return
	}
	a.mu.Unlock()
	a.saveWindowState()
	a.Shutdown()
	if a.web != nil {
		w := a.web
		w.Dispatch(func() {
			if a.tray != nil {
				a.tray.close()
				a.tray = nil
			}
			a.restoreWindowProc()
			w.Destroy()
		})
	}
}

// ---- bindings -------------------------------------------------------------

func (a *App) bindingReady() (StatePayload, error) {
	a.mu.Lock()
	a.pageReady = true
	hidden := a.startHidden
	a.mu.Unlock()
	if !hidden {
		a.showMainWindow()
	}
	// Let the first paint settle before revealing the window.
	time.AfterFunc(120*time.Millisecond, func() {
		a.emitState()
		a.emitStatus()
	})
	return a.snapshot(), nil
}

func (a *App) bindingSavePrefs(p Prefs) map[string]any {
	a.setPrefs(p)
	if a.hwnd != 0 {
		a.mu.Lock()
		on := a.state.Prefs.AlwaysOnTop
		a.mu.Unlock()
		applyTopMost(a.hwnd, on)
	}
	a.emitState()
	return map[string]any{"ok": true}
}

func (a *App) bindingSetAlwaysOnTop(on bool) map[string]any {
	a.setAlwaysOnTop(on)
	return map[string]any{"ok": true, "alwaysOnTop": on}
}

func (a *App) bindingWindowAction(action string) map[string]any {
	switch action {
	case "minimize":
		if a.hwnd != 0 {
			showWindow(a.hwnd, swMinimize)
		}
	case "hide", "close":
		a.hideMainWindow()
	case "show":
		a.showMainWindow()
	case "quit":
		a.quitApp()
	}
	return map[string]any{"ok": true}
}

func (a *App) bindingConnectDSH(sessionID string) map[string]any {
	if a.dsh == nil {
		return map[string]any{"ok": false, "error": "DSH 客户端尚未启动"}
	}
	a.dsh.SelectSession(sessionID)
	a.dsh.Wake()
	return map[string]any{"ok": true}
}

func (a *App) bindingRefreshSessions() []SessionOption {
	if a.dsh != nil {
		a.dsh.Wake()
	}
	return a.getSessions()
}

func (a *App) bindingRecheckPrompts() {
	if a.dsh != nil {
		a.dsh.Wake()
	}
}

func (a *App) bindingSelectSession(id string) map[string]any {
	if a.dsh == nil {
		return map[string]any{"ok": false, "error": "DSH 客户端尚未启动"}
	}
	a.dsh.SelectSession(id)
	return map[string]any{"ok": true}
}

func (a *App) bindingNewSession() map[string]any {
	if a.dsh == nil {
		return map[string]any{"ok": false, "error": "DSH 客户端尚未启动"}
	}
	a.dsh.NewDedicatedSession()
	return map[string]any{"ok": true}
}

func (a *App) bindingSendOnboarding() (map[string]any, error) {
	if a.dsh == nil {
		return nil, fmt.Errorf("DSH 客户端尚未启动")
	}
	text := a.dsh.OnboardingText()
	id, err := a.dsh.SendPrompt(text, true)
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "id": id}, nil
}

func (a *App) bindingWriteAgents() map[string]any {
	path, err := a.writeAgentsBlock()
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	a.toast("success", "已写入 "+path)
	return map[string]any{"ok": true, "path": path}
}

func (a *App) bindingDiagnostics() string {
	snap := a.snapshot()
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\n", appName, appVersion)
	fmt.Fprintf(&b, "data: %s\n", snap.DataPath)
	fmt.Fprintf(&b, "dsh: connected=%v base=%s session=%s\n", snap.DSH.Connected, snap.DSH.BaseURL, snap.DSH.SessionID)
	fmt.Fprintf(&b, "dsh title: %s\n", snap.DSH.SessionTitle)
	fmt.Fprintf(&b, "dsh cwd: %s\n", snap.DSH.CWD)
	fmt.Fprintf(&b, "dsh error: %s\n", snap.DSH.Error)
	fmt.Fprintf(&b, "bridge: %s\n", snap.Bridge.URL)
	fmt.Fprintf(&b, "bridge config: %s\n", snap.Bridge.ConfigPath)
	fmt.Fprintf(&b, "todos: %d, events: %d, messages: %d\n", len(snap.Todos), len(snap.Events), len(snap.Messages))
	return b.String()
}

func (a *App) bindingQuit() map[string]any {
	a.quitApp()
	return map[string]any{"ok": true}
}
