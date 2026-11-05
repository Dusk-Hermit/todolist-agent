package main

import (
	"encoding/json"
	"testing"
)

func TestReplicaConcurrentAddsAndEdits(t *testing.T) {
	left := replica{Device: "desktop"}
	left.capture(map[string]json.RawMessage{"todo:base": json.RawMessage(`"base"`)}, 100)
	right := replica{Device: "phone", Clock: left.Clock, Tasks: left.Tasks, List: left.List}
	left.capture(map[string]json.RawMessage{"todo:base": json.RawMessage(`"edited"`), "todo:a": json.RawMessage(`"a"`)}, 200)
	right.capture(map[string]json.RawMessage{"todo:base": json.RawMessage(`"base"`), "todo:b": json.RawMessage(`"b"`)}, 210)
	left.merge(right)
	right.merge(left)
	for _, state := range []replica{left, right} {
		for key, want := range map[string]string{"todo:a": `"a"`, "todo:b": `"b"`, "todo:base": `"edited"`} {
			if got := string(state.Tasks[key].Value); got != want {
				t.Fatalf("%s = %q, want %q", key, got, want)
			}
		}
	}
}

func TestReplicaConflictReplacesWholeListAndStalePeerCannotResurrect(t *testing.T) {
	seed := replica{Device: "seed"}
	seed.capture(map[string]json.RawMessage{"todo:base": json.RawMessage(`"base"`)}, 100)
	left, right := seed, seed
	left.Device, right.Device = "desktop", "phone"
	left.capture(map[string]json.RawMessage{"todo:base": json.RawMessage(`"left"`), "todo:discard": json.RawMessage(`"discard"`)}, 200)
	right.capture(map[string]json.RawMessage{"todo:keep": json.RawMessage(`"keep"`)}, 300)
	stale := left
	left.merge(right)
	right.merge(stale)
	left.merge(stale)
	for _, state := range []replica{left, right} {
		if len(state.Tasks["todo:base"].Value) != 0 || len(state.Tasks["todo:discard"].Value) != 0 ||
			string(state.Tasks["todo:keep"].Value) != `"keep"` {
			t.Fatalf("unexpected conflict result: %+v", state.Tasks)
		}
	}
}

func TestReplicaSequentialEditWithOlderWallClock(t *testing.T) {
	left := replica{Device: "desktop"}
	left.capture(map[string]json.RawMessage{"todo:base": json.RawMessage(`"base"`)}, 1000)
	right := left
	right.Device = "phone"
	right.capture(map[string]json.RawMessage{"todo:base": json.RawMessage(`"updated"`)}, 500)
	left.merge(right)
	if got := string(left.Tasks["todo:base"].Value); got != `"updated"` {
		t.Fatalf("causal edit lost to clock skew: %s", got)
	}
}
