package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestInterruptedDSHTurnSettlesAfterRestart(t *testing.T) {
	a := isolatedApp(t)
	prompt := &activePrompt{queuedPrompt: queuedPrompt{ID: "prompt", Text: "request", At: 1}, Session: "original", Started: time.Now().UnixMilli()}
	a.state.ActivePrompt, a.state.QueueBatch = prompt, true
	a.state.Messages = []ChatMessage{{ID: prompt.ID, Kind: "input", Status: "sending"}}
	a.markDirty()
	a.saveNow()

	restarted := NewApp()
	restarted.dataPath = a.dataPath
	if err := restarted.load(); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/session/events" {
			t.Errorf("recovery unexpectedly called %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"result":{"ok":true,"value":{"records":[
			{"event":{"type":"turn/start","seq":1,"data":{"turn":1}}},
			{"event":{"type":"user/message","seq":2,"data":{"content":[{"type":"text","text":"request\n<!-- todo-msg:prompt -->"}]}}},
			{"event":{"type":"turn/end","seq":3,"data":{"turn":1,"reason":{"kind":"interrupted"}}}}
		]}}}`))
	}))
	defer server.Close()
	c := newDSHClient(restarted)
	restarted.dsh = c
	c.conn = dshEndpoint{base: server.URL, secret: []byte("fixture key")}
	row := sessionRow{SessionID: "original", Running: true}
	row.Projections.AsOfSeq = 3
	c.pumpQueue(t.Context(), &row, []sessionRow{row})
	if restarted.state.ActivePrompt != nil || restarted.state.Messages[0].Status != "failed" || restarted.batchPausedLocked() {
		t.Fatal("a definitive DSH interruption must settle the active prompt and unblock sync")
	}
}

func TestPromptCompletionSurvivesRestartAndStalePeer(t *testing.T) {
	a := isolatedApp(t)
	prompt := &activePrompt{queuedPrompt: queuedPrompt{ID: "prompt", Text: "request", At: 1}, Session: "original"}
	message := ChatMessage{ID: prompt.ID, Text: prompt.Text, At: prompt.At, SessionID: prompt.Session, Kind: "input", Status: "sending"}
	a.state.Messages = []ChatMessage{message}
	a.state.ActivePrompt = prompt
	stale := a.replicaSnapshot()
	stale.Device = "stale-peer"
	message.Status, message.Error = "uncertain", "old error"
	record, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	stale.capture(map[string]json.RawMessage{"message:prompt": record}, time.Now().Add(time.Hour).UnixMilli())
	newDSHClient(a).finishQueued(prompt, promptOutcome{status: "sent"})

	restarted := NewApp()
	restarted.dataPath = a.dataPath
	if err := restarted.load(); err != nil {
		t.Fatal(err)
	}
	if err := restarted.mergeReplica(stale); err != nil {
		t.Fatal(err)
	}
	if msg := restarted.state.Messages[0]; msg.Status != "sent" || msg.Error != "" {
		t.Fatalf("stale peer downgraded completion to %s (%s)", msg.Status, msg.Error)
	}
	if err := restarted.load(); err != nil {
		t.Fatal(err)
	}
	if msg := restarted.state.Messages[0]; msg.Status != "sent" || msg.Error != "" {
		t.Fatalf("downgraded status persisted: %s (%s)", msg.Status, msg.Error)
	}
}

func TestLoadRestoresCompletionFromExistingHistory(t *testing.T) {
	a := isolatedApp(t)
	path := filepath.Join(t.TempDir(), "restart.json")
	a.dataPath = path
	next := a.state
	next.Messages = []ChatMessage{{ID: "prompt", Kind: "input", Status: "uncertain", Error: "old error"}}
	next.Events = []HistoryEvent{{ID: "prompt-reply-prompt", Kind: "dsh.reply", SourceMessageID: "prompt"}}
	raw, err := json.Marshal(next)
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(path, raw); err != nil {
		t.Fatal(err)
	}
	if err := a.load(); err != nil {
		t.Fatal(err)
	}
	if msg := a.state.Messages[0]; msg.Status != "sent" || msg.Error != "" {
		t.Fatalf("did not restore known completion: %s (%s)", msg.Status, msg.Error)
	}
}

func TestPromptAcceptanceIsDurableAndAmbiguousDeliveryIsNotReplayed(t *testing.T) {
	a := isolatedApp(t)
	prompt := &activePrompt{queuedPrompt: queuedPrompt{ID: "prompt", Text: "request", At: 1},
		Session: "original", Started: time.Now().Add(-2 * time.Minute).UnixMilli()}
	a.state.ActivePrompt = prompt
	a.state.Messages = []ChatMessage{{ID: prompt.ID, Kind: "input", Status: "sending"}}
	a.markPromptAccepted(prompt)
	restarted := NewApp()
	restarted.dataPath = a.dataPath
	if err := restarted.load(); err != nil {
		t.Fatal(err)
	}
	if restarted.state.ActivePrompt == nil || !restarted.state.ActivePrompt.Accepted ||
		restarted.state.Messages[0].Status != "accepted" {
		t.Fatal("DSH acknowledgement was lost at restart")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("recovery unexpectedly called %s", r.URL.Path)
	}))
	defer server.Close()
	c := newDSHClient(restarted)
	restarted.dsh = c
	c.conn = dshEndpoint{base: server.URL, secret: []byte("fixture key")}
	other := sessionRow{SessionID: "other"}
	c.pumpQueue(t.Context(), &other, []sessionRow{other})
	c.pumpQueue(t.Context(), &other, []sessionRow{other})
	if restarted.state.ActivePrompt != nil || !restarted.state.UncertainPrompts[prompt.ID].Accepted ||
		restarted.state.Messages[0].Status != "uncertain" || len(restarted.state.Queue) != 0 {
		t.Fatal("missing original session must retain the acknowledged prompt for review only")
	}
}
