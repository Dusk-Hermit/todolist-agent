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
)

// App owns all mutable state. Network work happens outside a.mu; snapshot()
// copies slices so the web layer never observes a concurrent append.
type App struct {
	mu          sync.Mutex
	stateEmitMu sync.Mutex
	updates     updater

	state     persistedState
	dataPath  string
	dirty     bool
	saveTimer *time.Timer

	quitting bool
	platformState

	lan         *lanService
	lanError    string
	lanConfigMu sync.Mutex

	status   DSHStatus
	sessions []SessionOption

	dsh     *DSHClient
	bridge  *BridgeServer
	logFile *os.File
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

func (a *App) Shutdown() {
	a.mu.Lock()
	if a.quitting {
		a.mu.Unlock()
		return
	}
	a.quitting = true
	a.mu.Unlock()

	a.stopLAN()
	a.stopUpdateChecks()
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
	uncertain := 0
	for i := len(a.state.Messages) - 1; i >= 0; i-- {
		m := a.state.Messages[i]
		if m.Kind == "onboarding" {
			continue
		}
		messageTotal++
		if m.Status == "uncertain" {
			uncertain++
		}
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
	promptStage := ""
	if prompt := a.state.ActivePrompt; prompt != nil {
		promptStage = "sending"
		if prompt.Accepted {
			promptStage = "accepted"
		}
	}
	return StatePayload{
		Version:      appVersion,
		Todos:        todos,
		Events:       events,
		Messages:     messages,
		MessageTotal: messageTotal,
		PromptStage:  promptStage,
		Uncertain:    uncertain,
		Prefs:        prefs,
		FocusID:      a.state.FocusID,
		DSH:          a.status,
		Config:       cfg,
		Bridge:       bridge,
		Sessions:     sessions,
		DataPath:     a.dataPath,
		Platform:     platformName,
	}
}

func cloneBoolMap(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
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
	if a.state.Replica.Device != "" {
		a.captureReplicaLocked()
	}
	b, err := json.MarshalIndent(a.state, "", "  ")
	defer a.mu.Unlock()
	if err != nil {
		log.Printf("marshal state: %v", err)
		return
	}
	if err := atomicWrite(a.dataPath, b); err != nil {
		log.Printf("save state: %v", err)
		return
	}
	a.dirty = false
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
	payload, err := json.Marshal(map[string]any{"type": kind, "data": data})
	if err != nil {
		log.Printf("emit %s: %v", kind, err)
		return
	}
	script := "window.__todoAgent&&window.__todoAgent.push(" + string(payload) + ")"
	a.dispatch(script)
}

func (a *App) emitState() {
	a.stateEmitMu.Lock()
	defer a.stateEmitMu.Unlock()
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
	case statusTodo, statusDoing, statusDone:
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
	if in.Status == "dropped" {
		return Todo{}, fmt.Errorf("丢弃状态已移除，明确要求移出清单时请使用删除")
	}
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
	placement := strings.ToLower(strings.TrimSpace(in.Placement))
	if placement != "" && placement != "first" && placement != "last" {
		return Todo{}, fmt.Errorf("placement 必须为 first 或 last")
	}
	appendToGroup := placement == "last" || (placement == "" && source == "agent")
	status, priority := normalizeStatus(in.Status), normalizePriority(in.Priority)
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
		if parent.Status == statusDone {
			a.mu.Unlock()
			return Todo{}, fmt.Errorf("父任务已完成，请先重新打开")
		}
	}
	position := 0
	for _, sibling := range a.state.Todos {
		if sibling.ParentID != parentID {
			continue
		}
		if !appendToGroup {
			position = min(position, sibling.Position-1)
		} else if sibling.Status == status && (parentID != "" || sibling.Priority == priority) {
			position = max(position, sibling.Position+1)
		}
	}
	t := Todo{
		ID:              uuid.NewString(),
		Title:           title,
		Notes:           notes,
		Status:          status,
		Priority:        priority,
		Source:          source,
		SessionID:       a.state.DSH.SessionID,
		CreatedAt:       now,
		UpdatedAt:       now,
		ParentID:        parentID,
		Position:        position,
		SourceMessageID: sourceID,
		SourceText:      sourceText,
		SourceAt:        sourceAt,
	}
	switch t.Status {
	case statusDone:
		t.CompletedAt = now
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
	if a.state.Replica.Device != "" {
		a.captureReplicaLocked()
	}
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
	if moved.Priority != before.Priority {
		moved.UpdatedAt = now
	}

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
	if a.state.Replica.Device != "" {
		a.captureReplicaLocked()
	}
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
	if patch.Status != nil && *patch.Status == "dropped" {
		return Todo{}, fmt.Errorf("丢弃状态已移除，明确要求移出清单时请使用删除")
	}
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
	if patch.Status != nil {
		v := normalizeStatus(*patch.Status)
		if v != after.Status {
			after.Status = v
			changes = append(changes, "状态")
			if v == statusDone {
				after.CompletedAt = time.Now().UnixMilli()
			} else {
				after.CompletedAt = 0
			}
		}
	}
	if len(changes) == 0 {
		a.mu.Unlock()
		return before, nil
	}
	after.UpdatedAt = time.Now().UnixMilli()
	a.state.Todos[idx] = after
	if after.Status == statusDone && a.state.validateFocus(a.state.FocusID) != nil {
		a.state.FocusID = ""
	}
	kind := "todo.update"
	detail := "更新 " + strings.Join(changes, "、")
	switch {
	case after.Status == statusDone && before.Status != statusDone:
		kind = "todo.complete"
		detail = "完成任务"
	case after.Status == statusDoing && before.Status != statusDoing:
		kind = "todo.start"
		detail = "开始处理"
	case after.Status == statusTodo && before.Status == statusDone:
		kind = "todo.reopen"
		detail = "重新打开"
	}
	a.appendEventLocked(HistoryEvent{
		ID: uuid.NewString(), Kind: kind, Actor: actor,
		TodoID: after.ID, Title: after.Title, Detail: detail, At: after.UpdatedAt,
		SourceMessageID: after.SourceMessageID, OriginalText: truncateRunes(after.SourceText, 500),
		Before: cloneTodoPtr(before), After: cloneTodoPtr(after),
	})
	if a.state.Replica.Device != "" {
		a.captureReplicaLocked()
	}
	a.mu.Unlock()
	a.markDirty()
	a.emitState()
	return after, nil
}

func (a *App) setTodoStatus(id, status, actor string) (Todo, error) {
	return a.updateTodo(id, TodoPatch{Status: &status}, actor)
}

func (st *persistedState) validateFocus(id string) error {
	if id == "" {
		return nil
	}
	for _, task := range st.Todos {
		if task.ID != id {
			continue
		}
		if task.Status == statusDone {
			return fmt.Errorf("只能专注未完成的任务")
		}
		if task.ParentID != "" {
			for _, parent := range st.Todos {
				if parent.ID == task.ParentID && parent.Status == statusDone {
					return fmt.Errorf("父任务已完成，请先重新打开父任务")
				}
			}
		}
		return nil
	}
	return fmt.Errorf("任务不存在")
}

// setFocus makes one task the current focus. An empty id clears it.
func (a *App) setFocus(id, actor string) (string, error) {
	a.mu.Lock()
	id = strings.TrimSpace(id)
	if err := a.state.validateFocus(id); err != nil {
		a.mu.Unlock()
		return "", err
	}
	a.state.FocusID = id
	if a.state.Replica.Device != "" {
		a.captureReplicaLocked()
	}
	a.mu.Unlock()
	a.markDirty()
	a.emitState()
	return id, nil
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
	if a.state.FocusID == id {
		a.state.FocusID = ""
	}
	children := make([]Todo, 0)
	kept := make([]Todo, 0, len(a.state.Todos))
	for _, t := range a.state.Todos {
		if t.ID == id {
			continue
		}
		if t.ParentID == id {
			if a.state.FocusID == t.ID {
				a.state.FocusID = ""
			}
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
	if a.state.Replica.Device != "" {
		a.captureReplicaLocked()
	}
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

func (a *App) todayStats() (open, done int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, t := range a.state.Todos {
		switch t.Status {
		case statusDone:
			done++
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
			ID: m.ID, Text: m.Text, Status: m.Status, Error: m.Error, SessionID: m.SessionID, At: m.At, Tasks: tasks,
		})
	}
	return JournalPage{Items: items, HasMore: hasMore, Total: total}
}

func sortSessions(items []SessionOption) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].UpdatedAt == items[j].UpdatedAt {
			return items[i].ID < items[j].ID
		}
		return items[i].UpdatedAt > items[j].UpdatedAt
	})
}
