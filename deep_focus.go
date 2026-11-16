package main

import (
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
)

type deepFocus struct {
	ID        string `json:"id"`
	TodoID    string `json:"todoId"`
	Title     string `json:"title"`
	StartedAt int64  `json:"startedAt"`
	EndsAt    int64  `json:"endsAt"`
	Locked    bool   `json:"locked"`
	Exited    bool   `json:"exited,omitempty"`
}

func addDeepFocusEvent(state *persistedState, session deepFocus, kind, title, actor string, at int64) {
	id := "deep-focus-" + session.ID + "-" + kind
	if slices.ContainsFunc(state.Events, func(event HistoryEvent) bool { return event.ID == id }) {
		return
	}
	state.Events = append(slices.Clone(state.Events), HistoryEvent{
		ID: id, Kind: "deep-focus." + kind, Title: title, Actor: actor,
		TodoID: session.TodoID, Detail: session.Title, At: at,
	})
}

func (a *App) bindingStartDeepFocus(seconds int, locked bool) error {
	if seconds < 10 || seconds > 24*60*60 || seconds%10 != 0 {
		return fmt.Errorf("请选择 10 秒至 24 小时的时长")
	}
	a.mu.Lock()
	if current := a.state.DeepFocus; current.ID != "" && !current.Exited {
		a.mu.Unlock()
		return fmt.Errorf("已有高度专注，请先返回清单")
	}
	id := a.state.FocusID
	if id == "" || a.state.validateFocus(id) != nil {
		a.mu.Unlock()
		return fmt.Errorf("请先在清单中选中一个要专注的待办项")
	}
	next := a.state
	now := time.Now()
	for _, todo := range next.Todos {
		if todo.ID == id {
			next.DeepFocus = deepFocus{ID: uuid.NewString(), TodoID: id, Title: todo.Title,
				StartedAt: now.UnixMilli(), EndsAt: now.Add(time.Duration(seconds) * time.Second).UnixMilli(), Locked: locked}
			break
		}
	}
	addDeepFocusEvent(&next, next.DeepFocus, "start", "开始高度专注", "user", now.UnixMilli())
	err := a.persistLocked(next)
	a.mu.Unlock()
	if err == nil {
		a.publishDeepFocus()
	}
	return err
}

func (a *App) bindingExitDeepFocus(id string) error {
	a.mu.Lock()
	next := a.state
	session := next.DeepFocus
	if id == "" || id != session.ID || session.Exited {
		a.mu.Unlock()
		return fmt.Errorf("这次高度专注已在其他设备上变更")
	}
	now := time.Now().UnixMilli()
	if now < session.EndsAt && session.Locked {
		a.mu.Unlock()
		return fmt.Errorf("计时结束后才能退出")
	}
	if now >= session.EndsAt {
		addDeepFocusEvent(&next, session, "complete", "高度专注计时完成", "system", session.EndsAt)
	}
	next.DeepFocus.Exited = true
	addDeepFocusEvent(&next, session, "exit", "退出高度专注", "user", now)
	err := a.persistLocked(next)
	a.mu.Unlock()
	if err == nil {
		a.publishDeepFocus()
	}
	return err
}

// Called with a.mu held when the deadline changes or state is restored.
func (a *App) scheduleDeepFocusLocked() {
	if a.deepFocusTimer != nil {
		a.deepFocusTimer.Stop()
		a.deepFocusTimer = nil
	}
	session := a.state.DeepFocus
	if a.quitting || session.ID == "" || session.Exited {
		return
	}
	a.deepFocusTimer = time.AfterFunc(time.Until(time.UnixMilli(session.EndsAt)), func() {
		a.mu.Lock()
		next := a.state
		if a.quitting || next.DeepFocus != session {
			a.mu.Unlock()
			return
		}
		if time.Now().UnixMilli() < session.EndsAt {
			a.scheduleDeepFocusLocked()
			a.mu.Unlock()
			return
		}
		addDeepFocusEvent(&next, session, "complete", "高度专注计时完成", "system", session.EndsAt)
		changed := len(next.Events) != len(a.state.Events)
		if changed && a.persistLocked(next) != nil {
			a.mu.Unlock()
			return
		}
		a.mu.Unlock()
		if changed {
			a.publishDeepFocus()
		}
	})
}

func (a *App) publishDeepFocus() {
	a.emitState()
	if lan := a.getLAN(); lan != nil {
		lan.discovery.notifySync()
		lan.signal()
	}
}
