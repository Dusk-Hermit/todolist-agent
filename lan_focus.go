package main

import (
	"fmt"
	"log"
	"net/http"
	"strings"
)

func deepFocusRecord(key string) bool {
	return key == "deep-focus" || strings.HasPrefix(key, "event:deep-focus-")
}

func (a *App) deepFocusReplica() replica {
	a.mu.Lock()
	defer a.mu.Unlock()
	created := a.state.Replica.Device == ""
	if a.captureReplicaLocked() || created {
		a.dirty = true
	}
	current := a.state.Replica
	result := replica{Device: current.Device, Clock: current.Clock, Tasks: make(map[string]replicaRecord)}
	for key, record := range current.Tasks {
		if deepFocusRecord(key) {
			result.Tasks[key] = record
		}
	}
	return result
}

func decodeFocusDelta(delta replicaDelta) (replica, error) {
	if delta.Manifest.Device == "" || delta.Manifest.List != (replicaStamp{}) {
		return replica{}, fmt.Errorf("无效的专注同步清单")
	}
	for key := range delta.Manifest.Versions {
		if !deepFocusRecord(key) {
			return replica{}, fmt.Errorf("专注同步仅接受专注状态和操作记录")
		}
	}
	return delta.expand(replica{})
}

func (s *lanService) handleDeepFocus(w http.ResponseWriter, r *http.Request) {
	if !requireMergeVersion(w, r) {
		return
	}
	var delta replicaDelta
	if !readLAN(w, r, &delta) {
		return
	}
	remote, err := decodeFocusDelta(delta)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.app.mergeLANReplica(remote, true); err != nil {
		log.Printf("LAN focus sync: %v", err)
		http.Error(w, "could not save focus state", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, s.app.deepFocusReplica().delta(replicaManifest{}))
}

func (s *lanService) exchangeDeepFocus(peer lanPeer) error {
	var delta replicaDelta
	if err := s.request("POST", peer.Address, "/focus", s.app.deepFocusReplica().delta(replicaManifest{}), &delta); err != nil {
		return err
	}
	if delta.Manifest.Device != peer.ID {
		return fmt.Errorf("设备标识已改变")
	}
	remote, err := decodeFocusDelta(delta)
	if err != nil {
		return err
	}
	return s.app.mergeLANReplica(remote, true)
}
