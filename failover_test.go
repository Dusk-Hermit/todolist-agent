package main

import (
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestChooseDSHFallsBackFromPreferredWithoutSession(t *testing.T) {
	peers := []lanPeer{
		{ID: "A", Platform: "windows", Dispatch: true, HasDSH: true,
			DSH: DSHStatus{Connected: true}},
		{ID: "B", Platform: "windows", Dispatch: true, HasDSH: true,
			DSH: DSHStatus{Connected: true, SessionID: "b-session"}},
	}
	if got := chooseDSH(peers, "A"); got == nil || got.ID != "B" {
		t.Fatalf("did not fail over: %+v", got)
	}
	peers[0].DSH.SessionID = "a-session"
	if got := chooseDSH(peers, "A"); got == nil || got.ID != "A" {
		t.Fatalf("did not restore preferred host: %+v", got)
	}
}
func TestFailoverDoesNotReplayOwnedPromptAndSyncsBackupHistory(t *testing.T) {
	phone, primary, backup := isolatedApp(t), isolatedApp(t), isolatedApp(t)
	primary.dsh, backup.dsh = newDSHClient(primary), newDSHClient(backup)
	code := randomToken()
	start := func(a *App) *lanService {
		t.Helper()
		s, err := newLAN(a, lanConfig{Code: code}, a.replicaSnapshot().Device, 0)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(s.close)
		return s
	}
	client, hostA, hostB := start(phone), start(primary), start(backup)
	peer := func(s *lanService) lanPeer {
		return lanPeer{ID: s.id, Address: fmt.Sprintf("127.0.0.1:%d", s.port),
			Platform: "windows", Dispatch: true, HasDSH: true,
			DSH: DSHStatus{Connected: true, SessionID: "valid"}}
	}
	a, b := peer(hostA), peer(hostB)
	phone.state.LAN.DSHDevice = a.ID
	first := queuedPrompt{ID: uuid.NewString(), Text: "A may already have this", At: 1}
	second := queuedPrompt{ID: uuid.NewString(), Text: "B should execute this", At: 2}
	if err := phone.enqueuePrompts([]queuedPrompt{first}); err != nil {
		t.Fatal(err)
	}
	reserved, err := phone.assignPrompts(a.ID, true)
	if err != nil || len(reserved) != 1 {
		t.Fatalf("reserve A: %v %v", reserved, err)
	}
	var ack map[string]bool
	// A durably accepts this, but the client loses the acknowledgement.
	if err := client.request("POST", a.Address, "/dispatch", promptDispatch{Host: a.ID, Prompts: reserved}, &ack); err != nil {
		t.Fatal(err)
	}
	if err := phone.enqueuePrompts([]queuedPrompt{second}); err != nil {
		t.Fatal(err)
	}
	if _, err := phone.bindingAddTodo(TodoInput{Title: "phone task"}); err != nil {
		t.Fatal(err)
	}
	if err := client.routeQueue([]lanPeer{b}, nil); err != nil {
		t.Fatal(err)
	}
	if len(backup.state.Queue) != 0 {
		t.Fatal("B accepted work before syncing")
	}
	if err := client.exchange(b); err != nil {
		t.Fatal(err)
	}
	if err := client.routeQueue([]lanPeer{b}, map[string]bool{b.ID: true}); err != nil {
		t.Fatal(err)
	}
	if len(backup.state.Queue) != 1 || backup.state.Queue[second.ID].Host != b.ID {
		t.Fatalf("backup queue: %+v", backup.state.Queue)
	}
	if phone.state.Queue[first.ID].Host != a.ID {
		t.Fatal("ambiguous A job moved or disappeared")
	}
	if len(backup.snapshot().Todos) != 1 {
		t.Fatal("missing synchronized phone task")
	}
	if err := client.request("POST", b.Address, "/dispatch", promptDispatch{Host: a.ID, Prompts: reserved}, &ack); err == nil {
		t.Fatal("B accepted a stale address dispatch for A")
	}
	if err := client.request("POST", b.Address, "/enqueue", reserved, &ack); err == nil {
		t.Fatal("legacy enqueue accepted work assigned to another host")
	}
	backup.updateMessage(ChatMessage{ID: second.ID, Text: second.Text, At: second.At, Kind: "input", Status: "sent"})
	if _, err := backup.bindingAddTodo(TodoInput{Title: "B result"}); err != nil {
		t.Fatal(err)
	}
	if err := client.exchange(b); err != nil {
		t.Fatal(err)
	}
	if err := client.exchange(a); err != nil {
		t.Fatal(err)
	}
	if len(phone.state.Queue) != 0 {
		t.Fatal("A's replicated acknowledgement did not clear the phone outbox")
	}
	if len(primary.state.Queue) != 1 || primary.state.Queue[first.ID].Host != a.ID {
		t.Fatal("B's journal must not become executable on A")
	}
	if len(primary.snapshot().Todos) != 2 || len(primary.snapshot().Messages) != 2 {
		t.Fatal("A did not receive B's tasks and message history")
	}
}

func TestInterruptedBatchResumesSyncAndReconcilesOnlyExactPrompt(t *testing.T) {
	a := isolatedApp(t)
	a.dsh = newDSHClient(a)
	id := uuid.NewString()
	a.state.Messages = []ChatMessage{{ID: id, Text: "request", Status: "sending"}}
	a.state.ActivePrompt = &activePrompt{queuedPrompt: queuedPrompt{ID: id, Text: "request"},
		Session: "dsh-a", Started: time.Now().Add(-2 * time.Minute).UnixMilli(), PreviousSeq: 1}
	a.state.QueueBatch = true
	a.dsh.reportError(errors.New("unavailable"))
	if a.state.ActivePrompt != nil || a.state.Messages[0].Status != "uncertain" || a.batchPausedLocked() {
		t.Fatal("disconnected host failed to release its expired batch")
	}
	var settled atomic.Bool
	a.dsh.conn = testConnector(t, func(request testConnectorRequest) string {
		if request.Args.Request.SessionID != "dsh-a" || request.Args.Request.RequestID != id {
			t.Errorf("wrong recovery target: %+v", request)
		}
		if !settled.Load() {
			return `{"stage":"not-found"}`
		}
		return `{"stage":"completed","turn":1,"response":"exact result"}`
	})
	a.dsh.reconcileUncertain(t.Context())
	if a.state.Messages[0].Status != "uncertain" {
		t.Fatal("matched an unrelated DSH response with identical user text")
	}
	settled.Store(true)
	a.dsh.reconcileUncertain(t.Context())
	if a.state.Messages[0].Status != "sent" || len(a.state.UncertainPrompts) != 0 ||
		a.state.Events[len(a.state.Events)-1].SourceMessageID != id {
		t.Fatal("late exact response did not reconcile")
	}
}
