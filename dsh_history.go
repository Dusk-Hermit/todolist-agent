package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type promptOutcome struct {
	status   string
	detail   string
	response string
	stage    string
	evidence string
}

// One independent request can await DSH events while the normal poller still
// serves the UI and handles reconnection to older connector versions.
func (c *DSHClient) watchPrompt(ctx context.Context, active *activePrompt) {
	if active != nil && c.watchID == active.ID {
		return
	}
	if c.watchCancel != nil {
		c.watchCancel()
		c.watchCancel = nil
	}
	c.watchID = ""
	if active == nil || c.endpoint().base == "" {
		return
	}
	ctx, c.watchCancel = context.WithCancel(ctx)
	c.watchID = active.ID
	args := promptStatusArgs(active)
	go func() {
		for ctx.Err() == nil {
			raw, _, err := c.invoke(ctx, c.endpoint(), "todolist/wait", args, 11*time.Second)
			if err != nil || ctx.Err() != nil {
				return
			}
			outcome := parseConnectorResult(raw)
			if outcome.status != "" {
				c.finishQueued(active, outcome)
				return
			}
			switch outcome.stage {
			case "queued", "accepted", "running":
			default:
				return
			}
		}
	}()
}

func promptStatusArgs(prompt *activePrompt) map[string]any {
	return map[string]any{
		"request": map[string]string{"sessionId": prompt.Session, "requestId": prompt.ID},
	}
}

func (c *DSHClient) connectorResult(ctx context.Context, prompt *activePrompt) promptOutcome {
	statusCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	raw, err := c.call(statusCtx, "todolist/status", promptStatusArgs(prompt))
	if err != nil {
		return promptOutcome{detail: "暂时无法读取 DSH 插件的执行记录"}
	}
	return parseConnectorResult(raw)
}

func parseConnectorResult(raw json.RawMessage) promptOutcome {
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
	if json.Unmarshal(raw, &receipt) != nil {
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
