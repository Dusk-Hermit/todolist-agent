package main

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestBackendInitializationFailureStillReconcilesOriginalPrompt(t *testing.T) {
	a := isolatedApp(t)
	c := newDSHClient(a)
	a.dsh = c
	prompt := &activePrompt{queuedPrompt: queuedPrompt{ID: "pending"}, Session: "original"}
	a.state.ActivePrompt = prompt
	a.state.Messages = []ChatMessage{{ID: "pending", Kind: "input", Status: "accepted", SessionID: "original"}}
	conn := testConnector(t, func(request testConnectorRequest) string {
		switch request.Method {
		case "todolist/backend":
			return `{"items":[{"sessionId":"new"}],"backend":{"phase":"failed","reason":"interrupted"}}`
		case "todolist/status", "todolist/wait":
			if request.Args.Request.SessionID != "original" {
				t.Errorf("receipt query changed to %s", request.Args.Request.SessionID)
			}
			return `{"stage":"completed","turn":1,"response":"done"}`
		default:
			t.Errorf("unexpected call %s", request.Method)
			return `{}`
		}
	})
	root := t.TempDir()
	t.Setenv("USERPROFILE", root)
	u, err := url.Parse(conn.base)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".dsh")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	config, err := json.Marshal(map[string]any{"version": 1, "port": port, "key": conn.secret})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "todolist-connector.json"), config, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := c.connect(t.Context()); err == nil {
		t.Fatal("initialization failure was hidden")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state.ActivePrompt != nil || a.state.Messages[0].Status != "sent" {
		t.Fatal("new backend initialization blocked the original message receipt")
	}
}
