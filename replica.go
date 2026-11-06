package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

type vector map[string]uint64

// compare reports causality. Concurrent and equal versions both return zero.
func (v vector) compare(other vector) int {
	earlier, later := false, false
	for id, n := range v {
		earlier = earlier || n < other[id]
		later = later || n > other[id]
	}
	for id, n := range other {
		earlier = earlier || n > v[id]
	}
	if earlier == later {
		return 0
	}
	if later {
		return 1
	}
	return -1
}

func (v vector) join(other vector) vector {
	result := make(vector, len(v)+len(other))
	maps.Copy(result, v)
	for id, n := range other {
		result[id] = max(result[id], n)
	}
	return result
}

type replicaStamp struct {
	At     int64  `json:"at"`
	Device string `json:"device"`
}

func (s replicaStamp) newer(other replicaStamp) bool {
	return s.At > other.At || (s.At == other.At && s.Device > other.Device)
}

type replicaRecord struct {
	Clock vector       `json:"clock"`
	Stamp replicaStamp `json:"stamp"`
	// An absent value is a durable deletion.
	Value json.RawMessage `json:"value,omitempty"`
}

type replica struct {
	Device string                   `json:"device"`
	Clock  vector                   `json:"clock"`
	List   replicaStamp             `json:"list"`
	Tasks  map[string]replicaRecord `json:"records"`
}

func listRecord(key string) bool {
	return key == "focus" || strings.HasPrefix(key, "todo:")
}

func (r *replica) capture(values map[string]json.RawMessage, now int64) bool {
	changes := make(map[string]json.RawMessage)
	for key, value := range values {
		if !bytes.Equal(r.Tasks[key].Value, value) {
			changes[key] = value
		}
	}
	for key, record := range r.Tasks {
		if listRecord(key) && len(record.Value) != 0 && values[key] == nil {
			changes[key] = nil
		}
	}
	if len(changes) == 0 {
		return false
	}
	clock := r.Clock.join(nil)
	clock[r.Device]++
	stamp := replicaStamp{At: max(now, r.List.At+1), Device: r.Device}
	records := make(map[string]replicaRecord, len(r.Tasks)+len(changes))
	maps.Copy(records, r.Tasks)
	for key, value := range changes {
		records[key] = replicaRecord{Clock: clock, Stamp: stamp, Value: value}
		if listRecord(key) {
			r.List = stamp
		}
	}
	r.Tasks, r.Clock = records, clock
	return true
}

// Merge each record independently. Causal successors win before timestamps
// are considered, and unrelated records keep their own versions.
func (r *replica) merge(other replica) bool {
	records := make(map[string]replicaRecord, len(r.Tasks)+len(other.Tasks))
	maps.Copy(records, r.Tasks)
	clock := r.Clock.join(other.Clock)
	changed := false
	for key, remote := range other.Tasks {
		local, exists := records[key]
		order := local.Clock.compare(remote.Clock)
		if !exists || order < 0 {
			records[key] = remote
			changed = true
			continue
		}
		if order > 0 || (maps.Equal(local.Clock, remote.Clock) &&
			local.Stamp == remote.Stamp && bytes.Equal(local.Value, remote.Value)) {
			continue
		}
		picked := local
		if remoteRecordWins(key, local, remote) {
			picked = remote
		}
		picked.Clock = local.Clock.join(remote.Clock)
		records[key] = picked
		changed = true
	}
	list := r.List
	if other.List.newer(r.List) {
		list = other.List
	}
	changed = changed || list != r.List || !maps.Equal(clock, r.Clock)
	if changed {
		r.Clock, r.List, r.Tasks = clock, list, records
	}
	return changed
}

func remoteRecordWins(key string, local, remote replicaRecord) bool {
	if strings.HasPrefix(key, "todo:") {
		leftAt, rightAt := taskChangeTime(local), taskChangeTime(remote)
		if leftAt != rightAt {
			return rightAt > leftAt
		}
		if (len(local.Value) == 0) != (len(remote.Value) == 0) {
			return len(remote.Value) == 0
		}
	}
	return remote.Stamp.newer(local.Stamp) ||
		(remote.Stamp == local.Stamp && bytes.Compare(remote.Value, local.Value) > 0)
}

func taskChangeTime(record replicaRecord) int64 {
	// An order-only move can change Position without editing the task.
	var task Todo
	if len(record.Value) != 0 && json.Unmarshal(record.Value, &task) == nil && task.UpdatedAt > 0 {
		return task.UpdatedAt
	}
	return record.Stamp.At
}

type replicaManifest struct {
	Device   string            `json:"device"`
	Clock    vector            `json:"clock"`
	List     replicaStamp      `json:"list"`
	Versions map[string]string `json:"versions"`
}

func (r replica) manifest() replicaManifest {
	out := replicaManifest{Device: r.Device, Clock: r.Clock, List: r.List, Versions: make(map[string]string, len(r.Tasks))}
	for key, record := range r.Tasks {
		out.Versions[key] = record.version()
	}
	return out
}

func (r replicaRecord) version() string {
	data, _ := json.Marshal(r)
	sum := sha256.Sum256(data)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

type replicaDelta struct {
	Manifest replicaManifest          `json:"manifest"`
	Records  map[string]replicaRecord `json:"records"`
}

func (r replica) delta(known replicaManifest) replicaDelta {
	delta := replicaDelta{Manifest: r.manifest(), Records: make(map[string]replicaRecord)}
	for key, version := range delta.Manifest.Versions {
		if known.Versions[key] != version {
			delta.Records[key] = r.Tasks[key]
		}
	}
	return delta
}

func (d replicaDelta) expand(base replica) (replica, error) {
	// Leave room for local increments and keep counters exact in JSON clients.
	const maxClockCounter = 1<<53 - 1
	for _, counter := range d.Manifest.Clock {
		if counter > maxClockCounter {
			return replica{}, fmt.Errorf("同步版本计数超出范围")
		}
	}
	out := replica{Device: d.Manifest.Device, Clock: d.Manifest.Clock, List: d.Manifest.List,
		Tasks: make(map[string]replicaRecord, len(d.Manifest.Versions))}
	for key, version := range d.Manifest.Versions {
		record, ok := d.Records[key]
		if !ok {
			record, ok = base.Tasks[key]
		}
		if !ok || record.version() != version {
			return replica{}, fmt.Errorf("同步增量不完整")
		}
		for device, counter := range record.Clock {
			if counter > out.Clock[device] {
				return replica{}, fmt.Errorf("同步条目版本超出清单范围")
			}
		}
		out.Tasks[key] = record
	}
	return out, nil
}

func replicaValues(state persistedState) map[string]json.RawMessage {
	values := make(map[string]json.RawMessage, len(state.Todos)+len(state.Events)+len(state.Messages)+1)
	add := func(key string, value any) {
		values[key], _ = json.Marshal(value)
	}
	for _, t := range state.Todos {
		t.Number = ""
		add("todo:"+t.ID, t)
	}
	if state.FocusID != "" {
		add("focus", state.FocusID)
	}
	for _, m := range state.Messages {
		if m.Kind != "onboarding" {
			add("message:"+m.ID, m)
		}
	}
	for _, e := range state.Events {
		if e.Kind != "onboarding" {
			add("event:"+e.ID, e)
		}
	}
	return values
}

func (a *App) captureReplicaLocked() bool {
	r := &a.state.Replica
	at := time.Now().UnixMilli()
	if r.Device == "" {
		r.Device = uuid.NewString()
		// Give migrated lists the timestamps of their existing edits.
		at = 0
		for _, t := range a.state.Todos {
			at = max(at, t.UpdatedAt)
		}
	}
	return r.capture(replicaValues(a.state), at)
}

func (r replica) applyTo(state *persistedState) error {
	state.Todos = []Todo{}
	state.FocusID = ""
	messages := []ChatMessage{}
	events := []HistoryEvent{}
	for _, m := range state.Messages {
		if m.Kind == "onboarding" {
			messages = append(messages, m)
		}
	}
	for _, e := range state.Events {
		if e.Kind == "onboarding" {
			events = append(events, e)
		}
	}
	for key, record := range r.Tasks {
		if len(record.Value) == 0 {
			continue
		}
		var err error
		switch {
		case key == "focus":
			err = json.Unmarshal(record.Value, &state.FocusID)
		case strings.HasPrefix(key, "todo:"):
			var t Todo
			err = json.Unmarshal(record.Value, &t)
			state.Todos = append(state.Todos, t)
		case strings.HasPrefix(key, "message:"):
			var m ChatMessage
			err = json.Unmarshal(record.Value, &m)
			messages = append(messages, m)
		case strings.HasPrefix(key, "event:"):
			var e HistoryEvent
			err = json.Unmarshal(record.Value, &e)
			events = append(events, e)
		}
		if err != nil {
			return err
		}
	}
	// A concurrent child addition can outlive its parent's deletion.
	roots := make(map[string]bool, len(state.Todos))
	for _, t := range state.Todos {
		roots[t.ID] = t.ParentID == ""
	}
	for i := range state.Todos {
		t := &state.Todos[i]
		if t.ParentID != "" && !roots[t.ParentID] {
			t.ParentID = ""
		}
	}
	slices.SortFunc(state.Todos, func(a, b Todo) int { return strings.Compare(a.ID, b.ID) })
	slices.SortFunc(messages, compareMessages)
	slices.SortFunc(events, func(a, b HistoryEvent) int {
		if a.At != b.At {
			if a.At < b.At {
				return -1
			}
			return 1
		}
		return strings.Compare(a.ID, b.ID)
	})
	state.Messages, state.Events = messages, events
	normalizeState(state)
	return nil
}
