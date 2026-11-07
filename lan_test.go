package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestLANRejectsLegacyReplicaExchangeInBothDirections(t *testing.T) {
	s := lanService{}
	for _, handler := range []http.HandlerFunc{s.handleSync, s.handleApply} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("POST", "/", strings.NewReader("{}")))
		if w.Code != http.StatusUpgradeRequired {
			t.Fatalf("old peer got %d, want upgrade required", w.Code)
		}
	}
	old := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/sync") {
			t.Errorf("sent a mutation to the old device: %s", r.URL.Path)
		}
		writeJSON(w, http.StatusOK, replicaDelta{})
	}))
	defer old.Close()
	s.app, s.ctx, s.client = isolatedApp(t), t.Context(), old.Client()
	err := s.exchange(lanPeer{Address: strings.TrimPrefix(old.URL, "https://")})
	if !errors.Is(err, errLANMergeUpgrade) {
		t.Fatalf("old server: got %v, want upgrade required", err)
	}
}

func isolatedApp(t *testing.T) *App {
	t.Helper()
	a := NewApp()
	a.dataPath = filepath.Join(t.TempDir(), "data.json")
	t.Cleanup(a.saveNow)
	return a
}

func TestLANExchangesChangesInBothDirectionsAndRejectsWrongCode(t *testing.T) {
	left, right := isolatedApp(t), isolatedApp(t)
	if _, err := left.bindingAddTodo(TodoInput{Title: "desktop"}); err != nil {
		t.Fatal(err)
	}
	if _, err := right.bindingAddTodo(TodoInput{Title: "phone"}); err != nil {
		t.Fatal(err)
	}
	code := randomToken()
	start := func(a *App, code string) *lanService {
		t.Helper()
		r := a.replicaSnapshot()
		service, err := newLAN(a, lanConfig{Code: code}, r.Device, 0)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(service.close)
		return service
	}
	receiver := start(right, code)
	peer := lanPeer{ID: receiver.id, Address: fmt.Sprintf("127.0.0.1:%d", receiver.port)}
	if err := start(left, randomToken()).exchange(peer); err == nil {
		t.Fatal("exchange with different pairing code succeeded")
	}
	sender := start(left, code)
	if err := sender.exchange(peer); err != nil {
		t.Fatal(err)
	}
	for _, a := range []*App{left, right} {
		if got := len(a.snapshot().Todos); got != 2 {
			t.Fatalf("got %d tasks, want 2", got)
		}
	}
	current := left.replicaSnapshot()
	if delta := current.delta(right.replicaSnapshot().manifest()); len(delta.Records) != 0 {
		t.Fatalf("unchanged tasks were sent: %v", delta.Records)
	}
}

func TestLANQueuePersistsOrderAndDeduplicatesRetries(t *testing.T) {
	desktop := isolatedApp(t)
	desktop.dsh = newDSHClient(desktop)
	now := time.Now().UnixMilli()
	late := queuedPrompt{ID: uuid.NewString(), Text: "desktop", At: now}
	early := queuedPrompt{ID: uuid.NewString(), Text: "phone", At: now - 20}
	for _, batch := range [][]queuedPrompt{{late}, {early}, {late, early}} {
		if err := desktop.enqueuePrompts(batch); err != nil {
			t.Fatal(err)
		}
	}
	restored := NewApp()
	restored.dataPath = desktop.dataPath
	if err := restored.load(); err != nil {
		t.Fatal(err)
	}
	pending := orderedQueue(restored.state.Queue)
	if len(pending) != 2 || pending[0] != early || pending[1] != late {
		t.Fatalf("unexpected pending queue: %v", pending)
	}
	desktop.mu.Lock()
	desktop.state.QueueBatch = true
	desktop.status.Connected = true
	desktop.status.SessionID = "test-session"
	desktop.mu.Unlock()
	if _, err := desktop.replicaForExchange(); !errors.Is(err, errBatchRunning) {
		t.Fatalf("batch should pause replica exchange, got %v", err)
	}
	if _, err := desktop.bindingSendInput("new while running"); err != nil {
		t.Fatal(err)
	}
}

func TestLivePromptWindowExtendsOnceWhenBacklogArrives(t *testing.T) {
	a := isolatedApp(t)
	a.dsh = newDSHClient(a)
	now := time.Now()
	live := queuedPrompt{ID: uuid.NewString(), Text: "live", At: now.UnixMilli(), Live: true}
	offline := queuedPrompt{ID: uuid.NewString(), Text: "offline", At: now.Add(-time.Hour).UnixMilli()}
	if err := a.enqueuePrompts([]queuedPrompt{live}); err != nil {
		t.Fatal(err)
	}
	if ready := time.UnixMilli(a.state.QueueReadyAt); ready.Sub(now) < livePromptCollectTime-time.Millisecond || ready.Sub(now) > time.Second {
		t.Fatalf("wrong live delay: %v", ready.Sub(now))
	}
	if err := a.enqueuePrompts([]queuedPrompt{offline}); err != nil {
		t.Fatal(err)
	}
	deadline := a.state.QueueReadyAt
	if deadline < now.Add(promptCollectTime-time.Millisecond).UnixMilli() || orderedQueue(a.state.Queue)[0].ID != offline.ID {
		t.Fatal("offline backlog did not extend collection and retain creation order")
	}
	if err := a.enqueuePrompts([]queuedPrompt{{ID: uuid.NewString(), Text: "another", At: now.UnixMilli()}}); err != nil {
		t.Fatal(err)
	}
	if a.state.QueueReadyAt != deadline {
		t.Fatal("more backlog postponed an already-open collection window")
	}
}
