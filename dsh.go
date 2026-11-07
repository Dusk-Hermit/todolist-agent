package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type sessionRow struct {
	SessionID      string `json:"sessionId"`
	UpdatedAt      int64  `json:"updatedAt"`
	AgentAvailable bool   `json:"agentAvailable"`
	Running        bool   `json:"running"`
	Blank          bool   `json:"blank"`
	CWD            string `json:"cwd"`
	Projections    struct {
		Kind    string `json:"kind"`
		AsOfSeq int    `json:"asOfSeq"`
		Values  struct {
			Title       *string `json:"title"`
			TurnOutline []struct {
				Turn     int    `json:"turn"`
				Seq      int    `json:"seq"`
				Prompt   string `json:"prompt"`
				Response string `json:"response"`
			} `json:"turnOutline"`
			SessionStats struct {
				Turns int `json:"turns"`
				Steps int `json:"steps"`
			} `json:"sessionStats"`
			TokenUsage struct {
				UncachedInputTokens int64 `json:"uncachedInputTokens"`
				OutputTokens        int64 `json:"outputTokens"`
				CacheReadTokens     int64 `json:"cacheReadTokens"`
				CacheWriteTokens    int64 `json:"cacheWriteTokens"`
			} `json:"tokenUsage"`
		} `json:"values"`
	} `json:"projections"`
}

type rpcError struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Details json.RawMessage `json:"details"`
}

func (e *rpcError) Error() string {
	return e.Code + ": " + e.Message
}

type rpcEnvelope struct {
	Type   string `json:"type"`
	RpcID  string `json:"rpcId"`
	Result struct {
		OK    bool            `json:"ok"`
		Value json.RawMessage `json:"value"`
		Error *rpcError       `json:"error"`
	} `json:"result"`
}

type DSHClient struct {
	app  *App
	http *http.Client

	mu   sync.Mutex
	conn dshEndpoint

	reqMu  sync.Mutex
	wake   chan struct{}
	cancel context.CancelFunc

	watchID     string
	watchCancel context.CancelFunc
}

func newDSHClient(a *App) *DSHClient {
	return &DSHClient{
		app: a,
		http: &http.Client{
			Timeout: 25 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
			Transport: &http.Transport{
				Proxy:                 nil,
				MaxIdleConns:          8,
				IdleConnTimeout:       60 * time.Second,
				ResponseHeaderTimeout: 20 * time.Second,
			},
		},
		wake: make(chan struct{}, 4),
	}
}

func (c *DSHClient) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	go c.loop(ctx)
}

func (c *DSHClient) Stop() {
	c.mu.Lock()
	cancel := c.cancel
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (c *DSHClient) Wake() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (c *DSHClient) loop(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		if err := c.connect(ctx); err != nil {
			c.reportError(err)
			if !sleepOrWake(ctx, c.wake, 6*time.Second) {
				return
			}
			continue
		}
		break
	}
	for {
		st := c.app.getStatus()
		interval := 3 * time.Second
		if st.Running || st.Sending {
			interval = 1200 * time.Millisecond
		}
		c.app.mu.Lock()
		if c.app.state.ActivePrompt == nil && len(c.app.state.Queue) > 0 {
			if remaining := time.Until(time.UnixMilli(c.app.state.QueueReadyAt)); remaining > 0 {
				interval = min(interval, remaining)
			}
		}
		c.app.mu.Unlock()
		if !sleepOrWake(ctx, c.wake, interval) {
			return
		}
		if err := c.refresh(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("dsh refresh: %v", err)
			c.reportError(err)
			if !sleepOrWake(ctx, c.wake, 2500*time.Millisecond) {
				return
			}
			for {
				if ctx.Err() != nil {
					return
				}
				if err := c.connect(ctx); err != nil {
					c.reportError(err)
					if !sleepOrWake(ctx, c.wake, 6*time.Second) {
						return
					}
					continue
				}
				break
			}
		}
	}
}

func sleepOrWake(ctx context.Context, wake <-chan struct{}, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-wake:
		return true
	case <-t.C:
		return true
	}
}

func (c *DSHClient) setConnecting() {
	st := c.app.getStatus()
	st.Connecting = true
	st.Connected = false
	st.Armed = false
	st.Error = ""
	c.app.setStatus(st)
	c.app.emitStatus()
}

func (c *DSHClient) reportError(err error) {
	if err == nil {
		return
	}
	st := c.app.getStatus()
	st.Connected = false
	st.Connecting = false
	st.Error = err.Error()
	st.Running = false
	st.RunningSince = 0
	st.Sending = false
	st.Armed = false
	st.CheckedAt = time.Now().UnixMilli()
	if c.app.setStatus(st) {
		c.app.emitStatus()
	}
	c.expireActivePrompt("正在等待 DSH 重连")
}

func (c *DSHClient) connect(ctx context.Context) error {
	c.setConnecting()
	conn, err := readDSHConnector()
	if err != nil {
		return err
	}
	items, err := c.listAt(ctx, conn, 8*time.Second)
	if err != nil {
		// Backend initialization can fail while the connector still serves
		// receipts for work already assigned to an earlier Session.
		c.mu.Lock()
		c.conn = conn
		c.mu.Unlock()
		c.watchPrompt(ctx, nil)
		c.pumpQueue(ctx, nil, nil)
		return fmt.Errorf("无法连接 DSH 插件，请确认已启动安装插件的 DSH：%w", err)
	}
	c.activate(ctx, conn, items)
	return nil
}

func (c *DSHClient) activate(ctx context.Context, conn dshEndpoint, items []sessionRow) {
	c.watchPrompt(ctx, nil)
	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	a := c.app
	a.mu.Lock()
	a.state.DSH.BaseURL = conn.base
	selected := items[0].SessionID
	a.state.DSH.SessionID = selected
	a.state.QueueReadyAt = max(a.state.QueueReadyAt, time.Now().Add(promptCollectTime).UnixMilli())
	a.mu.Unlock()
	a.markDirty()

	opts := buildSessionOptions(items)
	a.setSessions(opts)

	st := a.getStatus()
	st.Connected = true
	st.Connecting = false
	st.Error = ""
	st.Armed = false
	st.BaseURL = conn.base
	st.SessionID = selected
	if row := findSession(items, selected); row != nil {
		st.SessionTitle = sessionTitle(row)
		st.CWD = row.CWD
	}
	st.CheckedAt = time.Now().UnixMilli()
	a.setStatus(st)
	a.emit("sessions", opts)
	a.emitStatus()
	a.emitState()
	a.toast("success", "已连接 DeepSeek Harness")
}

func buildSessionOptions(items []sessionRow) []SessionOption {
	opts := make([]SessionOption, 0, len(items))
	for i := range items {
		it := &items[i]
		opts = append(opts, SessionOption{
			ID:        it.SessionID,
			Title:     sessionTitle(it),
			CWD:       it.CWD,
			UpdatedAt: it.UpdatedAt,
			Running:   it.Running,
			Blank:     it.Blank,
		})
	}
	sortSessions(opts)
	if len(opts) > 60 {
		opts = opts[:60]
	}
	return opts
}

func sessionTitle(row *sessionRow) string {
	if row.Projections.Values.Title != nil {
		if t := strings.TrimSpace(*row.Projections.Values.Title); t != "" {
			return t
		}
	}
	for i := len(row.Projections.Values.TurnOutline) - 1; i >= 0; i-- {
		if p := strings.TrimSpace(row.Projections.Values.TurnOutline[i].Prompt); p != "" {
			return truncateRunes(p, 42)
		}
	}
	if row.Blank {
		return "新会话"
	}
	return "未命名会话"
}

func findSession(items []sessionRow, id string) *sessionRow {
	for i := range items {
		if items[i].SessionID == id {
			return &items[i]
		}
	}
	return nil
}

func (c *DSHClient) refresh(ctx context.Context) error {
	conn := c.endpoint()
	items, err := c.listAt(ctx, conn, 20*time.Second)
	if err != nil {
		return err
	}
	a := c.app
	opts := buildSessionOptions(items)
	a.setSessions(opts)
	a.emit("sessions", opts)

	row := &items[0]
	a.mu.Lock()
	if a.state.DSH.SessionID != row.SessionID {
		a.state.DSH.SessionID = row.SessionID
		a.dirty = true
	}
	a.mu.Unlock()

	prev := a.getStatus()
	st := prev
	st.Connected = true
	st.Connecting = false
	st.Error = ""
	st.BaseURL = conn.base
	st.SessionID = ""
	st.CheckedAt = time.Now().UnixMilli()
	st.Running = false
	st.RunningSince = 0
	st.Turns = 0
	st.Steps = 0
	st.Tokens = 0
	st.UpdatedAt = 0
	st.LastPrompt = ""
	st.LastResponse = ""
	if row != nil {
		st.SessionID = row.SessionID
		st.SessionTitle = sessionTitle(row)
		st.CWD = row.CWD
		st.Running = row.Running
		st.UpdatedAt = row.UpdatedAt
		st.Turns = row.Projections.Values.SessionStats.Turns
		st.Steps = row.Projections.Values.SessionStats.Steps
		st.Tokens = row.Projections.Values.TokenUsage.OutputTokens
		if row.Running {
			if prev.Running && prev.RunningSince > 0 {
				st.RunningSince = prev.RunningSince
			} else {
				st.RunningSince = time.Now().UnixMilli()
			}
		} else {
			st.RunningSince = 0
		}
		if len(row.Projections.Values.TurnOutline) > 0 {
			last := row.Projections.Values.TurnOutline[len(row.Projections.Values.TurnOutline)-1]
			st.LastPrompt = truncateRunes(strings.TrimSpace(last.Prompt), 220)
			resp := strings.TrimSpace(last.Response)
			if resp != "" {
				st.LastResponse = truncateRunes(resp, 500)
			}
		}
	}

	armed := prev.Armed && prev.Connected && prev.SessionID == st.SessionID
	st.Armed = true
	changed := a.setStatus(st)
	if changed {
		a.emitStatus()
	}
	if armed {
		if !prev.Running && st.Running {
			a.recordEvent(HistoryEvent{
				ID: uuid.NewString(), Kind: "dsh.start", Actor: "system",
				Title: "DSH 开始处理", Detail: firstLine(st.LastPrompt), At: time.Now().UnixMilli(),
			})
			a.emitState()
			a.toast("info", "DSH 开始处理")
		}
		if prev.Running && !st.Running {
			a.recordEvent(HistoryEvent{
				ID: uuid.NewString(), Kind: "dsh.done", Actor: "system",
				Title: "DSH 本轮完成", Detail: "本轮工作已完成", At: time.Now().UnixMilli(),
			})
			a.emitState()
			a.toast("success", "DSH 已完成这一轮")
		}
		if st.LastResponse != "" && st.LastResponse != prev.LastResponse && !st.Running {
			a.recordEvent(HistoryEvent{
				ID: uuid.NewString(), Kind: "dsh.reply", Actor: "agent",
				Title: "DSH 回复", Detail: st.LastResponse, At: time.Now().UnixMilli(),
			})
			a.emitState()
		}
	}
	c.pumpQueue(ctx, row, items)
	return nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	return truncateRunes(s, 120)
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func (c *DSHClient) endpoint() dshEndpoint {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn
}

func (c *DSHClient) listAt(ctx context.Context, conn dshEndpoint, timeout time.Duration) ([]sessionRow, error) {
	c.reqMu.Lock()
	defer c.reqMu.Unlock()
	raw, _, err := c.invoke(ctx, conn, "todolist/backend", c.backendArgs(), timeout)
	if err != nil {
		return nil, err
	}
	var out struct {
		Items   []sessionRow `json:"items"`
		Backend struct {
			Phase  string `json:"phase"`
			Reason string `json:"reason"`
		} `json:"backend"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("解析会话列表失败: %w", err)
	}
	if len(out.Items) != 1 || out.Items[0].SessionID == "" {
		return nil, fmt.Errorf("DSH 插件没有返回处理会话，请升级插件")
	}
	if out.Backend.Phase == "failed" {
		return nil, fmt.Errorf("DSH 后端初始化失败（%s），请在 DSH 插件页新建处理会话", out.Backend.Reason)
	}
	return out.Items, nil
}

func (c *DSHClient) call(ctx context.Context, method string, args any) (json.RawMessage, error) {
	c.reqMu.Lock()
	defer c.reqMu.Unlock()
	raw, _, err := c.invoke(ctx, c.endpoint(), method, args, 25*time.Second)
	return raw, err
}

func (c *DSHClient) invoke(ctx context.Context, conn dshEndpoint, method string, args any, timeout time.Duration) (json.RawMessage, int, error) {
	if conn.base == "" {
		return nil, 0, fmt.Errorf("DSH 尚未连接")
	}
	return c.callConnector(ctx, conn, method, args, timeout)
}

func decodeDSHResult(data []byte, status int) (json.RawMessage, int, error) {
	var env rpcEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, status, fmt.Errorf("解析 DSH 响应失败: %w", err)
	}
	if !env.Result.OK {
		if env.Result.Error != nil {
			return nil, status, env.Result.Error
		}
		return nil, status, fmt.Errorf("DSH 调用失败")
	}
	return env.Result.Value, status, nil
}

func clientTimeZone() string {
	if tz := strings.TrimSpace(os.Getenv("DSH_CLIENT_TIMEZONE")); tz != "" {
		return tz
	}
	return "Asia/Shanghai"
}

func (c *DSHClient) backendArgs() map[string]any {
	request := map[string]any{"clientID": c.app.replicaSnapshot().Device}
	if c.app.bridge != nil {
		request["url"] = c.app.bridge.URL()
		request["token"] = c.app.bridge.Token()
	}
	return map[string]any{"request": request}
}

func (c *DSHClient) preparePrompt(ctx context.Context, id string) (string, error) {
	raw, err := c.call(ctx, "todolist/prepare", map[string]any{"request": map[string]string{
		"clientID": c.app.replicaSnapshot().Device, "requestId": id,
	}})
	if err != nil {
		return "", err
	}
	var reply struct {
		SessionID string `json:"sessionId"`
	}
	if json.Unmarshal(raw, &reply) != nil || reply.SessionID == "" {
		return "", fmt.Errorf("DSH 插件未分配处理会话")
	}
	return reply.SessionID, nil
}
