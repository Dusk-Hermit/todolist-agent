package main

const (
	appName    = "TodoList Agent"
	appVersion = "0.2.7"
	appID      = "Dusk.TodoListAgent"

	statusTodo    = "todo"
	statusDoing   = "doing"
	statusDone    = "done"
	statusDropped = "dropped"

	priorityLow    = "low"
	priorityNormal = "normal"
	priorityHigh   = "high"
	priorityPinned = "pinned" // 置顶 · 长期
)

// Todo is one user-visible task. JSON keys are consumed directly by the web UI.
type Todo struct {
	ID              string `json:"id"`
	Title           string `json:"title"`
	Notes           string `json:"notes"`
	Status          string `json:"status"`
	Priority        string `json:"priority"`
	Source          string `json:"source"`
	SessionID       string `json:"sessionId,omitempty"`
	CreatedAt       int64  `json:"createdAt"`
	UpdatedAt       int64  `json:"updatedAt"`
	CompletedAt     int64  `json:"completedAt,omitempty"`
	DroppedAt       int64  `json:"droppedAt,omitempty"`
	DropReason      string `json:"dropReason,omitempty"`
	ParentID        string `json:"parentId,omitempty"`
	Number          string `json:"number,omitempty"` // display-only, recomputed in snapshots
	Position        int    `json:"position"`
	SourceMessageID string `json:"sourceMessageId,omitempty"`
	SourceText      string `json:"sourceText,omitempty"`
	SourceAt        int64  `json:"sourceAt,omitempty"`
}

// HistoryEvent is an append-only audit trail entry. Before/After snapshots make
// every change reconstructable without replaying the whole list.
type HistoryEvent struct {
	ID              string `json:"id"`
	Kind            string `json:"kind"`
	Actor           string `json:"actor"`
	TodoID          string `json:"todoId,omitempty"`
	Title           string `json:"title,omitempty"`
	Detail          string `json:"detail,omitempty"`
	At              int64  `json:"at"`
	SourceMessageID string `json:"sourceMessageId,omitempty"`
	OriginalText    string `json:"originalText,omitempty"`
	Before          *Todo  `json:"before,omitempty"`
	After           *Todo  `json:"after,omitempty"`
}

// ChatMessage records one raw input sent through the desktop composer. These
// messages are the long-term journal: they are not rewritten or summarized.
type ChatMessage struct {
	ID        string `json:"id"`
	Text      string `json:"text"`
	Status    string `json:"status"`         // sending | sent | queued | failed
	Kind      string `json:"kind,omitempty"` // input | onboarding
	Error     string `json:"error,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
	At        int64  `json:"at"`
}

type WindowRect struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

type Prefs struct {
	AlwaysOnTop       bool            `json:"alwaysOnTop"`
	Filter            string          `json:"filter"`
	Tab               string          `json:"tab"`
	ViewMode          string          `json:"viewMode"` // cards | compact
	Window            WindowRect      `json:"window"`
	WriteAgents       bool            `json:"writeAgents"`
	OnboardedSessions map[string]bool `json:"onboardedSessions"`
}

type DSHConfig struct {
	BaseURL     string `json:"baseUrl"`
	SessionID   string `json:"sessionId"`
	Workspace   string `json:"workspace"`
	AutoConnect bool   `json:"autoConnect"`
}

type BridgeConfig struct {
	Port  int    `json:"port"`
	Token string `json:"token"`
}

type persistedState struct {
	Version  int            `json:"version"`
	Todos    []Todo         `json:"todos"`
	Events   []HistoryEvent `json:"events"`
	Messages []ChatMessage  `json:"messages"`
	Prefs    Prefs          `json:"prefs"`
	DSH      DSHConfig      `json:"dsh"`
	Bridge   BridgeConfig   `json:"bridge"`
}

type DSHStatus struct {
	Connected    bool   `json:"connected"`
	Connecting   bool   `json:"connecting"`
	Error        string `json:"error,omitempty"`
	BaseURL      string `json:"baseUrl,omitempty"`
	SessionID    string `json:"sessionId,omitempty"`
	SessionTitle string `json:"sessionTitle,omitempty"`
	CWD          string `json:"cwd,omitempty"`
	Running      bool   `json:"running"`
	RunningSince int64  `json:"runningSince,omitempty"`
	UpdatedAt    int64  `json:"updatedAt,omitempty"`
	LastPrompt   string `json:"lastPrompt,omitempty"`
	LastResponse string `json:"lastResponse,omitempty"`
	Turns        int    `json:"turns"`
	Steps        int    `json:"steps"`
	Tokens       int64  `json:"tokens"`
	CheckedAt    int64  `json:"checkedAt,omitempty"`
	Sending      bool   `json:"sending"`
	Armed        bool   `json:"-"`
}

type SessionOption struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	CWD       string `json:"cwd"`
	UpdatedAt int64  `json:"updatedAt"`
	Running   bool   `json:"running"`
	Blank     bool   `json:"blank"`
}

type BridgePublic struct {
	URL        string `json:"url"`
	Token      string `json:"token"`
	Port       int    `json:"port"`
	ConfigPath string `json:"configPath"`
}

type JournalTask struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Priority string `json:"priority"`
}

type JournalEntry struct {
	ID        string        `json:"id"`
	Text      string        `json:"text"`
	Status    string        `json:"status"`
	SessionID string        `json:"sessionId,omitempty"`
	At        int64         `json:"at"`
	Tasks     []JournalTask `json:"tasks"`
}

type JournalPage struct {
	Items   []JournalEntry `json:"items"`
	HasMore bool           `json:"hasMore"`
	Total   int            `json:"total"`
}

type StatePayload struct {
	Version      string          `json:"version"`
	Todos        []Todo          `json:"todos"`
	Events       []HistoryEvent  `json:"events"`
	Messages     []ChatMessage   `json:"messages"`
	MessageTotal int             `json:"messageTotal"`
	Prefs        Prefs           `json:"prefs"`
	DSH          DSHStatus       `json:"dsh"`
	Config       DSHConfig       `json:"config"`
	Bridge       BridgePublic    `json:"bridge"`
	Sessions     []SessionOption `json:"sessions"`
	DataPath     string          `json:"dataPath"`
	Platform     string          `json:"platform"`
}

type TodoInput struct {
	Title           string `json:"title"`
	Notes           string `json:"notes"`
	Priority        string `json:"priority"`
	Status          string `json:"status"`
	ParentID        string `json:"parentId"`
	SourceMessageID string `json:"sourceMessageId"`
	SourceText      string `json:"sourceText"`
	DropReason      string `json:"dropReason"`
}

type TodoPatch struct {
	Title      *string `json:"title"`
	Notes      *string `json:"notes"`
	Priority   *string `json:"priority"`
	Status     *string `json:"status"`
	DropReason *string `json:"dropReason"`
}
