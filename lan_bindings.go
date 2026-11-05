package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/netip"
	"slices"
	"strings"
	"time"
)

type lanConfig struct {
	Code      string `json:"code,omitempty"`
	Address   string `json:"address,omitempty"`
	DSHDevice string `json:"dshDevice,omitempty"`
}

type lanView struct {
	lanConfig
	Peers     []lanPeer      `json:"peers"`
	ActiveDSH string         `json:"activeDSH"`
	Pending   []queuedPrompt `json:"pending"`
	Error     string         `json:"error,omitempty"`
}

func normalizeLANAddress(address string) (string, error) {
	parts := strings.FieldsFunc(address, func(r rune) bool {
		return r == ',' || r == '，' || r == ' ' || r == '\n' || r == '\r' || r == '\t'
	})
	if len(parts) > 16 {
		return "", fmt.Errorf("最多可填写 16 个手动设备地址")
	}
	for i, part := range parts {
		if ip, err := netip.ParseAddr(part); err == nil {
			part = netip.AddrPortFrom(ip, lanPort).String()
		}
		ap, err := netip.ParseAddrPort(part)
		if err != nil || ap.Port() == 0 || !ap.Addr().Is4() || !isLANIP(ap.Addr()) {
			return "", fmt.Errorf("请输入局域网 IPv4 地址，可附加端口")
		}
		parts[i] = ap.String()
	}
	return strings.Join(parts, ","), nil
}

func (a *App) bindingLANConfigure(code, address string) (lanView, error) {
	a.lanConfigMu.Lock()
	defer a.lanConfigMu.Unlock()
	code = strings.ToLower(strings.TrimSpace(code))
	if code == "generate" {
		code = randomToken()
	}
	if code != "" {
		if b, err := hex.DecodeString(code); err != nil || len(b) != 32 {
			return lanView{}, fmt.Errorf("请粘贴由 TodoList 生成的配对码")
		}
	}
	address, err := normalizeLANAddress(address)
	if err != nil {
		return lanView{}, err
	}
	a.mu.Lock()
	next := a.state
	if next.LAN.Code != code {
		next.LAN.DSHDevice = ""
	}
	next.LAN.Code, next.LAN.Address = code, address
	err = a.persistLocked(next)
	a.mu.Unlock()
	if err != nil {
		return lanView{}, err
	}
	a.replaceLAN()
	return a.bindingLANState(), nil
}

func (a *App) replaceLAN() {
	a.mu.Lock()
	previous := a.lan
	a.lan = nil
	cfg := a.state.LAN
	a.lanError = ""
	a.mu.Unlock()
	if previous != nil {
		previous.close()
	}
	if cfg.Code != "" {
		replica := a.replicaSnapshot()
		a.saveNow()
		service, err := newLAN(a, cfg, replica.Device, lanPort)
		a.mu.Lock()
		if err != nil {
			a.lanError = err.Error()
		} else {
			a.lan = service
		}
		a.mu.Unlock()
		if service != nil {
			service.start()
		}
	}
	a.updateLANStatus(a.getLAN())
}

func (a *App) startSavedLAN() {
	a.lanConfigMu.Lock()
	defer a.lanConfigMu.Unlock()
	if a.getLAN() == nil {
		a.replaceLAN()
	}
}

func (a *App) stopLAN() {
	a.lanConfigMu.Lock()
	defer a.lanConfigMu.Unlock()
	a.mu.Lock()
	previous := a.lan
	a.lan = nil
	a.mu.Unlock()
	if previous != nil {
		previous.close()
	}
	a.updateLANStatus(nil)
}

func (a *App) getLAN() *lanService {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lan
}

func (a *App) bindingLANState() lanView {
	a.mu.Lock()
	s := a.lan
	view := lanView{lanConfig: a.state.LAN, Peers: []lanPeer{}, Error: a.lanError,
		Pending: orderedQueue(a.state.Queue)}
	a.mu.Unlock()
	if s != nil {
		view.Peers = s.availablePeers()
		if peer := chooseDSH(view.Peers, view.DSHDevice); peer != nil {
			view.ActiveDSH = peer.ID
		}
	}
	return view
}

func (s *lanService) availablePeers() []lanPeer {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.peers)
}

func chooseDSH(peers []lanPeer, preferred string) *lanPeer {
	var fallback *lanPeer
	for i := range peers {
		p := &peers[i]
		if !p.canDispatch() {
			continue
		}
		if p.ID == preferred {
			return p
		}
		if fallback == nil || p.ID < fallback.ID {
			fallback = p
		}
	}
	return fallback
}

func (p lanPeer) canDispatch() bool {
	return p.Platform == "windows" && p.Dispatch && p.DSHAddress != "" &&
		p.DSH.Connected && p.DSH.SessionID != ""
}

func (a *App) updateLANStatus(s *lanService) {
	if a.getLAN() != s {
		return
	}
	view := a.bindingLANState()
	if platformName == "android" {
		status := DSHStatus{}
		for _, p := range view.Peers {
			if p.ID == view.ActiveDSH {
				status = p.DSH
				status.BaseURL = p.DSHAddress
				break
			}
		}
		if a.setStatus(status) {
			a.emitStatus()
		}
	}
	a.emit("lan", view)
}

func (a *App) bindingLANSelect(id string) lanView {
	a.mu.Lock()
	a.state.LAN.DSHDevice = id
	a.mu.Unlock()
	a.markDirty()
	s := a.getLAN()
	a.updateLANStatus(s)
	if s != nil {
		s.signal()
	}
	return a.bindingLANState()
}

func (a *App) bindingLANRefresh() (bool, error) {
	s := a.getLAN()
	if s == nil {
		a.startSavedLAN()
		s = a.getLAN()
		if s == nil {
			view := a.bindingLANState()
			if view.Error != "" {
				return false, errors.New(view.Error)
			}
			return false, fmt.Errorf("请先在设置中启用局域网同步")
		}
	}
	go func() {
		count, err := s.refresh(true)
		message := ""
		if err != nil {
			message = err.Error()
		}
		a.emit("lan-refresh", map[string]any{"count": count, "error": message})
	}()
	return true, nil
}

func (a *App) replicaSnapshot() replica {
	a.mu.Lock()
	defer a.mu.Unlock()
	created := a.state.Replica.Device == ""
	if a.captureReplicaLocked() || created {
		a.dirty = true
	}
	return a.state.Replica
}

func (a *App) replicaForExchange() (replica, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.batchPausedLocked() {
		return replica{}, errBatchRunning
	}
	if a.captureReplicaLocked() {
		a.dirty = true
	}
	return a.state.Replica, nil
}

func (a *App) mergeReplica(other replica) error {
	a.mu.Lock()
	if a.batchPausedLocked() {
		a.mu.Unlock()
		return errBatchRunning
	}
	if a.captureReplicaLocked() {
		a.dirty = true
	}
	next := a.state
	if !next.Replica.merge(other) {
		a.mu.Unlock()
		return nil
	}
	err := next.Replica.applyTo(&next)
	if err == nil {
		next.UncertainPrompts = maps.Clone(next.UncertainPrompts)
		restorePromptCompletions(&next)
		if a.dsh == nil && len(next.Queue) != 0 {
			next.Queue = maps.Clone(next.Queue)
			for _, m := range next.Messages {
				if next.Queue[m.ID].Host != "" {
					delete(next.Queue, m.ID)
				}
			}
		}
		err = a.persistLocked(next)
	}
	a.mu.Unlock()
	if err == nil {
		a.emitState()
	}
	return err
}

// The caller holds a.mu. Update the in-memory state only after the same
// snapshot is safely on disk.
func (a *App) persistLocked(next persistedState) error {
	if next.Replica.Device != "" {
		next.Replica.capture(replicaValues(next), time.Now().UnixMilli())
	}
	payload, err := json.MarshalIndent(next, "", "  ")
	if err == nil {
		err = atomicWrite(a.dataPath, payload)
	}
	if err == nil {
		a.state = next
		a.dirty = false
	}
	return err
}
