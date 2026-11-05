package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	webview2 "github.com/jchv/go-webview2"
)

// App owns all mutable state. Network work happens outside a.mu; snapshot()
// copies slices so the web layer never observes a concurrent append.
type App struct {
	mu sync.Mutex

	state     persistedState
	dataPath  string
	dirty     bool
	saveTimer *time.Timer

	quitting    bool
	startHidden bool
	pageReady   bool

	web     webview2.WebView
	hwnd    uintptr
	oldProc uintptr
	wndProc uintptr

	status   DSHStatus
	sessions []SessionOption

	dsh    *DSHClient
	bridge *BridgeServer
	tray   *trayCtx

	iconPath string
	logFile  *os.File
}

func NewApp() *App {
	a := &App{}
	a.state = defaultState()
	return a
}

func defaultState() persistedState {
	return persistedState{
		Version:  2,
		Todos:    []Todo{},
		Events:   []HistoryEvent{},
		Messages: []ChatMessage{},
		Prefs: Prefs{
			AlwaysOnTop:       false,
			Filter:            "all",
			Tab:               "tasks",
			ViewMode:          "cards",
			Window:            WindowRect{X: -9999, Y: -9999},
			WriteAgents:       true,
			OnboardedSessions: map[string]bool{},
		},
		DSH:    DSHConfig{AutoConnect: true},
		Bridge: BridgeConfig{Port: 17890},
	}
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
	if err := os.WriteFile(a.iconPath, iconICO, 0o644); err != nil {
		log.Printf("write icon: %v", err)
	}

	a.bridge = newBridge(a)
	if err := a.bridge.Start(); err != nil {
		log.Printf("bridge start: %v", err)
	}
	a.dsh = newDSHClient(a)
	a.dsh.Start()
	return nil
}

func (a *App) Shutdown() {
	a.mu.Lock()
	if a.quitting {
		a.mu.Unlock()
		return
	}
	a.quitting = true
	a.mu.Unlock()

	a.saveNow()
	if a.dsh != nil {
		a.dsh.Stop()
	}
	if a.bridge != nil {
		a.bridge.Stop()
	}
	if a.logFile != nil {
		_ = a.logFile.Close()
	}
}

func (a *App) snapshot() StatePayload {
	a.mu.Lock()
	defer a.mu.Unlock()
	todos := append(make([]Todo, 0, len(a.state.Todos)), a.state.Todos...)
	numbers := a.displayNumbersLocked()
	for i := range todos {
		todos[i].Number = numbers[todos[i].ID]
	}
	events := append(make([]HistoryEvent, 0, len(a.state.Events)), a.state.Events...)
	if len(events) > 1000 {
		events = events[len(events)-1000:]
	}
	messages := make([]ChatMessage, 0, 50)
	messageTotal := 0
	for i := len(a.state.Messages) - 1; i >= 0; i-- {
		m := a.state.Messages[i]
		if m.Kind == "onboarding" {
			continue
		}
		messageTotal++
		if len(messages) < 50 {
			messages = append(messages, m)
		}
	}
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
	sessions := append(make([]SessionOption, 0, len(a.sessions)), a.sessions...)
	cfg := a.state.DSH
	prefs := a.state.Prefs
	prefs.OnboardedSessions = cloneBoolMap(prefs.OnboardedSessions)
	bridge := BridgePublic{
		Port:  a.state.Bridge.Port,
		Token: a.state.Bridge.Token,
	}
	if a.bridge != nil {
		bridge.URL = a.bridge.URL()
		bridge.ConfigPath = a.bridge.ConfigPath()
	}
	return StatePayload{
		Version:      appVersion,
		Todos:        todos,
		Events:       events,
		Messages:     messages,
		MessageTotal: messageTotal,
		Prefs:        prefs,
		DSH:          a.status,
		Config:       cfg,
		Bridge:       bridge,
		Sessions:     sessions,
		DataPath:     a.dataPath,
		Platform:     "windows",
	}
}

func cloneBoolMap(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (a *App) isOnboarded(sessionID string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state.Prefs.OnboardedSessions[sessionID]
}

func (a *App) markOnboarded(sessionID string) {
	a.mu.Lock()
	if a.state.Prefs.OnboardedSessions == nil {
		a.state.Prefs.OnboardedSessions = map[string]bool{}
	}
	a.state.Prefs.OnboardedSessions[sessionID] = true
	a.dirty = true
	a.mu.Unlock()
	a.markDirty()
}

func (a *App) markDirty() {
	a.mu.Lock()
	a.dirty = true
	if a.saveTimer != nil {
		a.saveTimer.Stop()
	}
	a.saveTimer = time.AfterFunc(320*time.Millisecond, a.saveNow)
	a.mu.Unlock()
}

func (a *App) saveNow() {
	a.mu.Lock()
	if !a.dirty {
		a.mu.Unlock()
		return
	}
	b, err := json.MarshalIndent(a.state, "", "  ")
	a.dirty = false
	path := a.dataPath
	a.mu.Unlock()
	if err != nil {
		log.Printf("marshal state: %v", err)
		return
	}
	if err := atomicWrite(path, b); err != nil {
		log.Printf("save state: %v", err)
	}
}

func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".data-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func (a *App) emit(kind string, data any) {
	a.mu.Lock()
	w := a.web
	ready := a.pageReady
	a.mu.Unlock()
	if w == nil || !ready {
		return
	}
	payload, err := json.Marshal(map[string]any{"type": kind, "data": data})
	if err != nil {
		log.Printf("emit %s: %v", kind, err)
		return
	}
	script := "window.__todoAgent&&window.__todoAgent.push(" + string(payload) + ")"
	w.Dispatch(func() { w.Eval(script) })
}

func (a *App) emitState() {
	a.emit("state", a.snapshot())
}

func (a *App) emitStatus() {
	a.mu.Lock()
	s := a.status
	a.mu.Unlock()
	a.emit("status", s)
}

func (a *App) toast(kind, text string) {
	a.emit("toast", map[string]string{"kind": kind, "text": text})
}

func (a *App) setStatus(s DSHStatus) bool {
	a.mu.Lock()
	old := a.status
	a.status = s
	a.mu.Unlock()
	ob, _ := json.Marshal(old)
	nb, _ := json.Marshal(s)
	return !bytes.Equal(ob, nb)
}

func (a *App) getStatus() DSHStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.status
}

func (a *App) setSessions(items []SessionOption) {
	a.mu.Lock()
	a.sessions = items
	a.mu.Unlock()
}

func (a *App) getSessions() []SessionOption {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]SessionOption(nil), a.sessions...)
}

func (a *App) setSessionConfig(baseURL, sessionID string) {
	a.mu.Lock()
	a.state.DSH.BaseURL = baseURL
	a.state.DSH.SessionID = sessionID
	a.mu.Unlock()
	a.markDirty()
}

func (a *App) sessionID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state.DSH.SessionID
}

func (a *App) setPrefs(p Prefs) {
	a.mu.Lock()
	if p.Filter != "" {
		a.state.Prefs.Filter = p.Filter
	}
	if p.Tab != "" {
		a.state.Prefs.Tab = p.Tab
	}
	if p.ViewMode == "compact" || p.ViewMode == "cards" {
		a.state.Prefs.ViewMode = p.ViewMode
	}
	a.state.Prefs.AlwaysOnTop = p.AlwaysOnTop
	a.state.Prefs.WriteAgents = p.WriteAgents
	a.mu.Unlock()
	a.markDirty()
}

// ---- Todo mutations -------------------------------------------------------

func normalizePriority(p string) string {
	switch p {
	case priorityPinned, priorityHigh, priorityNormal, priorityLow:
		return p
	default:
		return priorityNormal
	}
}

func normalizeStatus(s string) string {
	switch s {
	case statusTodo, statusDoing, statusDone, statusDropped:
		return s
	default:
		return statusTodo
	}
}

func (a *App) latestJournalMessageLocked(maxAge time.Duration) (ChatMessage, bool) {
	for i := len(a.state.Messages) - 1; i >= 0; i-- {
		m := a.state.Messages[i]
		if m.Status != "sent" || m.Kind == "onboarding" {
			continue
		}
		if maxAge > 0 && time.Since(time.UnixMilli(m.At)) > maxAge {
			return ChatMessage{}, false
		}
		return m, true
	}
	return ChatMessage{}, false
}

func (a *App) messageByIDLocked(id string) (ChatMessage, bool) {
	for i := len(a.state.Messages) - 1; i >= 0; i-- {
		if a.state.Messages[i].ID == id {
			return a.state.Messages[i], true
		}
	}
	return ChatMessage{}, false
}

func (a *App) addTodo(in TodoInput, source, actor string) (Todo, error) {
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return Todo{}, fmt.Errorf("任务标题不能为空")
	}
	if len([]rune(title)) > 500 {
		title = string([]rune(title)[:500])
	}
	notes := strings.TrimSpace(in.Notes)
	if len([]rune(notes)) > 2000 {
		notes = string([]rune(notes)[:2000])
	}
	now := time.Now().UnixMilli()
	a.mu.Lock()
	sourceID := strings.TrimSpace(in.SourceMessageID)
	sourceText := strings.TrimSpace(in.SourceText)
	var sourceAt int64
	if sourceID != "" {
		if m, ok := a.messageByIDLocked(sourceID); ok {
			sourceText = m.Text
			sourceAt = m.At
		}
	} else if actor == "agent" {
		// DSH normally passes the message id from the hidden marker. Fall back
		// to the latest input so bridge actions from an older prompt still link
		// to the thought that caused them.
		if m, ok := a.latestJournalMessageLocked(6 * time.Hour); ok {
			sourceID = m.ID
			sourceText = m.Text
			sourceAt = m.At
		}
	}
	if len([]rune(sourceText)) > 2000 {
		sourceText = truncateRunes(sourceText, 2000)
	}
	parentID := strings.TrimSpace(in.ParentID)
	if parentID != "" {
		var parent *Todo
		for i := range a.state.Todos {
			if a.state.Todos[i].ID == parentID {
				parent = &a.state.Todos[i]
				break
			}
		}
		if parent == nil {
			a.mu.Unlock()
			return Todo{}, fmt.Errorf("父任务不存在")
		}
		if parent.ParentID != "" {
			a.mu.Unlock()
			return Todo{}, fmt.Errorf("子任务不能再创建子任务（只支持一层嵌套）")
		}
		if parent.Status == statusDropped || parent.Status == statusDone {
			a.mu.Unlock()
			return Todo{}, fmt.Errorf("父任务已完成或已丢弃，请先重新打开")
		}
	}
	for i := range a.state.Todos {
		if a.state.Todos[i].ParentID == parentID {
			a.state.Todos[i].Position++
		}
	}
	t := Todo{
		ID:              uuid.NewString(),
		Title:           title,
		Notes:           notes,
		Status:          normalizeStatus(in.Status),
		Priority:        normalizePriority(in.Priority),
		Source:          source,
		SessionID:       a.state.DSH.SessionID,
		CreatedAt:       now,
		UpdatedAt:       now,
		ParentID:        parentID,
		Position:        0,
		SourceMessageID: sourceID,
		SourceText:      sourceText,
		SourceAt:        sourceAt,
	}
	switch t.Status {
	case statusDone:
		t.CompletedAt = now
	case statusDropped:
		t.DroppedAt = now
		t.DropReason = truncateRunes(strings.TrimSpace(in.DropReason), 500)
	}
	a.state.Todos = append(a.state.Todos, t)
	detail := "新增任务"
	if parentID != "" {
		detail = "新增子任务"
	}
	a.appendEventLocked(HistoryEvent{
		ID: uuid.NewString(), Kind: "todo.add", Actor: actor,
		TodoID: t.ID, Title: t.Title, Detail: detail, At: now,
		SourceMessageID: sourceID, OriginalText: truncateRunes(sourceText, 500),
		After: cloneTodoPtr(t),
	})
	a.mu.Unlock()
	a.markDirty()
	a.emitState()
	return t, nil
}

func cloneTodoPtr(t Todo) *Todo {
	c := t
	return &c
}

func statusRank(status string) int {
	switch normalizeStatus(status) {
	case statusDoing:
		return 0
	case statusTodo:
		return 1
	case statusDone:
		return 2
	case statusDropped:
		return 3
	}
	return 1
}

func priorityRank(priority string) int {
	switch normalizePriority(priority) {
	case priorityPinned:
		return 0
	case priorityHigh:
		return 1
	case priorityLow:
		return 3
	}
	return 2
}

func todoOrderLess(a, b Todo) bool {
	if ra, rb := statusRank(a.Status), statusRank(b.Status); ra != rb {
		return ra < rb
	}
	if ra, rb := priorityRank(a.Priority), priorityRank(b.Priority); ra != rb {
		return ra < rb
	}
	if a.Position != b.Position {
		return a.Position < b.Position
	}
	return a.CreatedAt < b.CreatedAt
}

// displayNumbersLocked mirrors the UI's root/child order: top-level tasks are
// numbered 1,2,3…, and children become 2.4-style references. The numbers are
// display-only and recomputed on every snapshot; they are never persisted.
func (a *App) displayNumbersLocked() map[string]string {
	byID := make(map[string]bool, len(a.state.Todos))
	for _, t := range a.state.Todos {
		byID[t.ID] = true
	}
	roots := make([]Todo, 0, len(a.state.Todos))
	children := make(map[string][]Todo)
	for _, t := range a.state.Todos {
		if t.ParentID == "" || !byID[t.ParentID] {
			roots = append(roots, t)
			continue
		}
		children[t.ParentID] = append(children[t.ParentID], t)
	}
	sort.SliceStable(roots, func(i, j int) bool { return todoOrderLess(roots[i], roots[j]) })
	numbers := make(map[string]string, len(a.state.Todos))
	for i := range roots {
		rootNumber := strconv.Itoa(i + 1)
		numbers[roots[i].ID] = rootNumber
		kids := children[roots[i].ID]
		sort.SliceStable(kids, func(x, y int) bool { return todoOrderLess(kids[x], kids[y]) })
		for j := range kids {
			numbers[kids[j].ID] = rootNumber + "." + strconv.Itoa(j+1)
		}
	}
	return numbers
}

// moveTodo changes priority (when dropping into another priority band) and
// re-inserts the task relative to beforeID. Only siblings with the same parent
// participate, so moving a root task never disturbs a child's position.
func (a *App) moveTodo(id, priority, beforeID string, after bool, actor string) (Todo, error) {
	a.mu.Lock()
	idx := -1
	for i := range a.state.Todos {
		if a.state.Todos[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		a.mu.Unlock()
		return Todo{}, fmt.Errorf("任务不存在")
	}
	before := a.state.Todos[idx]
	moved := before
	if strings.TrimSpace(priority) != "" {
		moved.Priority = normalizePriority(priority)
	}
	now := time.Now().UnixMilli()
	moved.UpdatedAt = now

	siblings := make([]Todo, 0, len(a.state.Todos))
	for _, t := range a.state.Todos {
		if t.ParentID == moved.ParentID && t.ID != moved.ID {
			siblings = append(siblings, t)
		}
	}
	sort.SliceStable(siblings, func(i, j int) bool { return todoOrderLess(siblings[i], siblings[j]) })

	insert := len(siblings)
	if beforeID != "" {
		for i := range siblings {
			if siblings[i].ID == beforeID {
				insert = i
				if after {
					insert++
				}
				break
			}
		}
	} else {
		// Dropping on a priority header means "put it at the top of this band".
		for i := range siblings {
			if priorityRank(siblings[i].Priority) >= priorityRank(moved.Priority) {
				insert = i
				break
			}
		}
	}
	if insert < 0 {
		insert = 0
	}
	if insert > len(siblings) {
		insert = len(siblings)
	}

	ordered := make([]Todo, 0, len(siblings)+1)
	ordered = append(ordered, siblings[:insert]...)
	ordered = append(ordered, moved)
	ordered = append(ordered, siblings[insert:]...)
	positions := make(map[string]int, len(ordered))
	for i := range ordered {
		positions[ordered[i].ID] = i
	}
	moved.Position = positions[moved.ID]
	for i := range a.state.Todos {
		if p, ok := positions[a.state.Todos[i].ID]; ok {
			a.state.Todos[i].Position = p
		}
	}
	a.state.Todos[idx] = moved

	detail := "调整顺序"
	if before.Priority != moved.Priority {
		detail = "优先级改为" + priorityLabel(moved.Priority)
	}
	a.appendEventLocked(HistoryEvent{
		ID: uuid.NewString(), Kind: "todo.move", Actor: actor,
		TodoID: moved.ID, Title: moved.Title, Detail: detail, At: now,
		SourceMessageID: moved.SourceMessageID,
		OriginalText:    truncateRunes(moved.SourceText, 500),
		Before:          cloneTodoPtr(before),
		After:           cloneTodoPtr(moved),
	})
	a.mu.Unlock()
	a.markDirty()
	a.emitState()
	return moved, nil
}

func priorityLabel(priority string) string {
	switch normalizePriority(priority) {
	case priorityPinned:
		return "置顶 · 长期"
	case priorityHigh:
		return "高优先级"
	case priorityLow:
		return "低优先级"
	}
	return "中优先级"
}

func (a *App) updateTodo(id string, patch TodoPatch, actor string) (Todo, error) {
	a.mu.Lock()
	idx := -1
	for i := range a.state.Todos {
		if a.state.Todos[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		a.mu.Unlock()
		return Todo{}, fmt.Errorf("任务不存在")
	}
	before := a.state.Todos[idx]
	after := before
	changes := make([]string, 0, 4)
	if patch.Title != nil {
		v := strings.TrimSpace(*patch.Title)
		if v == "" {
			a.mu.Unlock()
			return Todo{}, fmt.Errorf("任务标题不能为空")
		}
		if len([]rune(v)) > 500 {
			v = string([]rune(v)[:500])
		}
		if v != after.Title {
			after.Title = v
			changes = append(changes, "标题")
		}
	}
	if patch.Notes != nil {
		v := strings.TrimSpace(*patch.Notes)
		if len([]rune(v)) > 2000 {
			v = string([]rune(v)[:2000])
		}
		if v != after.Notes {
			after.Notes = v
			changes = append(changes, "备注")
		}
	}
	if patch.Priority != nil {
		v := normalizePriority(*patch.Priority)
		if v != after.Priority {
			after.Priority = v
			changes = append(changes, "优先级")
		}
	}
	if patch.DropReason != nil {
		v := truncateRunes(strings.TrimSpace(*patch.DropReason), 500)
		if v != after.DropReason {
			after.DropReason = v
			changes = append(changes, "丢弃原因")
		}
	}
	if patch.Status != nil {
		v := normalizeStatus(*patch.Status)
		if v != after.Status {
			after.Status = v
			changes = append(changes, "状态")
			switch v {
			case statusDone:
				after.CompletedAt = time.Now().UnixMilli()
				after.DroppedAt = 0
				after.DropReason = ""
			case statusDropped:
				after.CompletedAt = 0
				after.DroppedAt = time.Now().UnixMilli()
			default:
				after.CompletedAt = 0
				after.DroppedAt = 0
				after.DropReason = ""
			}
		}
	}
	if len(changes) == 0 {
		a.mu.Unlock()
		return before, nil
	}
	after.UpdatedAt = time.Now().UnixMilli()
	a.state.Todos[idx] = after
	kind := "todo.update"
	detail := "更新 " + strings.Join(changes, "、")
	switch {
	case after.Status == statusDone && before.Status != statusDone:
		kind = "todo.complete"
		detail = "完成任务"
	case after.Status == statusDropped && before.Status != statusDropped:
		kind = "todo.drop"
		detail = "丢弃任务"
		if after.DropReason != "" {
			detail += "：" + after.DropReason
		}
	case after.Status == statusDoing && before.Status != statusDoing:
		kind = "todo.start"
		detail = "开始处理"
	case after.Status == statusTodo && (before.Status == statusDone || before.Status == statusDropped):
		kind = "todo.reopen"
		detail = "重新打开"
	}
	a.appendEventLocked(HistoryEvent{
		ID: uuid.NewString(), Kind: kind, Actor: actor,
		TodoID: after.ID, Title: after.Title, Detail: detail, At: after.UpdatedAt,
		SourceMessageID: after.SourceMessageID, OriginalText: truncateRunes(after.SourceText, 500),
		Before: cloneTodoPtr(before), After: cloneTodoPtr(after),
	})
	a.mu.Unlock()
	a.markDirty()
	a.emitState()
	return after, nil
}

func (a *App) setTodoStatus(id, status, actor string) (Todo, error) {
	return a.updateTodo(id, TodoPatch{Status: &status}, actor)
}

func (a *App) dropTodo(id, reason, actor string) (Todo, error) {
	status := statusDropped
	patch := TodoPatch{Status: &status}
	if strings.TrimSpace(reason) != "" {
		r := strings.TrimSpace(reason)
		patch.DropReason = &r
	}
	return a.updateTodo(id, patch, actor)
}

func (a *App) deleteTodo(id, actor string) error {
	a.mu.Lock()
	idx := -1
	for i := range a.state.Todos {
		if a.state.Todos[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		a.mu.Unlock()
		return fmt.Errorf("任务不存在")
	}
	before := a.state.Todos[idx]
	now := time.Now().UnixMilli()
	children := make([]Todo, 0)
	kept := make([]Todo, 0, len(a.state.Todos))
	for _, t := range a.state.Todos {
		if t.ID == id {
			continue
		}
		if t.ParentID == id {
			children = append(children, t)
			a.appendEventLocked(HistoryEvent{
				ID: uuid.NewString(), Kind: "todo.delete", Actor: actor,
				TodoID: t.ID, Title: t.Title, Detail: "随父任务删除",
				At: now, SourceMessageID: t.SourceMessageID,
				OriginalText: truncateRunes(t.SourceText, 500), Before: cloneTodoPtr(t),
			})
			continue
		}
		kept = append(kept, t)
	}
	a.state.Todos = kept
	detail := "删除任务"
	if len(children) > 0 {
		detail = fmt.Sprintf("删除任务（含 %d 个子任务）", len(children))
	}
	a.appendEventLocked(HistoryEvent{
		ID: uuid.NewString(), Kind: "todo.delete", Actor: actor,
		TodoID: before.ID, Title: before.Title, Detail: detail,
		At:              now,
		SourceMessageID: before.SourceMessageID,
		OriginalText:    truncateRunes(before.SourceText, 500),
		Before:          cloneTodoPtr(before),
	})
	a.mu.Unlock()
	a.markDirty()
	a.emitState()
	return nil
}

func (a *App) appendEventLocked(e HistoryEvent) {
	a.state.Events = append(a.state.Events, e)
	if len(a.state.Events) > 20000 {
		a.state.Events = append([]HistoryEvent(nil), a.state.Events[len(a.state.Events)-20000:]...)
	}
}

func (a *App) appendMessage(m ChatMessage) {
	a.mu.Lock()
	a.state.Messages = append(a.state.Messages, m)
	if len(a.state.Messages) > 10000 {
		a.state.Messages = append([]ChatMessage(nil), a.state.Messages[len(a.state.Messages)-10000:]...)
	}
	a.mu.Unlock()
	a.markDirty()
}

func (a *App) updateMessage(m ChatMessage) {
	a.mu.Lock()
	for i := range a.state.Messages {
		if a.state.Messages[i].ID == m.ID {
			a.state.Messages[i] = m
			break
		}
	}
	a.mu.Unlock()
	a.markDirty()
	a.emitState()
}

func (a *App) recordEvent(e HistoryEvent) {
	a.mu.Lock()
	a.appendEventLocked(e)
	a.mu.Unlock()
	a.markDirty()
}

func (a *App) todayStats() (open, done, dropped int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, t := range a.state.Todos {
		switch t.Status {
		case statusDone:
			done++
		case statusDropped:
			dropped++
		default:
			open++
		}
	}
	return
}

// journalPage returns raw inputs newest-first, optionally filtered by a
// case-insensitive substring. Each entry carries the tasks linked to it.
func (a *App) journalPage(beforeAt int64, limit int, query string) JournalPage {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	q := strings.ToLower(strings.TrimSpace(query))
	a.mu.Lock()
	defer a.mu.Unlock()

	tasksByMessage := map[string][]JournalTask{}
	for _, t := range a.state.Todos {
		if t.SourceMessageID == "" {
			continue
		}
		tasksByMessage[t.SourceMessageID] = append(tasksByMessage[t.SourceMessageID], JournalTask{
			ID: t.ID, Title: t.Title, Status: t.Status, Priority: t.Priority,
		})
	}

	matched := make([]ChatMessage, 0, limit+1)
	total := 0
	for i := len(a.state.Messages) - 1; i >= 0; i-- {
		m := a.state.Messages[i]
		if m.Kind == "onboarding" {
			continue
		}
		if beforeAt > 0 && m.At >= beforeAt {
			continue
		}
		if q != "" {
			hit := strings.Contains(strings.ToLower(m.Text), q)
			if !hit {
				for _, t := range tasksByMessage[m.ID] {
					if strings.Contains(strings.ToLower(t.Title), q) {
						hit = true
						break
					}
				}
			}
			if !hit {
				continue
			}
		}
		total++
		if len(matched) <= limit {
			matched = append(matched, m)
		}
	}
	hasMore := len(matched) > limit
	if hasMore {
		matched = matched[:limit]
	}
	items := make([]JournalEntry, 0, len(matched))
	for _, m := range matched {
		tasks := tasksByMessage[m.ID]
		if tasks == nil {
			tasks = []JournalTask{}
		}
		items = append(items, JournalEntry{
			ID: m.ID, Text: m.Text, Status: m.Status, SessionID: m.SessionID, At: m.At, Tasks: tasks,
		})
	}
	return JournalPage{Items: items, HasMore: hasMore, Total: total}
}

// ---- workspace instructions ----------------------------------------------

const agentsBegin = "<!-- todolist-agent:begin -->"
const agentsEnd = "<!-- todolist-agent:end -->"

func (a *App) agentsBlock() string {
	url := ""
	if a.bridge != nil {
		url = a.bridge.URL()
	}
	return strings.Join([]string{
		agentsBegin,
		"## 桌面 TodoList 应用联动（长期清单）",
		"",
		"本会话与一个桌面 TodoList 应用联动。用户会持续、零散地输入想法，可能夹带感慨、吐槽、转述和链接；**不是每句话都是任务**。任务清单和想法回顾库由这个本地接口维护：",
		"",
		"1. 读取 `%APPDATA%\\TodoListAgent\\bridge.json`，取 `url` 与 `token`。",
		"2. `GET <url>/api/todos` 查看全部任务（含 `dropped`），用于查重和定位。",
		"3. `GET <url>/api/journal?limit=20&q=关键词` 回看旧想法原文及其关联任务。",
		"4. `POST <url>/api/todos` 执行动作：",
		"   - `{\"action\":\"add\",\"title\":\"...\",\"notes\":\"...\",\"priority\":\"pinned|high|normal|low\",\"parentId\":\"...\",\"sourceMessageId\":\"...\"}`",
		"   - `{\"action\":\"update\",\"id\":\"...\",\"title\":\"...\",\"status\":\"todo|doing|done|dropped\",\"dropReason\":\"...\"}`",
		"   - `{\"action\":\"complete\",\"id\":\"...\"}` / `reopen` / `drop`（带 reason） / `delete`",
		"",
		"处理规则：",
		"- 只把**明确的行动意图**（想做、要做、记一下、提醒我、推进某事）提炼成任务；纯情绪/感慨/分享不要自动建任务，可以简短回应或问一句是否需要记下。",
		"- 一条消息有多个行动点就拆成多个任务；一个行动点不要拆碎。",
		"- 任务支持一层子任务：`add` 时传 `parentId` 即可；子任务不能再有子任务。只有当一个任务确实有 2-5 个明确步骤时才拆子任务，不要为了层级而层级。",
		"- 长期跟踪、需要一直排在最前面但不紧急的任务用 `priority:\"pinned\"`（置顶 · 长期）；它排在 high 之前，但视觉上不抢高优先级。",
		"- `GET /api/todos` 返回的每个任务有 `number` 字段（如 `2.4`），它就是打钩圆圈右侧的序号；用户用序号指代时按 `number` 精确找到 id，不要按数组下标猜。序号会随排序、优先级和状态变化，操作前重新 GET。",
		"- 先查列表再新增，避免重复；相似任务优先 update 而不是 add。",
		"- 用户说“关掉/做完了/搞定” → `complete`；说“不做了/算了/放弃/搁置/丢弃” → `drop` 并写 `reason`；只有明确说“删除/删掉”才 `delete`。",
		"- 开始处理某任务 → `doing`；完成后 → `done`。",
		"- 任务标题写成可执行的短句；`notes` 保留用户原话和背景，不要过度改写。",
		"- 来自 TodoList 的消息末尾有隐藏标记 `<!-- todo-msg:<id> -->`，请把它作为 `sourceMessageId`；没有标记就省略。",
		"- 回复只简要说明实际新增/完成/丢弃了哪些任务，不要只列计划。",
		"",
		"当前接口地址（可能随应用重启变化，以 bridge.json 为准）：" + url,
		agentsEnd,
	}, "\n")
}

func (a *App) writeAgentsBlock() (string, error) {
	status := a.getStatus()
	cwd := strings.TrimSpace(status.CWD)
	if cwd == "" {
		a.mu.Lock()
		cwd = strings.TrimSpace(a.state.DSH.Workspace)
		a.mu.Unlock()
	}
	if cwd == "" {
		return "", fmt.Errorf("还没有可用的工作区，请先连接 DSH 会话")
	}
	// Keep the overlay local so a shared AGENTS.md is not modified. DSH loads
	// AGENTS.local.md after the base chain, which is exactly the precedence a
	// machine-local integration note needs.
	path := filepath.Join(cwd, "AGENTS.local.md")
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	content := string(old)
	block := a.agentsBlock()
	if i := strings.Index(content, agentsBegin); i >= 0 {
		if j := strings.Index(content[i:], agentsEnd); j >= 0 {
			content = content[:i] + block + content[i+j+len(agentsEnd):]
		} else {
			content = content[:i] + block
		}
	} else {
		if strings.TrimSpace(content) != "" {
			content = strings.TrimRight(content, "\r\n") + "\n\n"
		}
		content += block + "\n"
	}
	if err := atomicWrite(path, []byte(content)); err != nil {
		return "", err
	}
	// Migrate an earlier version that wrote the block into AGENTS.md.
	base := filepath.Join(cwd, "AGENTS.md")
	if legacy, rerr := os.ReadFile(base); rerr == nil {
		s := string(legacy)
		if i := strings.Index(s, agentsBegin); i >= 0 {
			if j := strings.Index(s[i:], agentsEnd); j >= 0 {
				s = strings.TrimRight(s[:i]+s[i+j+len(agentsEnd):], "\r\n ")
				if strings.TrimSpace(s) == "" {
					_ = os.Remove(base)
				} else {
					_ = atomicWrite(base, []byte(s+"\n"))
				}
			}
		}
	}
	return path, nil
}

func (a *App) workspacePath() string {
	status := a.getStatus()
	if status.CWD != "" {
		return status.CWD
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state.DSH.Workspace
}

func sortSessions(items []SessionOption) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].UpdatedAt == items[j].UpdatedAt {
			return items[i].ID < items[j].ID
		}
		return items[i].UpdatedAt > items[j].UpdatedAt
	})
}
