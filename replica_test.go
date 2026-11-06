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

func TestReplicaTaskConflictPreservesOtherEditsAndDeletes(t *testing.T) {
	seed := replica{Device: "seed"}
	seed.capture(map[string]json.RawMessage{
		"todo:shared": json.RawMessage(`"base"`), "todo:delete": json.RawMessage(`"old"`),
	}, 100)
	left, right, third := seed, seed, seed
	left.Device, right.Device, third.Device = "desktop", "phone", "other-desktop"
	left.capture(map[string]json.RawMessage{"todo:shared": json.RawMessage(`"left"`), "todo:a": json.RawMessage(`"a"`)}, 200)
	right.capture(map[string]json.RawMessage{
		"todo:shared": json.RawMessage(`"right"`), "todo:b": json.RawMessage(`"b"`), "todo:delete": json.RawMessage(`"old"`),
	}, 300)
	third.capture(map[string]json.RawMessage{
		"todo:shared": json.RawMessage(`"base"`), "todo:c": json.RawMessage(`"c"`), "todo:delete": json.RawMessage(`"old"`),
	}, 400)
	for _, order := range [][]replica{{left, right, third}, {third, left, right}, {right, third, left}} {
		state := order[0]
		for _, peer := range append(order[1:], seed) {
			state.merge(peer)
		}
		for key, want := range map[string]string{
			"todo:shared": `"right"`, "todo:a": `"a"`, "todo:b": `"b"`, "todo:c": `"c"`, "todo:delete": "",
		} {
			if got := string(state.Tasks[key].Value); got != want {
				t.Fatalf("%s = %s, want %s", key, got, want)
			}
		}
	}
}

func TestReplicaReorderDoesNotDiscardConcurrentContentEdit(t *testing.T) {
	left := replica{Device: "desktop"}
	left.capture(map[string]json.RawMessage{"todo:task": json.RawMessage(`{"id":"task","title":"base","updatedAt":100,"position":1}`)}, 100)
	right := left
	right.Device = "phone"
	edit := json.RawMessage(`{"id":"task","title":"edited","updatedAt":200,"position":1}`)
	left.capture(map[string]json.RawMessage{"todo:task": edit}, 200)
	right.capture(map[string]json.RawMessage{"todo:task": json.RawMessage(`{"id":"task","title":"base","updatedAt":100,"position":4}`)}, 300)
	left.merge(right)
	if got := string(left.Tasks["todo:task"].Value); got != string(edit) {
		t.Fatalf("unrelated reordering replaced the task edit: %s", got)
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

func TestReplicaDeltaRejectsOverflowedClock(t *testing.T) {
	delta := replicaDelta{Manifest: replicaManifest{Clock: vector{"device": ^uint64(0)}}}
	if _, err := delta.expand(replica{}); err == nil {
		t.Fatal("replica accepted a clock that would wrap on the next edit")
	}
}

func TestReplicaDeltaRejectsRecordClockAheadOfManifest(t *testing.T) {
	record := replicaRecord{Clock: vector{"device": 2}}
	delta := replicaDelta{
		Manifest: replicaManifest{Clock: vector{"device": 1}, Versions: map[string]string{"todo:x": record.version()}},
		Records:  map[string]replicaRecord{"todo:x": record},
	}
	if _, err := delta.expand(replica{}); err == nil {
		t.Fatal("replica accepted a record clock ahead of its manifest")
	}
}
