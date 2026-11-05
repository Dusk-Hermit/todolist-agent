package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBridgeAddsDailyTasksAtEndOfEachGroup(t *testing.T) {
	a := isolatedApp(t)
	a.state.Todos = []Todo{
		{ID: "high", Title: "已有高优先级", Status: statusTodo, Priority: priorityHigh, Position: 4},
		{ID: "normal", Title: "已有中优先级", Status: statusTodo, Priority: priorityNormal, Position: 70},
		{ID: "doing", Title: "已有进行中", Status: statusDoing, Priority: priorityHigh, Position: 90},
		{ID: "child", ParentID: "high", Title: "已有子任务", Status: statusTodo, Priority: priorityLow, Position: 8},
	}
	b := newBridge(a)
	b.token = randomToken()
	add := func(body string) Todo {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/api/todos", bytes.NewBufferString(body))
		request.Header.Set("Authorization", "Bearer "+b.token)
		response := httptest.NewRecorder()
		b.handleTodos(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("bridge returned %d: %s", response.Code, response.Body)
		}
		var payload struct{ Todo Todo }
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		return payload.Todo
	}
	first := add(`{"action":"add","title":"第一条日常任务","priority":"high"}`)
	second := add(`{"action":"add","title":"第二条日常任务","priority":"high","placement":"last"}`)
	child := add(`{"action":"add","title":"新子任务","parentId":"high","priority":"high"}`)
	directed := add(`{"action":"add","title":"明确要求放前面","priority":"high","placement":"first"}`)
	if first.Position != 5 || second.Position != 6 || child.Position != 9 || directed.Position >= 4 {
		t.Fatalf("unexpected positions: first=%d second=%d child=%d directed=%d", first.Position, second.Position, child.Position, directed.Position)
	}
	manual, err := a.bindingAddTodo(TodoInput{Title: "手动任务", Priority: priorityHigh})
	if err != nil || manual.Position >= directed.Position {
		t.Fatalf("manual placement changed: %d, %v", manual.Position, err)
	}
	if a.state.Todos[0].Position != 4 || a.state.Todos[1].Position != 70 {
		t.Fatal("inserting a task rewrote existing task positions")
	}
}
