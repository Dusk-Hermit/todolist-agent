package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type dshHistoryRecord struct {
	Event struct {
		Type string          `json:"type"`
		Seq  int             `json:"seq"`
		Data json.RawMessage `json:"data"`
	} `json:"event"`
}

type dshHistoryPage struct {
	Records []dshHistoryRecord `json:"records"`
	HasMore bool               `json:"hasMore"`
}

// Cache the read-only history for the current projection sequence so that
// multiple pending prompts do not each fetch the same session events.
type dshQueueHistory struct {
	session     string
	sequence    int
	before      int
	pages       int
	more        bool
	unsupported bool
	failedAt    time.Time
	records     []dshHistoryRecord
}

type promptOutcome struct {
	status   string
	detail   string
	response string
	stage    string
	evidence string
}

func (c *DSHClient) queuedResult(ctx context.Context, prompt *activePrompt, row *sessionRow) promptOutcome {
	if c.endpoint().plugin {
		return c.connectorResult(ctx, prompt)
	}
	if row == nil || row.Projections.AsOfSeq <= prompt.PreviousSeq {
		return queuedProjectionResult(prompt, row)
	}

	if c.queueHistory == nil {
		c.queueHistory = make(map[string]*dshQueueHistory)
	}
	h := c.queueHistory[row.SessionID]
	if h == nil || (h.sequence != row.Projections.AsOfSeq && (!h.more || h.pages >= 32 || h.before <= prompt.PreviousSeq)) {
		h = &dshQueueHistory{session: row.SessionID, sequence: row.Projections.AsOfSeq, more: true}
		c.queueHistory[row.SessionID] = h
	}
	for fetched := 0; ; fetched++ {
		if outcome := promptResultInHistory(prompt, h.records); outcome.status != "" {
			return outcome
		}
		if h.unsupported {
			return queuedProjectionResult(prompt, row)
		}
		if fetched >= 2 || !h.more || h.pages >= 32 || (h.before > 0 && h.before <= prompt.PreviousSeq) ||
			time.Since(h.failedAt) < 10*time.Second {
			return promptOutcome{}
		}
		request := map[string]any{
			"address":     map[string]string{"kind": "session", "sessionId": h.session},
			"throughSeq":  h.sequence,
			"maxMessages": 40,
		}
		if h.before > 0 {
			request["beforeSeq"] = h.before
		}
		pageCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		raw, err := c.call(pageCtx, "session/events", map[string]any{"request": request})
		cancel()
		if err != nil && (strings.HasPrefix(err.Error(), "DSH 返回 HTTP 404:") ||
			strings.HasPrefix(err.Error(), "METHOD_NOT_FOUND:")) {
			h.unsupported = true
			return queuedProjectionResult(prompt, row)
		}
		var page dshHistoryPage
		if err != nil || json.Unmarshal(raw, &page) != nil {
			h.failedAt = time.Now()
			return promptOutcome{}
		}
		if len(page.Records) == 0 {
			h.more = false
			return promptOutcome{}
		}
		oldest := page.Records[0].Event.Seq
		if oldest <= 0 || (h.before > 0 && oldest >= h.before) {
			h.more = false
			return promptOutcome{}
		}
		h.before, h.more = oldest, page.HasMore
		h.pages++
		h.records = append(page.Records, h.records...)
	}
}

func (c *DSHClient) connectorResult(ctx context.Context, prompt *activePrompt) promptOutcome {
	statusCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	raw, err := c.call(statusCtx, "todolist/status", map[string]any{
		"request": map[string]string{"sessionId": prompt.Session, "requestId": prompt.ID},
	})
	var receipt struct {
		Stage    string `json:"stage"`
		Reason   string `json:"reason"`
		Response string `json:"response"`
		Turn     int    `json:"turn"`
		Tools    []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"tools"`
	}
	if err != nil || json.Unmarshal(raw, &receipt) != nil {
		return promptOutcome{detail: "暂时无法读取 DSH 插件的执行记录"}
	}
	var evidence string
	if receipt.Stage == "completed" || receipt.Stage == "failed" {
		succeeded, failed, unconfirmed := 0, 0, 0
		var failures []string
		for _, tool := range receipt.Tools {
			switch tool.Status {
			case "succeeded":
				succeeded++
			case "failed":
				failed++
				if len(failures) < 5 {
					failures = append(failures, truncateRunes(tool.Name, 60))
				}
			default:
				unconfirmed++
			}
		}
		evidence = fmt.Sprintf("第 %d 轮 · 工具调用成功 %d、失败 %d、未确认 %d", receipt.Turn, succeeded, failed, unconfirmed)
		if len(failures) > 0 {
			evidence += " · 失败工具 " + strings.Join(failures, "、")
		}
	}
	switch receipt.Stage {
	case "completed":
		return promptOutcome{status: "sent", response: receipt.Response, evidence: evidence}
	case "failed":
		detail := "DSH 未正常结束本轮，请查看原会话"
		switch receipt.Reason {
		case "aborted", "cancelled", "canceled":
			detail = "DSH 已取消执行，可能已完成部分操作"
		case "interrupted":
			detail = "DSH 在本轮结束前中断，可能已完成部分操作"
		}
		return promptOutcome{status: "failed", detail: detail, evidence: evidence}
	case "session-not-found":
		return promptOutcome{stage: receipt.Stage, detail: "当前 DSH 配置中找不到原会话"}
	case "not-found":
		return promptOutcome{stage: receipt.Stage, detail: "原会话中尚未找到这条消息的执行记录"}
	default:
		return promptOutcome{stage: receipt.Stage}
	}
}

type dshHistoryContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func promptResultInHistory(prompt *activePrompt, records []dshHistoryRecord) promptOutcome {
	marker := "<!-- todo-msg:" + prompt.ID + " -->"
	turn, matched := 0, false
	response := ""
	for _, record := range records {
		event := record.Event
		switch event.Type {
		case "turn/start", "user/message", "assistant/message", "turn/end":
		default:
			continue
		}
		var data struct {
			Turn    int                 `json:"turn"`
			Content []dshHistoryContent `json:"content"`
			Message struct {
				Content []dshHistoryContent `json:"content"`
			} `json:"message"`
			Reason struct {
				Kind string `json:"kind"`
			} `json:"reason"`
		}
		if err := json.Unmarshal(event.Data, &data); err != nil {
			continue
		}
		switch event.Type {
		case "turn/start":
			turn, matched, response = data.Turn, false, ""
		case "user/message":
			if turn != 0 && event.Seq > prompt.PreviousSeq {
				for _, part := range data.Content {
					if part.Type == "text" && strings.HasSuffix(strings.TrimSpace(part.Text), marker) {
						matched = true
						break
					}
				}
			}
		case "assistant/message":
			if matched && data.Turn == turn {
				var text strings.Builder
				for _, part := range data.Message.Content {
					if part.Type == "text" {
						text.WriteString(part.Text)
					}
				}
				if text.Len() > 0 {
					response = text.String()
				}
			}
		case "turn/end":
			if data.Turn == turn {
				if matched {
					switch data.Reason.Kind {
					case "completed":
						return promptOutcome{status: "sent", response: response}
					case "aborted", "cancelled", "canceled":
						return promptOutcome{status: "failed", detail: "DSH 已取消执行，可能已完成部分操作"}
					case "interrupted":
						return promptOutcome{status: "failed", detail: "DSH 执行中断，可能已完成部分操作"}
					case "error", "blocked", "max-tokens":
						return promptOutcome{status: "failed", detail: "DSH 未正常完成执行，请查看原会话"}
					}
				}
				turn, matched, response = 0, false, ""
			}
		}
	}
	return promptOutcome{}
}
