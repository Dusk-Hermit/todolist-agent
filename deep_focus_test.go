package main

import (
	"slices"
	"testing"
	"time"
)

func TestDeepFocusReplicatesLockCompletionAndExit(t *testing.T) {
	desktop, phone := isolatedApp(t), isolatedApp(t)
	if err := desktop.bindingStartDeepFocus(300, false); err == nil {
		t.Fatal("started without a selected task")
	}
	parent, err := desktop.bindingAddTodo(TodoInput{Title: "Parent"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := desktop.bindingAddTodo(TodoInput{Title: "Child", ParentID: parent.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := desktop.bindingSetFocus(child.ID); err != nil {
		t.Fatal(err)
	}
	if err := desktop.bindingStartDeepFocus(86410, false); err == nil {
		t.Fatal("accepted a duration longer than 24 hours")
	}
	if err := desktop.bindingStartDeepFocus(10, true); err != nil {
		t.Fatal(err)
	}
	session := desktop.snapshot().DeepFocus
	if err := phone.mergeReplica(desktop.replicaSnapshot()); err != nil {
		t.Fatal(err)
	}
	if got := phone.snapshot().DeepFocus; got != session || got.TodoID != child.ID {
		t.Fatalf("peer received the wrong session: %+v", got)
	}
	if err := phone.bindingExitDeepFocus(session.ID); err == nil {
		t.Fatal("peer bypassed the lock")
	}

	desktop.mu.Lock()
	next := desktop.state
	next.DeepFocus.StartedAt -= 10000
	next.DeepFocus.EndsAt = time.Now().Add(-time.Millisecond).UnixMilli()
	err = desktop.persistLocked(next)
	desktop.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		state := desktop.snapshot()
		if slices.ContainsFunc(state.Events, func(event HistoryEvent) bool { return event.Kind == "deep-focus.complete" }) {
			if state.DeepFocus.Exited || state.FocusID != child.ID {
				t.Fatal("completion exited the screen or cleared task focus")
			}
			for _, todo := range state.Todos {
				if todo.ID == child.ID && todo.Status != statusTodo {
					t.Fatal("completion changed the task status")
				}
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("completion did not enter history")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := phone.mergeReplica(desktop.replicaSnapshot()); err != nil {
		t.Fatal(err)
	}
	if err := phone.bindingExitDeepFocus(session.ID); err != nil {
		t.Fatal(err)
	}
	if err := desktop.mergeReplica(phone.replicaSnapshot()); err != nil {
		t.Fatal(err)
	}
	if state := desktop.snapshot(); !state.DeepFocus.Exited ||
		!slices.ContainsFunc(state.Events, func(event HistoryEvent) bool { return event.Kind == "deep-focus.exit" }) {
		t.Fatal("peer exit did not replicate")
	}
}
