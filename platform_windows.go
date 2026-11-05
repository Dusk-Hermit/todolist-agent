package main

import (
	"bytes"
	"log"
	"os"
	"path/filepath"

	webview2 "github.com/jchv/go-webview2"
)

const platformName = "windows"

type platformState struct {
	startHidden bool
	pageReady   bool

	web      webview2.WebView
	hwnd     uintptr
	oldProc  uintptr
	wndProc  uintptr
	tray     *trayCtx
	iconPath string
}

// Start loads persisted state and starts the bridge plus the DSH poller.
func (a *App) Start() error {
	dir, err := dataDir()
	if err != nil {
		return err
	}
	a.dataPath = filepath.Join(dir, "data.json")
	if err := a.load(); err != nil {
		log.Printf("state load failed, starting fresh: %v", err)
	}
	a.iconPath = filepath.Join(dir, "icon.ico")
	if old, err := os.ReadFile(a.iconPath); err != nil || !bytes.Equal(old, iconICO) {
		if err := os.WriteFile(a.iconPath, iconICO, 0o644); err != nil {
			log.Printf("write icon: %v", err)
		}
	}

	a.bridge = newBridge(a)
	if err := a.bridge.Start(); err != nil {
		log.Printf("bridge start: %v", err)
	}
	a.dsh = newDSHClient(a)
	a.dsh.Start()
	a.startSavedLAN()
	return nil
}

func (a *App) dispatch(script string) {
	a.mu.Lock()
	w, ready := a.web, a.pageReady
	a.mu.Unlock()
	if w == nil || !ready {
		return
	}

	w.Dispatch(func() { w.Eval(script) })
}
