package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

const promptCollectTime = 5 * time.Second
const maxPromptBytes = 64000

var errBatchRunning = errors.New("DSH 正在处理消息，完成后会自动同步")

type queuedPrompt struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	At   int64  `json:"at"`
}

type activePrompt struct {
	queuedPrompt
	Session         string `json:"session"`
	Started         int64  `json:"started"`
	PreviousSeq     int    `json:"previousSeq"`
	PreviousTurns   int    `json:"previousTurns"`
	ObservedRunning bool   `json:"observedRunning,omitempty"`
}

func orderedQueue(queue map[string]queuedPrompt) []queuedPrompt {
	out := slices.Collect(maps.Values(queue))
	slices.SortFunc(out, func(a, b queuedPrompt) int {
		if a.At < b.At {
			return -1
		}
		if a.At > b.At {
			return 1
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out
}

func validQueuedPrompt(p queuedPrompt) bool {
	_, err := uuid.Parse(p.ID)
	return err == nil && p.At > 0 && strings.TrimSpace(p.Text) != "" && len(p.Text) <= maxPromptBytes
}

func (a *App) enqueuePrompts(prompts []queuedPrompt) error {
	for _, p := range prompts {
		if !validQueuedPrompt(p) {
			return fmt.Errorf("消息为空、过长或格式无效")
		}
	}
	a.mu.Lock()
	if a.quitting {
		a.mu.Unlock()
		return fmt.Errorf("应用正在退出")
	}
	next := a.state
	next.Queue = maps.Clone(next.Queue)
	next.QueueReceipts = maps.Clone(next.QueueReceipts)
	if next.Queue == nil {
		next.Queue = make(map[string]queuedPrompt)
	}
	if next.QueueReceipts == nil {
		next.QueueReceipts = make(map[string]bool)
	}
	changed := false
	for _, p := range prompts {
		if next.QueueReceipts[p.ID] {
			continue
		}
		next.Queue[p.ID] = p
		next.QueueReceipts[p.ID] = true
		changed = true
	}
	if !changed {
		a.mu.Unlock()
		return nil
	}
	if a.dsh != nil && len(a.state.Queue) == 0 && a.state.ActivePrompt == nil {
		next.QueueReadyAt = time.Now().Add(promptCollectTime).UnixMilli()
	}
	err := a.persistLocked(next)
	a.mu.Unlock()
	if err == nil {
		if a.dsh != nil {
			a.dsh.Wake()
		}
		if s := a.getLAN(); s != nil {
			s.signal()
		}
		a.emit("lan", a.bindingLANState())
	}
	return err
}

func (a *App) bindingSendInput(text string) (map[string]any, error) {
	p := queuedPrompt{ID: uuid.NewString(), Text: strings.TrimSpace(text), At: time.Now().UnixMilli()}
	if err := a.enqueuePrompts([]queuedPrompt{p}); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "id": p.ID, "queued": true}, nil
}

func (a *App) queueUploaded(prompts []queuedPrompt) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	next := a.state
	next.Queue = maps.Clone(next.Queue)
	for _, p := range prompts {
		delete(next.Queue, p.ID)
	}
	return a.persistLocked(next)
}

func (a *App) batchPausedLocked() bool {
	return a.dsh != nil && (a.state.ActivePrompt != nil || a.state.QueueBatch)
}

func (c *DSHClient) pumpQueue(ctx context.Context, selected *sessionRow, sessions []sessionRow) {
	a := c.app
	a.mu.Lock()
	active := a.state.ActivePrompt
	if active != nil {
		a.mu.Unlock()
		row := findSession(sessions, active.Session)
		if row != nil && row.Running {
			if !active.ObservedRunning {
				a.mu.Lock()
				updated := *active
				updated.ObservedRunning = true
				next := a.state
				next.ActivePrompt = &updated
				_ = a.persistLocked(next)
				a.mu.Unlock()
			}
			return
		}
		if row != nil && queuedPromptCompleted(active, row) {
			c.finishQueued(active, "")
		} else if time.Since(time.UnixMilli(active.Started)) > 90*time.Second {
			c.finishQueued(active, "无法确认 DSH 已执行此消息，请在会话中核对")
		}
		return
	}
	if selected == nil || selected.Running || len(a.state.Queue) == 0 ||
		time.Now().UnixMilli() < a.state.QueueReadyAt || a.quitting {
		a.mu.Unlock()
		return
	}
	item := orderedQueue(a.state.Queue)[0]
	active = &activePrompt{queuedPrompt: item, Session: selected.SessionID,
		Started: time.Now().UnixMilli(), PreviousSeq: selected.Projections.AsOfSeq,
		PreviousTurns: selected.Projections.Values.SessionStats.Turns}
	next := a.state
	next.Queue = maps.Clone(next.Queue)
	delete(next.Queue, item.ID)
	next.ActivePrompt = active
	next.QueueBatch = true
	next.Messages = append(slices.Clone(next.Messages), ChatMessage{
		ID: item.ID, Text: item.Text, At: item.At, Status: "sending", Kind: "input", SessionID: active.Session,
	})
	slices.SortFunc(next.Messages, compareMessages)
	err := a.persistLocked(next)
	a.mu.Unlock()
	if err != nil {
		return
	}
	a.emitState()

	raw, err := c.call(ctx, "session/prompt", promptArgs(item.ID, active.Session,
		item.Text+"\n\n<!-- todo-msg:"+item.ID+" -->"))
	if err != nil {
		// The request may have reached DSH before a transport failure.
		// Keep the claim and reconcile it from session activity.
		c.Wake()
		return
	}
	var reply struct {
		Accepted bool `json:"accepted"`
	}
	if json.Unmarshal(raw, &reply) != nil || !reply.Accepted {
		c.finishQueued(active, "DSH 未接受消息")
		return
	}
	a.updateQueuedMessage(active, "sent", "")
	c.Wake()
}

func queuedPromptCompleted(active *activePrompt, row *sessionRow) bool {
	if row.Running {
		return false
	}
	newResponse := false
	for _, turn := range row.Projections.Values.TurnOutline {
		if turn.Response == "" {
			continue
		}
		if strings.Contains(turn.Prompt, "<!-- todo-msg:"+active.ID+" -->") ||
			(turn.Seq > active.PreviousSeq && strings.TrimSpace(turn.Prompt) == active.Text) {
			return true
		}
		newResponse = newResponse || turn.Seq > active.PreviousSeq
	}
	return row.Projections.AsOfSeq > active.PreviousSeq &&
		(active.ObservedRunning || (newResponse && row.Projections.Values.SessionStats.Turns > active.PreviousTurns))
}

func promptArgs(id, session, text string) map[string]any {
	return map[string]any{"request": map[string]any{
		"requestId": id, "sessionId": session, "mode": "queue",
		"content":        []map[string]any{{"type": "text", "text": text}},
		"clientTimeZone": clientTimeZone(),
	}}
}

func (a *App) updateQueuedMessage(active *activePrompt, status, reason string) {
	a.updateMessage(ChatMessage{
		ID: active.ID, Text: active.Text, At: active.At, SessionID: active.Session,
		Status: status, Error: reason, Kind: "input",
	})
}

func (c *DSHClient) finishQueued(active *activePrompt, reason string) {
	a := c.app
	a.mu.Lock()
	next := a.state
	next.ActivePrompt = nil
	next.QueueBatch = len(next.Queue) > 0
	next.Messages = slices.Clone(next.Messages)
	for i := range next.Messages {
		if next.Messages[i].ID != active.ID {
			continue
		}
		if reason == "" {
			next.Messages[i].Status = "sent"
		} else {
			next.Messages[i].Status, next.Messages[i].Error = "failed", reason
		}
		break
	}
	if reason == "" {
		next.Events = append(slices.Clone(next.Events), HistoryEvent{
			ID: uuid.NewString(), Kind: "input", Actor: "user", Title: "发送到 DSH",
			Detail: truncateRunes(active.Text, 300), At: time.Now().UnixMilli(),
			SourceMessageID: active.ID, OriginalText: truncateRunes(active.Text, 500),
		})
	}
	err := a.persistLocked(next)
	a.mu.Unlock()
	if err == nil {
		a.emitState()
		a.emit("lan", a.bindingLANState())
		if s := a.getLAN(); s != nil {
			s.signal()
		}
		c.Wake()
	}
}

func compareMessages(a, b ChatMessage) int {
	if a.At < b.At {
		return -1
	}
	if a.At > b.At {
		return 1
	}
	return strings.Compare(a.ID, b.ID)
}
