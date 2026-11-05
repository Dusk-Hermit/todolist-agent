package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func dataDir() (string, error) {
	if dir := strings.TrimSpace(os.Getenv("TODOLIST_AGENT_HOME")); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
		return dir, nil
	}
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		base = os.Getenv("APPDATA")
	}
	if base == "" {
		return "", fmt.Errorf("cannot resolve user config directory")
	}
	dir := filepath.Join(base, "TodoListAgent")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func (a *App) load() error {
	data, err := os.ReadFile(a.dataPath)
	if err != nil {
		if os.IsNotExist(err) {
			a.state = defaultState()
			return nil
		}
		return err
	}
	if len(data) == 0 {
		a.state = defaultState()
		return nil
	}
	var st persistedState
	if err := json.Unmarshal(data, &st); err != nil {
		backup := fmt.Sprintf("%s.corrupt-%d", a.dataPath, time.Now().Unix())
		_ = os.WriteFile(backup, data, 0o644)
		log.Printf("state corrupt, backed up to %s: %v", backup, err)
		a.state = defaultState()
		return err
	}
	normalizeState(&st)
	a.state = st
	return nil
}

func normalizeState(st *persistedState) {
	if st.Version < 2 {
		st.Version = 2
	}
	if st.Todos == nil {
		st.Todos = []Todo{}
	}
	if st.Events == nil {
		st.Events = []HistoryEvent{}
	}
	if st.Messages == nil {
		st.Messages = []ChatMessage{}
	}
	if st.Prefs.Filter == "" {
		st.Prefs.Filter = "all"
	}
	if st.Prefs.Tab == "" {
		st.Prefs.Tab = "tasks"
	}
	if st.Prefs.ViewMode != "compact" {
		st.Prefs.ViewMode = "cards"
	}
	if st.Prefs.OnboardedSessions == nil {
		st.Prefs.OnboardedSessions = map[string]bool{}
	}
	if st.Prefs.Window.X == 0 && st.Prefs.Window.Y == 0 {
		st.Prefs.Window.X = -9999
		st.Prefs.Window.Y = -9999
	}
	if st.Bridge.Port == 0 {
		st.Bridge.Port = 17890
	}
	for i := range st.Todos {
		t := &st.Todos[i]
		if t.ID == "" {
			t.ID = fmt.Sprintf("legacy-%d-%d", i, time.Now().UnixNano())
		}
		if t.Status == "" {
			t.Status = statusTodo
		}
		if t.Priority == "" {
			t.Priority = priorityNormal
		}
		if t.Source == "" {
			t.Source = "user"
		}
		if t.CreatedAt == 0 {
			t.CreatedAt = time.Now().UnixMilli()
		}
		if t.UpdatedAt == 0 {
			t.UpdatedAt = t.CreatedAt
		}
		if t.Status == statusDropped && t.DroppedAt == 0 {
			t.DroppedAt = t.UpdatedAt
		}
	}
	for i := range st.Messages {
		m := &st.Messages[i]
		if m.ID == "" {
			m.ID = fmt.Sprintf("msg-legacy-%d", i)
		}
		if m.Kind == "" {
			if strings.HasPrefix(strings.TrimSpace(m.Text), "【TodoList") {
				m.Kind = "onboarding"
			} else {
				m.Kind = "input"
			}
		}
	}
	messageText := make(map[string]string, len(st.Messages))
	messageAt := make(map[string]int64, len(st.Messages))
	onboardingIDs := map[string]bool{}
	for _, m := range st.Messages {
		messageText[m.ID] = m.Text
		messageAt[m.ID] = m.At
		if m.Kind == "onboarding" {
			onboardingIDs[m.ID] = true
		}
	}
	for i := range st.Events {
		e := &st.Events[i]
		if e.ID == "" {
			e.ID = fmt.Sprintf("evt-legacy-%d", i)
		}
		if onboardingIDs[e.SourceMessageID] || strings.HasPrefix(strings.TrimSpace(e.OriginalText), "【TodoList") {
			e.SourceMessageID = ""
			e.OriginalText = ""
			if e.Kind == "input" || strings.HasPrefix(strings.TrimSpace(e.Detail), "【TodoList") {
				e.Kind = "onboarding"
				e.Title = "启用清单同步"
				e.Detail = "已发送 TodoList 长期清单接入说明"
			}
		}
	}
	for i := range st.Todos {
		t := &st.Todos[i]
		if onboardingIDs[t.SourceMessageID] {
			// Never expose setup text/tokens as a task's origin.
			t.SourceMessageID = ""
			t.SourceText = ""
			t.SourceAt = 0
		}
		if t.SourceMessageID != "" {
			if t.SourceText == "" {
				if text := messageText[t.SourceMessageID]; text != "" {
					t.SourceText = truncateRunes(text, 2000)
				}
			}
			if t.SourceAt == 0 {
				t.SourceAt = messageAt[t.SourceMessageID]
			}
		}
	}
}
