package main

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestQueuedResultUsesCompletedSessionEvents(t *testing.T) {
	var hits atomic.Int32
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/session/events" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":{"ok":true,"value":{"hasMore":false,"records":[
          {"event":{"type":"turn/start","seq":11,"data":{"turn":2}}},
          {"event":{"type":"user/message","seq":12,"data":{"content":[{"type":"text","text":"test\n<!-- todo-msg:first -->"}]}}},
          {"event":{"type":"assistant/message","seq":13,"data":{"turn":2,"message":{"content":[{"type":"text","text":"完成"}]}}}},
          {"event":{"type":"turn/end","seq":14,"data":{"turn":2,"reason":{"kind":"completed"}}}},
          {"event":{"type":"turn/start","seq":15,"data":{"turn":3}}},
          {"event":{"type":"user/message","seq":16,"data":{"content":[{"type":"text","text":"test\n<!-- todo-msg:second -->"}]}}},
          {"event":{"type":"turn/end","seq":17,"data":{"turn":3,"reason":{"kind":"cancelled"}}}}
        ]}}}`))
	}))
	defer mock.Close()
	c := newDSHClient(isolatedApp(t))
	c.baseURL, c.secret = mock.URL, []byte("test signing key")
	row := sessionRow{SessionID: "test"}
	row.Projections.AsOfSeq = 17
	for id, want := range map[string]bool{"first": true, "second": false, "missing": false} {
		p := &activePrompt{queuedPrompt: queuedPrompt{ID: id}, Session: "test", PreviousSeq: 10}
		response, ok := c.queuedResult(t.Context(), p, &row)
		if ok != want || (ok && response != "完成") {
			t.Fatalf("prompt %s got %q, %t", id, response, ok)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("history was read %d times", hits.Load())
	}
}
