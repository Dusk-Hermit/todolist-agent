package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

var errUnauthorized = errors.New("dsh web authentication failed")

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

	mu       sync.Mutex
	baseURL  string
	secret   []byte
	lastScan time.Time

	reqMu  sync.Mutex
	wake   chan struct{}
	cancel context.CancelFunc

	queueHistory   map[string]*dshQueueHistory // accessed only by the DSH polling loop
	recheckHistory atomic.Bool
}

func newDSHClient(a *App) *DSHClient {
	return &DSHClient{
		app: a,
		http: &http.Client{
			Timeout: 25 * time.Second,
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

// Reconfigure applies a manual DSH address/session choice and wakes the poller.
func (c *DSHClient) Reconfigure(baseURL, sessionID string) {
	base := normalizeBase(baseURL)
	a := c.app
	a.mu.Lock()
	if base != "" {
		a.state.DSH.BaseURL = base
	}
	if sessionID != "" {
		a.state.DSH.SessionID = sessionID
	}
	a.mu.Unlock()
	c.mu.Lock()
	if base != "" {
		c.baseURL = base
	}
	c.mu.Unlock()
	a.markDirty()
	a.emitState()
	c.Wake()
}

// SelectSession changes the session the composer targets.
func (c *DSHClient) SelectSession(id string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	a := c.app
	a.mu.Lock()
	a.state.DSH.SessionID = id
	a.mu.Unlock()
	a.markDirty()
	st := a.getStatus()
	st.SessionID = id
	st.SessionTitle = ""
	st.CWD = ""
	st.Running = false
	st.RunningSince = 0
	st.Armed = false
	st.UpdatedAt = 0
	st.LastPrompt = ""
	st.LastResponse = ""
	st.Turns = 0
	st.Steps = 0
	st.Tokens = 0
	st.Error = ""
	a.setStatus(st)
	a.emitStatus()
	a.emitState()
	c.Wake()
}

// NewDedicatedSession creates a fresh DSH conversation for the desktop app.
func (c *DSHClient) NewDedicatedSession() {
	go func() {
		c.mu.Lock()
		base := c.baseURL
		secret := c.secret
		c.mu.Unlock()
		if base == "" || len(secret) == 0 {
			c.app.toast("error", "还没有连接到 DSH")
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		items, err := c.listAt(ctx, base, secret, 10*time.Second)
		if err != nil {
			c.app.toast("error", "无法读取会话列表："+err.Error())
			return
		}
		cwd := pickWorkspace(items)
		sid, err := c.createDedicated(ctx, base, secret, cwd)
		if err != nil {
			c.app.toast("error", "创建专用会话失败："+err.Error())
			return
		}
		a := c.app
		a.mu.Lock()
		a.state.DSH.SessionID = sid
		a.mu.Unlock()
		a.markDirty()
		st := a.getStatus()
		st.SessionID = sid
		st.SessionTitle = "TodoList · DSH 联动"
		st.CWD = cwd
		st.Connected = true
		st.Connecting = false
		st.Error = ""
		st.Running = false
		st.RunningSince = 0
		st.Armed = false
		st.UpdatedAt = 0
		st.LastPrompt = ""
		st.LastResponse = ""
		st.Turns = 0
		st.Steps = 0
		st.Tokens = 0
		st.CheckedAt = time.Now().UnixMilli()
		a.setStatus(st)
		a.emitStatus()
		a.emitState()
		a.toast("success", "已创建专用会话：TodoList · DSH 联动")
		c.Wake()
	}()
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
	secret, err := readDSHSecret()
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.secret = secret
	base := c.baseURL
	c.mu.Unlock()

	candidates := c.candidates(base)
	var lastErr error
	for _, cand := range candidates {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if cand == "" {
			continue
		}
		items, err := c.listAt(ctx, cand, secret, 5*time.Second)
		if err == nil {
			c.activate(ctx, cand, secret, items)
			return nil
		}
		lastErr = err
	}

	c.mu.Lock()
	scanAge := time.Since(c.lastScan)
	c.lastScan = time.Now()
	c.mu.Unlock()
	if scanAge > 10*time.Second {
		if cand := c.scan(ctx, secret); cand != "" {
			items, err := c.listAt(ctx, cand, secret, 5*time.Second)
			if err == nil {
				c.activate(ctx, cand, secret, items)
				return nil
			}
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("未找到正在运行的 DeepSeek Harness")
	}
	return lastErr
}

func (c *DSHClient) candidates(current string) []string {
	out := make([]string, 0, 4)
	add := func(s string) {
		s = normalizeBase(s)
		if s == "" {
			return
		}
		for _, v := range out {
			if v == s {
				return
			}
		}
		out = append(out, s)
	}
	if v := os.Getenv("DSH_WEB_URL"); v != "" {
		add(v)
	}
	add(current)
	c.app.mu.Lock()
	add(c.app.state.DSH.BaseURL)
	c.app.mu.Unlock()
	add("http://127.0.0.1:13848")
	return out
}

func pickWorkspace(items []sessionRow) string {
	// Prefer the workspace of the conversation the user is actively using.
	for _, it := range items {
		if it.Running && strings.TrimSpace(it.CWD) != "" {
			return it.CWD
		}
	}
	best := ""
	var bestAt int64
	for _, it := range items {
		if strings.TrimSpace(it.CWD) == "" {
			continue
		}
		if it.UpdatedAt >= bestAt {
			bestAt = it.UpdatedAt
			best = it.CWD
		}
	}
	return best
}

// createDedicated creates one ordinary DSH session, names it after this app, and
// returns its id. It is the zero-configuration path for a user who just opened
// the desktop app without a stored session.
func (c *DSHClient) createDedicated(ctx context.Context, base string, secret []byte, cwd string) (string, error) {
	cookie, err := mintCookie(base, secret)
	if err != nil {
		return "", err
	}
	request := map[string]any{}
	if strings.TrimSpace(cwd) != "" {
		request["cwd"] = cwd
	}
	raw, _, err := c.postWith(ctx, base, cookie, "session/create", map[string]any{"request": request}, 25*time.Second)
	if err != nil {
		return "", err
	}
	var out struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.SessionID == "" {
		return "", fmt.Errorf("session/create 没有返回会话 id")
	}
	_, _, _ = c.postWith(ctx, base, cookie, "session/rename", map[string]any{
		"request": map[string]any{
			"sessionId": out.SessionID,
			"title":     "TodoList · DSH 联动",
		},
	}, 15*time.Second)
	return out.SessionID, nil
}

func (c *DSHClient) activate(ctx context.Context, base string, secret []byte, items []sessionRow) {
	c.mu.Lock()
	c.baseURL = base
	c.mu.Unlock()

	a := c.app
	a.mu.Lock()
	a.state.DSH.BaseURL = base
	selected := a.state.DSH.SessionID
	workspace := a.state.DSH.Workspace
	a.mu.Unlock()

	ids := make(map[string]bool, len(items))
	for _, it := range items {
		ids[it.SessionID] = true
	}
	if !ids[selected] {
		selected = ""
		if env := os.Getenv("DSH_SESSION_ID"); env != "" && ids[env] {
			selected = env
		}
		if selected == "" {
			// A dedicated session keeps the desktop app's history separate from
			// whatever conversation the user had open in the DSH GUI.
			cwd := workspace
			if cwd == "" {
				cwd = pickWorkspace(items)
			}
			if sid, err := c.createDedicated(ctx, base, secret, cwd); err == nil {
				selected = sid
				if fresh, ferr := c.listWith(base, secret, 15*time.Second, ctx); ferr == nil {
					items = fresh
				}
			} else {
				log.Printf("create dedicated session: %v", err)
			}
		}
		if selected == "" && workspace != "" {
			for _, it := range items {
				if it.CWD == workspace && !it.Blank {
					selected = it.SessionID
					break
				}
			}
		}
		if selected == "" {
			for _, it := range items {
				if !it.Blank {
					selected = it.SessionID
					break
				}
			}
		}
		if selected == "" && len(items) > 0 {
			selected = items[0].SessionID
		}
	}

	a.mu.Lock()
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
	st.BaseURL = base
	st.SessionID = selected
	if row := findSession(items, selected); row != nil {
		st.SessionTitle = sessionTitle(row)
		st.CWD = row.CWD
	}
	st.CheckedAt = time.Now().UnixMilli()
	a.setStatus(st)
	a.syncAgentsBlock()
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
	c.mu.Lock()
	base := c.baseURL
	secret := c.secret
	c.mu.Unlock()
	if base == "" || len(secret) == 0 {
		return fmt.Errorf("DSH 尚未连接")
	}
	c.reqMu.Lock()
	items, err := c.listWith(base, secret, 20*time.Second, ctx)
	c.reqMu.Unlock()
	if err != nil {
		return err
	}
	a := c.app
	opts := buildSessionOptions(items)
	a.setSessions(opts)
	a.emit("sessions", opts)

	a.mu.Lock()
	sessionID := a.state.DSH.SessionID
	a.mu.Unlock()

	row := findSession(items, sessionID)
	if row == nil && sessionID == "" {
		selected := ""
		if len(items) > 0 {
			for _, it := range items {
				if !it.Blank {
					selected = it.SessionID
					break
				}
			}
			if selected == "" {
				selected = items[0].SessionID
			}
		}
		a.mu.Lock()
		a.state.DSH.SessionID = selected
		a.mu.Unlock()
		a.markDirty()
		row = findSession(items, selected)
	}

	prev := a.getStatus()
	st := prev
	st.Connected = true
	st.Connecting = false
	st.Error = ""
	st.BaseURL = base
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

func (c *DSHClient) listAt(ctx context.Context, base string, secret []byte, timeout time.Duration) ([]sessionRow, error) {
	c.reqMu.Lock()
	defer c.reqMu.Unlock()
	return c.listWith(base, secret, timeout, ctx)
}

func (c *DSHClient) listWith(base string, secret []byte, timeout time.Duration, ctx context.Context) ([]sessionRow, error) {
	return c.listWithTimeout(base, secret, timeout, ctx)
}

func (c *DSHClient) listWithTimeout(base string, secret []byte, timeout time.Duration, ctx context.Context) ([]sessionRow, error) {
	cookie, err := mintCookie(base, secret)
	if err != nil {
		return nil, err
	}
	raw, status, err := c.postWith(ctx, base, cookie, "session/list", map[string]any{"_request": map[string]any{}}, timeout)
	if status == http.StatusUnauthorized {
		return nil, errUnauthorized
	}
	if err != nil {
		return nil, err
	}
	var out struct {
		Items []sessionRow `json:"items"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("解析会话列表失败: %w", err)
	}
	return out.Items, nil
}

func (c *DSHClient) call(ctx context.Context, method string, args any) (json.RawMessage, error) {
	c.reqMu.Lock()
	defer c.reqMu.Unlock()
	c.mu.Lock()
	base := c.baseURL
	secret := c.secret
	c.mu.Unlock()
	if base == "" || len(secret) == 0 {
		return nil, fmt.Errorf("DSH 尚未连接")
	}
	cookie, err := mintCookie(base, secret)
	if err != nil {
		return nil, err
	}
	raw, status, err := c.postWith(ctx, base, cookie, method, args, 25*time.Second)
	if status == http.StatusUnauthorized {
		// The signing secret may have been rotated; reload once.
		if fresh, rerr := readDSHSecret(); rerr == nil && !bytes.Equal(fresh, secret) {
			c.mu.Lock()
			c.secret = fresh
			c.mu.Unlock()
			if cookie, err = mintCookie(base, fresh); err == nil {
				raw, status, err = c.postWith(ctx, base, cookie, method, args, 25*time.Second)
			}
		}
		if status == http.StatusUnauthorized {
			return nil, errUnauthorized
		}
	}
	return raw, err
}

func (c *DSHClient) postWith(ctx context.Context, base, cookie, method string, args any, timeout time.Duration) (json.RawMessage, int, error) {
	payload := map[string]any{
		"type":   "client-request",
		"rpcId":  uuid.NewString(),
		"method": method,
		"payload": map[string]any{
			"args": args,
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, err
	}
	u := strings.TrimRight(base, "/") + "/api/" + method
	reqCtx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		reqCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cookie", cookie)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("连接 DSH 失败: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, resp.StatusCode, errUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, fmt.Errorf("DSH 返回 HTTP %d: %s", resp.StatusCode, truncateRunes(string(data), 180))
	}
	var env rpcEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("解析 DSH 响应失败: %w", err)
	}
	if !env.Result.OK {
		if env.Result.Error != nil {
			return nil, resp.StatusCode, fmt.Errorf("%s: %s", env.Result.Error.Code, env.Result.Error.Message)
		}
		return nil, resp.StatusCode, fmt.Errorf("DSH 调用失败")
	}
	return env.Result.Value, resp.StatusCode, nil
}

func (c *DSHClient) scan(ctx context.Context, secret []byte) string {
	type hit struct{ base string }
	results := make(chan string, 8)
	stop := make(chan struct{})
	var once sync.Once
	sem := make(chan struct{}, 24)
	var wg sync.WaitGroup
	ports := make([]int, 0, 180)
	seen := make(map[int]bool)
	addPort := func(p int) {
		if p <= 0 || p > 65535 || seen[p] {
			return
		}
		seen[p] = true
		ports = append(ports, p)
	}
	for port := 13800; port <= 13920; port++ {
		addPort(port)
	}
	// DSH's desktop host picks an ephemeral loopback port on startup. The old
	// fixed range missed those instances; enumerate the listeners Windows
	// already knows about and probe them as well.
	for _, port := range listeningPorts() {
		addPort(port)
	}
	for _, port := range ports {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			select {
			case <-stop:
				return
			default:
			}
			addr := fmt.Sprintf("127.0.0.1:%d", p)
			conn, err := net.DialTimeout("tcp", addr, 120*time.Millisecond)
			if err != nil {
				return
			}
			_ = conn.Close()
			base := "http://" + addr
			probeCtx, cancel := context.WithTimeout(ctx, 1200*time.Millisecond)
			defer cancel()
			if _, err := c.listWithTimeout(base, secret, 1*time.Second, probeCtx); err == nil {
				once.Do(func() {
					select {
					case results <- base:
					default:
					}
					close(stop)
				})
			}
		}(port)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case base := <-results:
		return base
	case <-done:
		return ""
	case <-time.After(7 * time.Second):
		close(stop)
		return ""
	case <-ctx.Done():
		return ""
	}
}

// listeningPorts returns loopback TCP ports in LISTEN state. netstat is used
// instead of a fixed port range because DSH Desktop allocates a fresh ephemeral
// port every time it starts.
func listeningPorts() []int {
	out, err := exec.Command("netstat", "-ano", "-p", "tcp").Output()
	if err != nil {
		return nil
	}
	seen := make(map[int]bool)
	ports := make([]int, 0, 64)
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || !strings.EqualFold(fields[0], "TCP") {
			continue
		}
		local, remote := fields[1], fields[2]
		if !strings.HasSuffix(remote, ":0") {
			continue
		}
		host, portText, err := net.SplitHostPort(local)
		if err != nil {
			continue
		}
		if host != "127.0.0.1" && host != "0.0.0.0" && host != "::" && host != "::1" {
			continue
		}
		port, err := strconv.Atoi(portText)
		if err != nil || port < 1024 || seen[port] {
			continue
		}
		seen[port] = true
		ports = append(ports, port)
	}
	return ports
}

func (c *DSHClient) SendPrompt(text string, onboarding bool) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("消息不能为空")
	}
	st := c.app.getStatus()
	if !st.Connected || st.SessionID == "" {
		return "", fmt.Errorf("DSH 尚未连接，请先在设置中连接会话")
	}
	now := time.Now().UnixMilli()
	kind := "input"
	if onboarding {
		kind = "onboarding"
	}
	msg := ChatMessage{
		ID:        uuid.NewString(),
		Text:      text,
		Status:    "sending",
		Kind:      kind,
		SessionID: st.SessionID,
		At:        now,
	}
	c.app.appendMessage(msg)
	c.app.emitState()

	st.Sending = true
	c.app.setStatus(st)
	c.app.emitStatus()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		contentText := text
		if !onboarding {
			// Markdown hides HTML comments, while the model still receives the
			// durable message id. The bridge can then record exact provenance.
			contentText = todoPromptText(text, msg.ID)
		}
		args := map[string]any{
			"request": map[string]any{
				"requestId": uuid.NewString(),
				"sessionId": msg.SessionID,
				"mode":      "queue",
				"content": []map[string]any{
					{"type": "text", "text": contentText},
				},
				"clientTimeZone": clientTimeZone(),
			},
		}
		raw, err := c.call(ctx, "session/prompt", args)
		cur := c.app.getStatus()
		cur.Sending = false
		if err != nil {
			msg.Status = "failed"
			msg.Error = err.Error()
			c.app.updateMessage(msg)
			c.app.setStatus(cur)
			c.app.emitStatus()
			c.app.toast("error", "发送失败："+err.Error())
			return
		}
		var out struct {
			Accepted bool `json:"accepted"`
		}
		_ = json.Unmarshal(raw, &out)
		if !out.Accepted {
			msg.Status = "failed"
			msg.Error = "DSH 未接受消息"
			c.app.updateMessage(msg)
			c.app.setStatus(cur)
			c.app.emitStatus()
			c.app.toast("error", "DSH 未接受消息")
			return
		}
		msg.Status = "sent"
		c.app.updateMessage(msg)
		cur.LastPrompt = truncateRunes(text, 220)
		cur.Connected = true
		c.app.setStatus(cur)
		if onboarding {
			c.app.recordEvent(HistoryEvent{
				ID: uuid.NewString(), Kind: "onboarding", Actor: "user",
				Title: "启用清单同步", Detail: "已发送 TodoList 长期清单接入说明", At: time.Now().UnixMilli(),
			})
		} else {
			c.app.recordEvent(HistoryEvent{
				ID: uuid.NewString(), Kind: "input", Actor: "user",
				Title: "发送到 DSH", Detail: truncateRunes(text, 300), At: time.Now().UnixMilli(),
				SourceMessageID: msg.ID, OriginalText: truncateRunes(text, 500),
			})
		}
		c.app.emitState()
		if onboarding {
			c.app.markOnboarded(msg.SessionID)
			if c.app.shouldWriteAgents() {
				if path, werr := c.app.writeAgentsBlock(); werr == nil {
					c.app.toast("success", "已启用 DSH 任务同步，并写入 "+filepath.Base(path))
				}
			}
			c.app.emitState()
		}
		time.Sleep(350 * time.Millisecond)
		c.Wake()
	}()
	return msg.ID, nil
}

func (c *DSHClient) OnboardingText() string {
	url := "http://127.0.0.1:17890"
	token := ""
	if c.app.bridge != nil {
		url = c.app.bridge.URL()
		token = c.app.bridge.Token()
	}
	return strings.Join([]string{
		"【TodoList 长期清单 · 接入说明】",
		"我是通过桌面 TodoList 应用连接你的。我的输入会持续、零散、杂乱，可能包含感慨、吐槽、微信式自言自语、转述和链接；并不都是任务。请把本会话当作我的长期任务收件箱和想法回顾库。",
		"",
		"本地接口：",
		"- 地址：" + url,
		"- 鉴权：请求头 `Authorization: Bearer " + token + "`",
		"- `GET /api/todos`：查看全部任务（含 dropped），用于查重和定位",
		"- `GET /api/journal?limit=20&q=关键词`：回看很久以前的想法原文及关联任务",
		"- `POST /api/todos`：",
		"  - `{\"action\":\"add\",\"title\":\"...\",\"notes\":\"...\",\"priority\":\"pinned|high|normal|low\",\"parentId\":\"...\",\"sourceMessageId\":\"...\"}`",
		"  - `{\"action\":\"update\",\"id\":\"...\",\"title\":\"...\",\"notes\":\"...\",\"status\":\"todo|doing|done|dropped\",\"dropReason\":\"...\"}`",
		"  - `{\"action\":\"complete\",\"id\":\"...\"}` / `reopen` / `drop`（带 reason） / `delete`",
		"",
		"每条来自 TodoList 的消息末尾会有一个隐藏标记 `<!-- todo-msg:<id> -->`。请把它作为 `sourceMessageId`；没有标记就省略。这样我能回看每个任务是从哪条想法来的。",
		"",
		"请遵循：",
		"1. 先 GET 列表，避免重复；相似的旧任务优先 update，不要重复 add。",
		"2. 只把明确的行动意图（想做、要做、记一下、提醒我、推进某事）提炼成任务；纯情绪、感慨、分享不要自动建任务。可以简短回应，或问一句“要不要记成任务”。",
		"3. 一条消息有多个行动点就拆成多个任务；一个行动点不要拆碎。",
		"- " + todoPlacementRule,
		"4. 任务支持一层子任务：`add` 时传 `parentId`；子任务不能再有子任务。只有当一个任务确实有 2-5 个明确步骤时才拆子任务，不要为了层级而层级。",
		"5. 长期跟踪、需要一直排在最前面但不紧急的任务用 `priority:\"pinned\"`（置顶 · 长期）；它排在 high 之前，但视觉上不抢高优先级。",
		"6. `GET /api/todos` 返回的每个任务有 `number` 字段（如 `2.4`），它就是打钩圆圈右侧的序号；用户用序号指代时按 `number` 精确找到 id，不要按数组下标猜。序号会随排序、优先级和状态变化，操作前重新 GET。",
		"7. 用户说“关掉/做完了/搞定” → `complete`；说“不做了/算了/放弃/搁置/丢弃” → `drop` 并写明 reason；只有明确说“删除/删掉”才 `delete`。不要把“不做了”当成完成。",
		"8. 开始处理某任务 → `doing`；完成后 → `done`。",
		"9. 如果想法里提到明确的时间/期限（今晚、明天、周五、周末、下周、某月某日等），把这个时间写进任务标题，不要只写在 notes；用尽量短的写法，例如「周五前交开题报告」「周末去看展」。过于模糊的「以后/有空」不要硬塞时间。",
		"10. 修改标题时，新标题必须仍然能让人只看标题就知道要做什么。要保留旧标题里的核心动作、对象和关键限定，再融入修改意见，不能只写修改意见而丢掉原任务信息。例如旧标题「整理分享用的演示截图」，用户说「改成从录屏里挑」→「从录屏里挑分享用演示截图」，不能只写「从录屏里挑」。改完先自检：只看新标题能否知道要做什么？不能就补回旧核心信息。",
		"11. 任务标题写成可执行的短句；`notes` 使用固定结构，不要把所有内容混在一起。",
		"12. `notes` 必须分成两段：第一段是「这个事项的详细描述」，信息量要充足（目标、背景、做法、范围、判断标准等），不要只写一句；中间单独一行 `-------`；最后是「原话区」，只放我的原话，最新的在最上面、旧的依次往下。格式：`{{详细描述}}\\n\\n-------\\n\\n- {{最新原话}}\\n- {{较早原话}}`。",
		"13. 每次 update 都要重写详细描述，使其与当前任务一致；原话只追加、不改写、不删除，新原话放在原话区最上面，旧原话依次往后。所有原话必须集中在 `-------` 下方，不能混入详细描述；备注总长控制在 2000 字内，原话很多时保留最相关的。",
		"14. 回复只简要汇报实际新增/完成/丢弃了哪些任务，不要只列计划。",
		"",
		"如果我的消息没有明确行动点，就不要制造任务；拿不准时最多问一个简短问题。",
	}, "\n")
}

func (a *App) shouldWriteAgents() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state.Prefs.WriteAgents
}

func clientTimeZone() string {
	if tz := strings.TrimSpace(os.Getenv("DSH_CLIENT_TIMEZONE")); tz != "" {
		return tz
	}
	return "Asia/Shanghai"
}

// ---- credentials and cookies ---------------------------------------------

func dshHome() string {
	if v := strings.TrimSpace(os.Getenv("DSH_HOME")); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".dsh")
}

func readDSHSecret() ([]byte, error) {
	home := dshHome()
	if home == "" {
		return nil, fmt.Errorf("找不到用户目录")
	}
	data, err := os.ReadFile(filepath.Join(home, ".credentials.yaml"))
	if err != nil {
		return nil, fmt.Errorf("读取 DSH 凭据失败: %w", err)
	}
	lines := strings.Split(string(data), "\n")
	inRecord := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "client-connection/browser-session:") {
			inRecord = true
			continue
		}
		if !inRecord {
			continue
		}
		if strings.HasPrefix(trimmed, "secret:") {
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "secret:"))
			val = strings.Trim(val, "\"'")
			raw, err := decodeBase64URL(val)
			if err != nil || len(raw) != 32 {
				return nil, fmt.Errorf("DSH 浏览器签名密钥格式异常")
			}
			return raw, nil
		}
		if line != "" && line[0] != ' ' && line[0] != '\t' {
			inRecord = false
		}
	}
	return nil, fmt.Errorf("DSH 凭据中没有浏览器会话签名密钥")
}

func decodeBase64URL(s string) ([]byte, error) {
	s = strings.TrimRight(s, "=")
	return base64.RawURLEncoding.DecodeString(s)
}

func encodeBase64URL(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

func normalizeBase(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return ""
	}
	return scheme + "://" + u.Host
}

func mintCookie(base string, secret []byte) (string, error) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("DSH 地址无效")
	}
	authority := u.Host
	name := "dsh-auth-" + encodeBase64URL(sha256Sum([]byte(authority)))
	now := time.Now().UnixMilli()
	payload := struct {
		Version   int    `json:"version"`
		Authority string `json:"authority"`
		IssuedAt  int64  `json:"issuedAt"`
		ExpiresAt int64  `json:"expiresAt"`
	}{
		Version:   1,
		Authority: authority,
		IssuedAt:  now,
		ExpiresAt: now + 29*24*60*60*1000,
	}
	bodyJSON, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	body := encodeBase64URL(bodyJSON)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(body))
	sig := encodeBase64URL(mac.Sum(nil))
	return name + "=v1." + body + "." + sig, nil
}

func sha256Sum(b []byte) []byte {
	h := sha256.Sum256(b)
	return h[:]
}
