package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

type testConnectorRequest struct {
	Method string
	Args   struct {
		Request struct {
			SessionID string
			RequestID string
		}
	}
}

func testConnector(t *testing.T, reply func(testConnectorRequest) string) dshEndpoint {
	t.Helper()
	key := bytes.Repeat([]byte{42}, 32)
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil || len(data) < gcm.NonceSize()+gcm.Overhead() || r.URL.Path != "/todolist/v1/rpc" {
			t.Errorf("invalid request to %s: %v", r.URL.Path, err)
			w.WriteHeader(400)
			return
		}
		nonce := data[:gcm.NonceSize()]
		plain, err := gcm.Open(nil, nonce, data[gcm.NonceSize():], []byte("todolist/v1/request"))
		var request testConnectorRequest
		if err != nil || json.Unmarshal(plain, &request) != nil {
			t.Error("invalid authenticated request")
			w.WriteHeader(401)
			return
		}
		result := reply(request)
		responseNonce := make([]byte, gcm.NonceSize())
		if _, err := rand.Read(responseNonce); err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		payload := []byte(`{"result":{"ok":true,"value":` + result + `}}`)
		_, _ = w.Write(gcm.Seal(responseNonce, responseNonce, payload, append([]byte("todolist/v1/response:"), nonce...)))
	}))
	t.Cleanup(s.Close)
	return dshEndpoint{base: s.URL, secret: key}
}

func TestConnectorStatusUsesOriginalSessionAndRequestID(t *testing.T) {
	c := newDSHClient(isolatedApp(t))
	c.conn = testConnector(t, func(request testConnectorRequest) string {
		if request.Method != "todolist/status" || request.Args.Request.SessionID != "original" {
			t.Errorf("wrong status target: %+v", request)
		}
		switch request.Args.Request.RequestID {
		case "first":
			return `{"stage":"completed","turn":2,"response":"完成","tools":[{"name":"read","status":"succeeded"}]}`
		case "second":
			return `{"stage":"failed","reason":"interrupted","turn":3}`
		default:
			return `{"stage":"not-found"}`
		}
	})
	for id, want := range map[string]string{"first": "sent", "second": "failed", "missing": ""} {
		p := &activePrompt{queuedPrompt: queuedPrompt{ID: id}, Session: "original"}
		outcome := c.connectorResult(t.Context(), p)
		if outcome.status != want || (want == "sent" && (outcome.response != "完成" || outcome.evidence == "")) {
			t.Fatalf("prompt %s got %+v", id, outcome)
		}
	}
}
