package main

import (
	"encoding/json"
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
	c := newDSHClient(restarted)
	restarted.dsh = c
	c.conn = testConnector(t, func(request testConnectorRequest) string {
		if request.Args.Request.SessionID != "original" || request.Args.Request.RequestID != "prompt" {
			t.Errorf("wrong receipt query: %+v", request)
		}
		return `{"stage":"failed","reason":"interrupted","turn":1}`
	})
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
	c := newDSHClient(restarted)
	restarted.dsh = c
	c.conn = testConnector(t, func(request testConnectorRequest) string {
		if request.Method != "todolist/status" && request.Method != "todolist/wait" {
			t.Errorf("recovery unexpectedly called %s", request.Method)
		}
		return `{"stage":"session-not-found"}`
	})
	other := sessionRow{SessionID: "other"}
	c.pumpQueue(t.Context(), &other, []sessionRow{other})
	c.pumpQueue(t.Context(), &other, []sessionRow{other})
	if restarted.state.ActivePrompt != nil || !restarted.state.UncertainPrompts[prompt.ID].Accepted ||
		restarted.state.Messages[0].Status != "uncertain" || len(restarted.state.Queue) != 0 {
		t.Fatal("missing original session must retain the acknowledged prompt for review only")
	}
}
