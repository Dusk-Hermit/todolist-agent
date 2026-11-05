package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"

	"golang.org/x/sys/windows"
)

var instanceMutex windows.Handle

func main() {
	var hidden bool
	flag.BoolVar(&hidden, "hidden", false, "start minimized to the notification area")
	flag.Parse()

	initLogging()

	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic: %v\n%s", r, debug.Stack())
			messageBox(appName, fmt.Sprintf("TodoList Agent 遇到错误：\n%v", r))
		}
	}()

	h, ok := acquireSingleInstance()
	if !ok {
		return
	}
	instanceMutex = h
	defer func() {
		if instanceMutex != 0 {
			_ = windows.CloseHandle(instanceMutex)
		}
	}()

	setAppUserModelID(appID)

	app := NewApp()
	app.startHidden = hidden
	if err := app.Start(); err != nil {
		log.Printf("start: %v", err)
		messageBox(appName, "启动失败："+err.Error())
		return
	}

	w := createMainWindow(app)
	if w == nil {
		messageBox(appName, "无法启动 WebView2 界面。\n\n请确认系统已安装 Microsoft Edge WebView2 Runtime。")
		return
	}
	startTray(app)
	w.Run()
	app.Shutdown()
}

func initLogging() {
	dir, err := dataDir()
	if err != nil {
		return
	}
	path := filepath.Join(dir, "app.log")
	if st, err := os.Stat(path); err == nil && st.Size() > 4<<20 {
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	log.SetOutput(f)
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.Printf("---- %s %s starting (pid %d) ----", appName, appVersion, os.Getpid())
}
