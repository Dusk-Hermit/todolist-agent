package main

import "testing"

func TestLegacyDroppedTaskMigratesOnLoadAndSync(t *testing.T) {
	a := isolatedApp(t)
	a.state.Todos = []Todo{
		{ID: "archived", Title: "Archived", Status: "dropped", UpdatedAt: 10, DropReason: "old reason"},
		{ID: "active", Title: "Active child", Status: statusTodo, ParentID: "archived"},
	}
	a.state.Prefs.Filter = "dropped"
	old := a.replicaSnapshot()
	a.saveNow()
	restarted := NewApp()
	restarted.dataPath = a.dataPath
	if err := restarted.load(); err != nil {
		t.Fatal(err)
	}
	peer := isolatedApp(t)
	if err := peer.mergeReplica(old); err != nil {
		t.Fatal(err)
	}
	for _, state := range []persistedState{restarted.state, peer.state} {
		if len(state.Todos) != 1 || state.Todos[0].ID != "active" || state.Todos[0].ParentID != "" {
			t.Fatalf("unexpected migrated tasks: %+v", state.Todos)
		}
		if len(state.Replica.Tasks["todo:archived"].Value) != 0 {
			t.Fatal("missing deletion marker")
		}
		if len(state.Events) != 1 || state.Events[0].Before == nil || state.Events[0].Before.DropReason != "old reason" {
			t.Fatal("archived task was not kept in history")
		}
	}
	if restarted.state.Prefs.Filter != "all" {
		t.Fatal("removed filter survived upgrade")
	}
}
