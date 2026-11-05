package main

/*
#include <stdlib.h>
*/
import "C"

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
)

const platformName = "android"

type platformState struct {
	dispatchMu sync.Mutex
	events     []string
}

//go:embed web/android.js
var androidJS string

var phoneApp *App
var openPhone sync.Once

func main() {}

func (a *App) dispatch(script string) {
	a.dispatchMu.Lock()
	a.events = append(a.events, script)
	a.dispatchMu.Unlock()
}

//export TodoOpen
func TodoOpen(dir *C.char) *C.char {
	openPhone.Do(func() {
		a := NewApp()
		a.dataPath = filepath.Join(C.GoString(dir), "data.json")
		if err := a.load(); err != nil {
			log.Printf("load: %v", err)
		}
		phoneApp = a
	})
	html, err := buildHTML()
	if err != nil {
		return C.CString("<p>TodoList interface could not be loaded.</p>")
	}
	html = strings.Replace(html, "</head>", "<script>"+androidJS+"</script></head>", 1)
	return C.CString(html)
}

//export TodoForeground
func TodoForeground(foreground C.int) {
	if phoneApp == nil {
		return
	}
	if foreground != 0 {
		phoneApp.startSavedLAN()
	} else {
		phoneApp.stopLAN()
		phoneApp.saveNow()
		phoneApp.dispatchMu.Lock()
		phoneApp.events = nil
		phoneApp.dispatchMu.Unlock()
	}
}

//export TodoDrain
func TodoDrain() *C.char {
	if phoneApp == nil {
		return C.CString("")
	}
	phoneApp.dispatchMu.Lock()
	events := phoneApp.events
	phoneApp.events = nil
	phoneApp.dispatchMu.Unlock()
	return C.CString(strings.Join(events, ";\n"))
}

//export TodoInvoke
func TodoInvoke(method, data *C.char) *C.char {
	a := phoneApp
	handlers := map[string]any{
		"todo_ready": a.bindingGetState, "todo_getState": a.bindingGetState,
		"todo_addTodo": a.bindingAddTodo, "todo_updateTodo": a.bindingUpdateTodo,
		"todo_setTodoStatus": a.bindingSetTodoStatus, "todo_dropTodo": a.bindingDropTodo,
		"todo_deleteTodo": a.bindingDeleteTodo, "todo_moveTodo": a.bindingMoveTodo,
		"todo_setFocus": a.bindingSetFocus, "todo_journal": a.bindingJournal,
		"todo_sendInput": a.bindingSendInput,
		"todo_lanState":  a.bindingLANState, "todo_lanConfigure": a.bindingLANConfigure,
		"todo_lanRefresh": a.bindingLANRefresh, "todo_lanSelect": a.bindingLANSelect,
		"todo_savePrefs": func(p Prefs) bool { a.setPrefs(p); a.emitState(); return true },
	}
	value, err := invokeMobile(handlers[C.GoString(method)], []byte(C.GoString(data)))
	out := map[string]any{"value": value}
	if err != nil {
		out["error"] = err.Error()
	}
	response, err := json.Marshal(out)
	if err != nil {
		return C.CString(`{"error":"界面响应无法编码"}`)
	}
	return C.CString(string(response))
}

func invokeMobile(handler any, data []byte) (any, error) {
	if handler == nil {
		return nil, fmt.Errorf("未知的界面操作")
	}
	fn := reflect.ValueOf(handler)
	var args []json.RawMessage
	if err := json.Unmarshal(data, &args); err != nil || len(args) != fn.Type().NumIn() {
		return nil, fmt.Errorf("无效的界面参数")
	}
	values := make([]reflect.Value, len(args))
	for i, arg := range args {
		value := reflect.New(fn.Type().In(i))
		if err := json.Unmarshal(arg, value.Interface()); err != nil {
			return nil, err
		}
		values[i] = value.Elem()
	}
	results := fn.Call(values)
	if len(results) == 2 && !results[1].IsNil() {
		return nil, results[1].Interface().(error)
	}
	return results[0].Interface(), nil
}
