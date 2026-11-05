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
	Host string `json:"host,omitempty"`
}

type activePrompt struct {
	queuedPrompt
	Session     string `json:"session"`
	Started     int64  `json:"started"`
	PreviousSeq int    `json:"previousSeq"`
	Accepted    bool   `json:"accepted,omitempty"`
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
	journalIDs := make(map[string]bool, len(next.Messages))
	if a.dsh != nil {
		for _, m := range next.Messages {
			journalIDs[m.ID] = true
		}
		next.Messages = slices.Clone(next.Messages)
	}
	changed := false
	for _, p := range prompts {
		if next.QueueReceipts[p.ID] || journalIDs[p.ID] {
			continue
		}
		next.Queue[p.ID] = p
		next.QueueReceipts[p.ID] = true
		changed = true
		if a.dsh != nil {
			next.Messages = append(next.Messages, ChatMessage{ID: p.ID, Text: p.Text, At: p.At,
				Kind: "input", Status: "queued"})
		}
	}
	if !changed {
		a.mu.Unlock()
		return nil
	}
	if a.dsh != nil && len(a.state.Queue) == 0 && a.state.ActivePrompt == nil {
		next.QueueReadyAt = time.Now().Add(promptCollectTime).UnixMilli()
	}
	if a.dsh != nil {
		slices.SortFunc(next.Messages, compareMessages)
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

// A timed-out delivery can have been committed. Persist a single execution
// host before sending; a different host can only take fresh prompts.
func (a *App) assignPrompts(host string, includeFresh bool) ([]queuedPrompt, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var result []queuedPrompt
	next := a.state
	changed := false
	for _, p := range orderedQueue(a.state.Queue) {
		if p.Host == host {
			result = append(result, p)
			continue
		}
		if p.Host != "" || !includeFresh {
			continue
		}
		if !changed {
			next.Queue = maps.Clone(next.Queue)
			changed = true
		}
		p.Host = host
		next.Queue[p.ID] = p
		result = append(result, p)
	}
	if changed {
		if err := a.persistLocked(next); err != nil {
			return nil, err
		}
	}
	return result, nil
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
		if next.Queue[p.ID].Host == p.Host {
			delete(next.Queue, p.ID)
		}
	}
	return a.persistLocked(next)
}

func (a *App) batchPausedLocked() bool {
	return a.dsh != nil && (a.state.ActivePrompt != nil ||
		(a.status.Connected && a.status.SessionID != "" && a.state.QueueBatch && len(a.state.Queue) > 0))
}

func (c *DSHClient) pumpQueue(ctx context.Context, selected *sessionRow, sessions []sessionRow) {
	if c.recheckHistory.Swap(false) {
		clear(c.queueHistory)
	}
	a := c.app
	a.mu.Lock()
	active := a.state.ActivePrompt
	if active != nil {
		a.mu.Unlock()
		row := findSession(sessions, active.Session)
		if outcome := c.queuedResult(ctx, active, row); outcome.status != "" {
			c.finishQueued(active, outcome.status, outcome.detail, outcome.response)
		} else if row == nil {
			c.expireActivePrompt("原 DSH 会话当前不可用")
		} else if !row.Running {
			c.expireActivePrompt("原 DSH 会话中尚未找到执行结果")
		}
		return
	}
	if selected == nil || selected.Running || len(a.state.Queue) == 0 ||
		time.Now().UnixMilli() < a.state.QueueReadyAt || a.quitting {
		a.mu.Unlock()
		c.reconcileUncertain(ctx, sessions)
		return
	}
	item := orderedQueue(a.state.Queue)[0]
	active = &activePrompt{queuedPrompt: item, Session: selected.SessionID,
		Started: time.Now().UnixMilli(), PreviousSeq: selected.Projections.AsOfSeq}
	next := a.state
	next.Queue = maps.Clone(next.Queue)
	delete(next.Queue, item.ID)
	next.ActivePrompt = active
	next.QueueBatch = true
	message := ChatMessage{
		ID: item.ID, Text: item.Text, At: item.At, Status: "sending", Kind: "input", SessionID: active.Session,
	}
	next.Messages = slices.Clone(next.Messages)
	if i := slices.IndexFunc(next.Messages, func(m ChatMessage) bool { return m.ID == item.ID }); i >= 0 {
		next.Messages[i] = message
	} else {
		next.Messages = append(next.Messages, message)
	}
	slices.SortFunc(next.Messages, compareMessages)
	err := a.persistLocked(next)
	a.mu.Unlock()
	if err != nil {
		return
	}
	a.emitState()

	raw, err := c.call(ctx, "session/prompt", promptArgs(item.ID, active.Session,
		todoPromptText(item.Text, item.ID)))
	if err != nil {
		// The request may have reached DSH before a transport failure.
		// Keep the claim and reconcile it from session activity.
		c.Wake()
		return
	}
	var reply struct {
		Accepted *bool `json:"accepted"`
	}
	if json.Unmarshal(raw, &reply) != nil || reply.Accepted == nil {
		c.Wake()
		return
	}
	if !*reply.Accepted {
		c.finishQueued(active, "failed", "DSH 未接受消息", "")
		return
	}
	a.markPromptAccepted(active)
	c.Wake()
}

func queuedProjectionResult(active *activePrompt, row *sessionRow) promptOutcome {
	if row == nil || row.Running {
		return promptOutcome{}
	}
	for _, turn := range row.Projections.Values.TurnOutline {
		if turn.Response != "" && turn.Seq > active.PreviousSeq &&
			strings.HasSuffix(strings.TrimSpace(turn.Prompt), "<!-- todo-msg:"+active.ID+" -->") {
			return promptOutcome{status: "sent", response: turn.Response}
		}
	}
	return promptOutcome{}
}

func (c *DSHClient) expireActivePrompt(reason string) {
	a := c.app
	a.mu.Lock()
	active := a.state.ActivePrompt
	a.mu.Unlock()
	if active != nil && time.Since(time.UnixMilli(active.Started)) > 90*time.Second {
		stage := "发送后未收到确认"
		if active.Accepted {
			stage = "DSH 已接收"
		}
		c.finishQueued(active, "uncertain", stage+"，"+reason+"。重新连接后继续核对。", "")
	}
}

func (c *DSHClient) reconcileUncertain(ctx context.Context, sessions []sessionRow) {
	a := c.app
	a.mu.Lock()
	pending := maps.Clone(a.state.UncertainPrompts)
	a.mu.Unlock()
	sessionsWithPending := make(map[string]bool, len(pending))
	for _, prompt := range pending {
		if outcome := c.queuedResult(ctx, &prompt, findSession(sessions, prompt.Session)); outcome.status != "" {
			c.finishQueued(&prompt, outcome.status, outcome.detail, outcome.response)
		} else {
			sessionsWithPending[prompt.Session] = true
		}
	}
	for session := range c.queueHistory {
		if !sessionsWithPending[session] {
			delete(c.queueHistory, session)
		}
	}
}

func promptArgs(id, session, text string) map[string]any {
	return map[string]any{"request": map[string]any{
		"requestId": id, "sessionId": session, "mode": "queue",
		"content":        []map[string]any{{"type": "text", "text": text}},
		"clientTimeZone": clientTimeZone(),
	}}
}

func todoPromptText(text, id string) string {
	return text + "\n\n<!-- TodoList 排序规则：" + todoPlacementRule + " -->\n<!-- todo-msg:" + id + " -->"
}

func (a *App) markPromptAccepted(active *activePrompt) {
	a.mu.Lock()
	next := a.state
	if next.ActivePrompt == nil || next.ActivePrompt.ID != active.ID {
		a.mu.Unlock()
		return
	}
	accepted := *next.ActivePrompt
	accepted.Accepted = true
	next.ActivePrompt = &accepted
	next.Messages = slices.Clone(next.Messages)
	for i := range next.Messages {
		if next.Messages[i].ID == active.ID {
			next.Messages[i].Status = "accepted"
			break
		}
	}
	err := a.persistLocked(next)
	a.mu.Unlock()
	if err == nil {
		a.emitState()
	}
}

// Terminal evidence is append-only. A stale peer can publish a newer copy of
// the message record, but the execution outcome still wins.
func restorePromptCompletions(st *persistedState) bool {
	outcomes := make(map[string]promptOutcome)
	for _, event := range st.Events {
		id := event.SourceMessageID
		if id == "" {
			continue
		}
		if event.Kind == "input" || (event.Kind == "dsh.reply" && event.ID == "prompt-reply-"+id) {
			outcomes[id] = promptOutcome{status: "sent"}
		} else if event.Kind == "dsh.prompt_end" && event.ID == "prompt-end-"+id && outcomes[id].status != "sent" {
			outcomes[id] = promptOutcome{status: "failed", detail: event.Detail}
		}
	}
	changed := false
	for i := range st.Messages {
		m := &st.Messages[i]
		if outcome, ok := outcomes[m.ID]; ok && (m.Status != outcome.status || m.Error != outcome.detail) {
			m.Status, m.Error = outcome.status, outcome.detail
			changed = true
		}
	}
	for id := range st.UncertainPrompts {
		if _, settled := outcomes[id]; settled {
			delete(st.UncertainPrompts, id)
			changed = true
		}
	}
	if st.ActivePrompt != nil && outcomes[st.ActivePrompt.ID].status != "" {
		st.ActivePrompt = nil
		st.QueueBatch = len(st.Queue) != 0
		changed = true
	}
	return changed
}

// Older builds could keep the journal entry without its local reconciliation
// tracker. Only reconstruct work for which this device has a durable receipt.
func recoverPromptTracking(st *persistedState) bool {
	changed := false
	for i := range st.Messages {
		m := &st.Messages[i]
		if st.ActivePrompt != nil && st.ActivePrompt.ID == m.ID {
			if m.Status == "sent" {
				st.ActivePrompt.Accepted, m.Status = true, "accepted"
				changed = true
			}
			continue
		}
		if !st.QueueReceipts[m.ID] || m.SessionID == "" || m.Kind != "input" {
			continue
		}
		if _, queued := st.Queue[m.ID]; queued {
			continue
		}
		if _, tracked := st.UncertainPrompts[m.ID]; tracked {
			continue
		}
		if m.Status != "uncertain" && m.Status != "sending" && m.Status != "accepted" {
			continue
		}
		if st.UncertainPrompts == nil {
			st.UncertainPrompts = make(map[string]activePrompt)
		}
		st.UncertainPrompts[m.ID] = activePrompt{
			queuedPrompt: queuedPrompt{ID: m.ID, Text: m.Text, At: m.At},
			Session:      m.SessionID, Started: m.At, Accepted: m.Status == "accepted",
		}
		m.Status, m.Error = "uncertain", "正在原 DSH 会话中查找执行记录"
		changed = true
	}
	return changed
}

func (c *DSHClient) finishQueued(active *activePrompt, status, reason, response string) {
	a := c.app
	a.mu.Lock()
	next := a.state
	if next.ActivePrompt != nil && next.ActivePrompt.ID == active.ID {
		next.ActivePrompt = nil
		next.QueueBatch = len(next.Queue) > 0
	} else if _, tracked := next.UncertainPrompts[active.ID]; !tracked {
		a.mu.Unlock()
		return
	}
	next.UncertainPrompts = maps.Clone(next.UncertainPrompts)
	if status == "uncertain" {
		if next.UncertainPrompts == nil {
			next.UncertainPrompts = make(map[string]activePrompt)
		}
		next.UncertainPrompts[active.ID] = *active
	} else {
		delete(next.UncertainPrompts, active.ID)
	}
	next.Messages = slices.Clone(next.Messages)
	for i := range next.Messages {
		if next.Messages[i].ID != active.ID {
			continue
		}
		next.Messages[i].Status, next.Messages[i].Error = status, reason
		break
	}
	if status == "sent" {
		next.Events = append(slices.Clone(next.Events), HistoryEvent{
			ID: "prompt-input-" + active.ID, Kind: "input", Actor: "user", Title: "发送到 DSH",
			Detail: truncateRunes(active.Text, 300), At: time.Now().UnixMilli(),
			SourceMessageID: active.ID, OriginalText: truncateRunes(active.Text, 500),
		})
		if response != "" {
			next.Events = append(next.Events, HistoryEvent{
				ID: "prompt-reply-" + active.ID, Kind: "dsh.reply", Actor: "agent", Title: "DSH 回复",
				Detail: truncateRunes(response, 2000), At: time.Now().UnixMilli(), SourceMessageID: active.ID,
			})
		}
	} else if status == "failed" {
		next.Events = append(slices.Clone(next.Events), HistoryEvent{
			ID: "prompt-end-" + active.ID, Kind: "dsh.prompt_end", Actor: "system", Title: "DSH 未完成消息",
			Detail: reason, At: time.Now().UnixMilli(), SourceMessageID: active.ID,
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
