package main

import "testing"

func TestSubtaskFocusPersistsAndClearsWhenUnavailable(t *testing.T) {
	for _, action := range []struct {
		name   string
		parent bool
		status string
	}{
		{name: "complete child", status: statusDone},
		{name: "delete child"},
		{name: "complete parent", parent: true, status: statusDone},
		{name: "delete parent", parent: true},
	} {
		t.Run(action.name, func(t *testing.T) {
			a := isolatedApp(t)
			parent, err := a.bindingAddTodo(TodoInput{Title: "parent"})
			if err != nil {
				t.Fatal(err)
			}
			child, err := a.bindingAddTodo(TodoInput{Title: "child", ParentID: parent.ID})
			if err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{parent.ID, child.ID} {
				if _, err := a.bindingSetFocus(id); err != nil {
					t.Fatal(err)
				}
			}
			a.saveNow()
			restarted := isolatedApp(t)
			restarted.dataPath = a.dataPath
			if err := restarted.load(); err != nil {
				t.Fatal(err)
			}
			peer := isolatedApp(t)
			if err := peer.mergeReplica(restarted.replicaSnapshot()); err != nil {
				t.Fatal(err)
			}
			for _, app := range []*App{restarted, peer} {
				if got := app.snapshot().FocusID; got != child.ID {
					t.Fatalf("expected child focus, got %q", got)
				}
			}
			target := child.ID
			if action.parent {
				target = parent.ID
			}
			if action.status == "" {
				_, err = peer.bindingDeleteTodo(target)
			} else {
				_, err = peer.bindingSetTodoStatus(target, action.status)
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := peer.snapshot().FocusID; got != "" {
				t.Fatalf("expected focus to clear, got %q", got)
			}
			if _, err := peer.bindingSetFocus(child.ID); err == nil {
				t.Fatal("focused an unavailable subtask")
			}
		})
	}
}
