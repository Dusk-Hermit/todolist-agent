package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// BridgeServer is the small localhost API the DSH agent uses to keep the
// desktop list in sync. It is loopback-only and bearer-token protected.
type BridgeServer struct {
	app *App

	mu    sync.Mutex
	srv   *http.Server
	port  int
	token string
	url   string
	cfg   string
}

func newBridge(a *App) *BridgeServer {
	return &BridgeServer{app: a}
}

func (b *BridgeServer) Start() error {
	a := b.app
	a.mu.Lock()
	token := a.state.Bridge.Token
	port := a.state.Bridge.Port
	a.mu.Unlock()
	if token == "" {
		token = randomToken()
	}
	if port <= 0 {
		port = 17890
	}
	ln, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		var lerr error
		ln, lerr = net.Listen("tcp4", "127.0.0.1:0")
		if lerr != nil {
			return fmt.Errorf("listen bridge: %w", err)
		}
	}
	actualPort := ln.Addr().(*net.TCPAddr).Port
	url := fmt.Sprintf("http://127.0.0.1:%d", actualPort)

	dir, err := dataDir()
	if err != nil {
		_ = ln.Close()
		return err
	}
	cfgPath := filepath.Join(dir, "bridge.json")

	b.mu.Lock()
	b.port = actualPort
	b.token = token
	b.url = url
	b.cfg = cfgPath
	b.mu.Unlock()

	a.mu.Lock()
	a.state.Bridge.Port = actualPort
	a.state.Bridge.Token = token
	a.mu.Unlock()
	a.markDirty()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", b.handleHealth)
	mux.HandleFunc("/api/todos", b.handleTodos)
	mux.HandleFunc("/api/journal", b.handleJournal)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "not found"})
	})

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	b.mu.Lock()
	b.srv = srv
	b.mu.Unlock()

	info := map[string]any{
		"app":       appName,
		"version":   appVersion,
		"url":       url,
		"token":     token,
		"port":      actualPort,
		"updatedAt": time.Now().UnixMilli(),
	}
	if err := writeBridgeFile(cfgPath, info); err != nil {
		log.Printf("write bridge.json: %v", err)
	}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("bridge serve: %v", err)
		}
	}()
	log.Printf("agent bridge listening on %s", url)
	return nil
}

func writeBridgeFile(path string, info map[string]any) error {
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, data)
}

func (b *BridgeServer) Stop() {
	b.mu.Lock()
	srv := b.srv
	b.mu.Unlock()
	if srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

func (b *BridgeServer) URL() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.url
}

func (b *BridgeServer) Token() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.token
}

func (b *BridgeServer) ConfigPath() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cfg
}

func (b *BridgeServer) authorized(r *http.Request) bool {
	b.mu.Lock()
	token := b.token
	b.mu.Unlock()
	if token == "" {
		return false
	}
	got := ""
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		got = strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	if got == "" {
		got = r.Header.Get("X-Todo-Token")
	}
	if got == "" {
		got = r.URL.Query().Get("token")
	}
	if len(got) != len(token) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (b *BridgeServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"app":     appName,
		"version": appVersion,
	})
}

func (b *BridgeServer) handleJournal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}
	if !b.authorized(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "invalid bridge token"})
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
	page := b.app.journalPage(before, limit, q.Get("q"))
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"items":   page.Items,
		"hasMore": page.HasMore,
		"total":   page.Total,
	})
}

type bridgeAction struct {
	Action          string  `json:"action"`
	Placement       string  `json:"placement"`
	ID              string  `json:"id"`
	Title           *string `json:"title"`
	Notes           *string `json:"notes"`
	Priority        *string `json:"priority"`
	Status          *string `json:"status"`
	ParentID        string  `json:"parentId"`
	SourceMessageID string  `json:"sourceMessageId"`
	SourceText      string  `json:"sourceText"`
}

func (b *BridgeServer) handleTodos(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.Header().Set("Allow", "GET, POST, OPTIONS")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !b.authorized(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "invalid bridge token"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "todos": b.app.snapshot().Todos, "stats": b.stats()})
	case http.MethodPost:
		b.handlePost(w, r)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
	}
}

func (b *BridgeServer) handlePost(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 256<<10))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "read body"})
		return
	}
	var act bridgeAction
	if err := json.Unmarshal(body, &act); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid json: " + err.Error()})
		return
	}
	action := strings.ToLower(strings.TrimSpace(act.Action))
	if action == "" && act.Title != nil {
		action = "add"
	}
	actor := "agent"
	var todo Todo
	switch action {
	case "list":
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "todos": b.app.snapshot().Todos, "stats": b.stats()})
		return
	case "add":
		if act.Title == nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "title required"})
			return
		}
		in := TodoInput{
			Title: *act.Title, Status: statusTodo, Priority: priorityNormal, ParentID: act.ParentID,
			SourceMessageID: act.SourceMessageID, SourceText: act.SourceText, Placement: act.Placement,
		}
		if act.Notes != nil {
			in.Notes = *act.Notes
		}
		if act.Priority != nil {
			in.Priority = *act.Priority
		}
		if act.Status != nil {
			in.Status = *act.Status
		}
		todo, err = b.app.addTodo(in, "agent", actor)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
	case "update":
		if act.ID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "id required"})
			return
		}
		patch := TodoPatch{Title: act.Title, Notes: act.Notes, Priority: act.Priority, Status: act.Status}
		todo, err = b.app.updateTodo(act.ID, patch, actor)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
	case "complete", "done":
		if act.ID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "id required"})
			return
		}
		todo, err = b.app.setTodoStatus(act.ID, statusDone, actor)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
	case "reopen", "todo":
		if act.ID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "id required"})
			return
		}
		todo, err = b.app.setTodoStatus(act.ID, statusTodo, actor)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
	case "doing", "start":
		if act.ID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "id required"})
			return
		}
		todo, err = b.app.setTodoStatus(act.ID, statusDoing, actor)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
	case "drop", "discard", "abandon":
		writeJSON(w, http.StatusGone, map[string]any{"ok": false, "error": "丢弃操作已移除，仅在用户明确要求移出清单时使用 delete"})
		return
	case "delete":
		if act.ID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "id required"})
			return
		}
		if err := b.app.deleteTodo(act.ID, actor); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		todo = Todo{}
	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "unknown action " + action})
		return
	}

	snap := b.app.snapshot()
	if action != "delete" {
		prefix := "DSH 更新了任务："
		switch action {
		case "add":
			prefix = "DSH 新增了任务："
		case "complete", "done":
			prefix = "DSH 完成了任务："
		}
		b.app.toast("agent", prefix+todo.Title)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":    true,
		"todo":  todo,
		"todos": snap.Todos,
		"stats": b.stats(),
	})
	log.Printf("bridge %s todo=%s title=%q", action, todo.ID, todo.Title)
}

func (b *BridgeServer) stats() map[string]int {
	open, done := b.app.todayStats()
	return map[string]int{"open": open, "done": done, "total": open + done}
}

func randomToken() string {
	// uuid v4 twice gives 244 bits of entropy without another import.
	return strings.ReplaceAll(uuid.NewString(), "-", "") + strings.ReplaceAll(uuid.NewString(), "-", "")
}
