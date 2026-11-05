package main

import (
	"context"
	"encoding/json"
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
	session  string
	sequence int
	before   int
	pages    int
	more     bool
	failedAt time.Time
	records  []dshHistoryRecord
}

func (c *DSHClient) queuedResult(ctx context.Context, prompt *activePrompt, row *sessionRow) (string, bool) {
	if response, ok := queuedPromptResult(prompt, row); ok {
		return response, true
	}
	if row == nil || row.Running || row.Projections.AsOfSeq <= prompt.PreviousSeq {
		return "", false
	}

	h := &c.queueHistory
	if h.session != row.SessionID || h.sequence != row.Projections.AsOfSeq {
		*h = dshQueueHistory{session: row.SessionID, sequence: row.Projections.AsOfSeq, more: true}
	}
	for {
		if response, ok := completedPromptInHistory(prompt, h.records); ok {
			return response, true
		}
		if !h.more || h.pages >= 4 || (h.before > 0 && h.before <= prompt.PreviousSeq+1) ||
			time.Since(h.failedAt) < 10*time.Second {
			return "", false
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
		var page dshHistoryPage
		if err != nil || json.Unmarshal(raw, &page) != nil {
			h.failedAt = time.Now()
			return "", false
		}
		if len(page.Records) == 0 {
			h.more = false
			return "", false
		}
		oldest := page.Records[0].Event.Seq
		if oldest <= 0 || (h.before > 0 && oldest >= h.before) {
			h.more = false
			return "", false
		}
		h.before, h.more = oldest, page.HasMore
		h.pages++
		h.records = append(page.Records, h.records...)
	}
}

type dshHistoryContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func completedPromptInHistory(prompt *activePrompt, records []dshHistoryRecord) (string, bool) {
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
				if matched && data.Reason.Kind == "completed" {
					return response, true
				}
				turn, matched, response = 0, false, ""
			}
		}
	}
	return "", false
}
