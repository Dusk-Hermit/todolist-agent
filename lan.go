package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
)

const lanPort = 17891
const lanBodyLimit = 32 << 20
const lanPrefix = "/todolist/v1"

type lanInfo struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Platform string    `json:"platform"`
	DSH      DSHStatus `json:"dsh"`
	Paused   bool      `json:"paused"`
}

type lanPeer struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Platform   string    `json:"platform"`
	Address    string    `json:"address"`
	DSHAddress string    `json:"dshAddress,omitempty"`
	DSH        DSHStatus `json:"-"`
	Paused     bool      `json:"paused"`
}

type lanService struct {
	app                     *App
	id, name, code, address string
	port                    int
	ctx                     context.Context
	cancel                  context.CancelFunc
	server                  *http.Server
	client                  *http.Client

	refreshMu  sync.Mutex
	mu         sync.Mutex
	peers      []lanPeer
	discovered map[string]lanSighting
	wake       chan struct{}
	discovery  *lanDiscovery
	workers    sync.WaitGroup
}

type lanSighting struct {
	address string
	at      time.Time
}

func pairingTLS(code string) (*tls.Config, *tls.Config, error) {
	seed := sha256.Sum256([]byte("todolist LAN TLS v1:" + code))
	key := ed25519.NewKeyFromSeed(seed[:])
	public := key.Public().(ed25519.PublicKey)
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Unix(0, 0), NotAfter: time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC),
		KeyUsage: x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(nil, &template, &template, public, key)
	if err != nil {
		return nil, nil, err
	}
	server := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
	}
	client := &tls.Config{
		MinVersion: tls.VersionTLS13,
		// Pairing pins the public key for changing LAN addresses.
		InsecureSkipVerify: true,
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) != 1 || !public.Equal(state.PeerCertificates[0].PublicKey) {
				return fmt.Errorf("设备配对码不匹配")
			}
			return nil
		},
	}
	return server, client, nil
}

func newLAN(a *App, cfg lanConfig, id string, port int) (*lanService, error) {
	serverTLS, clientTLS, err := pairingTLS(cfg.Code)
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp4", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		return nil, fmt.Errorf("局域网服务无法监听端口 %d: %w", port, err)
	}
	hostname, _ := os.Hostname()
	if platformName == "android" || hostname == "" {
		hostname = platformName + " · " + id[:8]
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &lanService{
		app: a, id: id, name: hostname, code: cfg.Code, address: cfg.Address,
		port: ln.Addr().(*net.TCPAddr).Port, ctx: ctx, cancel: cancel,
		client: &http.Client{
			Timeout:       5 * time.Second,
			Transport:     &http.Transport{TLSClientConfig: clientTLS, MaxIdleConns: 12, IdleConnTimeout: 30 * time.Second},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		discovered: make(map[string]lanSighting), wake: make(chan struct{}, 1),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+lanPrefix+"/info", s.handleInfo)
	mux.HandleFunc("POST "+lanPrefix+"/sync", s.handleSync)
	mux.HandleFunc("POST "+lanPrefix+"/apply", s.handleApply)
	mux.HandleFunc("POST "+lanPrefix+"/enqueue", s.handleEnqueue)
	s.server = &http.Server{ReadTimeout: 10 * time.Second, WriteTimeout: 12 * time.Second, IdleTimeout: 30 * time.Second,
		MaxHeaderBytes: 8 << 10, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !hmac.Equal([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.code)) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			mux.ServeHTTP(w, r)
		})}
	s.workers.Go(func() {
		if err := s.server.Serve(tls.NewListener(ln, serverTLS)); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("LAN server: %v", err)
		}
	})
	return s, nil
}

func (s *lanService) start() {
	s.discovery = newLANDiscovery(s)
	s.workers.Go(s.discovery.run)
	s.workers.Go(s.run)
}

func (s *lanService) close() {
	s.cancel()
	_ = s.server.Close()
	s.workers.Wait()
	s.client.CloseIdleConnections()
}

func (s *lanService) run() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		s.refresh(false)
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
		case <-s.wake:
		}
	}
}

func (s *lanService) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *lanService) request(method, address, route string, data, out any) error {
	var body io.Reader
	if data != nil {
		payload, err := json.Marshal(data)
		if err != nil {
			return err
		}
		body = bytes.NewReader(payload)
	}
	r, err := http.NewRequestWithContext(s.ctx, method, "https://"+address+lanPrefix+route, body)
	if err != nil {
		return err
	}
	r.Header.Set("Authorization", "Bearer "+s.code)
	r.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(r)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusLocked {
		return errBatchRunning
	}
	if resp.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("设备返回 %d: %s", resp.StatusCode, strings.TrimSpace(string(detail)))
	}
	return json.NewDecoder(io.LimitReader(resp.Body, lanBodyLimit)).Decode(out)
}

func (s *lanService) handleInfo(w http.ResponseWriter, _ *http.Request) {
	status := DSHStatus{}
	if s.app.dsh != nil {
		status = s.app.getStatus()
		if status.BaseURL == "" {
			s.app.mu.Lock()
			status.BaseURL = s.app.state.DSH.BaseURL
			s.app.mu.Unlock()
		}
	}
	s.app.mu.Lock()
	paused := s.app.batchPausedLocked()
	s.app.mu.Unlock()
	writeJSON(w, http.StatusOK, lanInfo{ID: s.id, Name: s.name, Platform: platformName, DSH: status, Paused: paused})
}

func readLAN(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, lanBodyLimit)).Decode(dst); err != nil {
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return false
	}
	return true
}

func (s *lanService) handleSync(w http.ResponseWriter, r *http.Request) {
	var known replicaManifest
	if !readLAN(w, r, &known) {
		return
	}
	snapshot, err := s.app.replicaForExchange()
	if err != nil {
		http.Error(w, err.Error(), http.StatusLocked)
		return
	}
	writeJSON(w, http.StatusOK, snapshot.delta(known))
}

func (s *lanService) handleApply(w http.ResponseWriter, r *http.Request) {
	var delta replicaDelta
	if !readLAN(w, r, &delta) {
		return
	}
	snapshot, err := s.app.replicaForExchange()
	if err != nil {
		http.Error(w, err.Error(), http.StatusLocked)
		return
	}
	remote, err := delta.expand(snapshot)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if err := s.app.mergeReplica(remote); err != nil {
		if errors.Is(err, errBatchRunning) {
			http.Error(w, err.Error(), http.StatusLocked)
			return
		}
		log.Printf("LAN merge: %v", err)
		http.Error(w, "could not save sync data", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *lanService) handleEnqueue(w http.ResponseWriter, r *http.Request) {
	var pending []queuedPrompt
	if !readLAN(w, r, &pending) {
		return
	}
	if s.app.dsh == nil || len(pending) > 100 {
		http.Error(w, "无效的 DSH 队列请求", http.StatusBadRequest)
		return
	}
	if err := s.app.enqueuePrompts(pending); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *lanService) uploadQueue(peer *lanPeer) error {
	if peer == nil || !peer.DSH.Connected || peer.DSH.SessionID == "" {
		return nil
	}
	s.app.mu.Lock()
	pending := orderedQueue(s.app.state.Queue)
	s.app.mu.Unlock()
	for batch := range slices.Chunk(pending, 100) {
		var result map[string]bool
		if err := s.request("POST", peer.Address, "/enqueue", batch, &result); err != nil {
			return err
		}
		if !result["ok"] {
			return fmt.Errorf("电脑未确认接收待发消息")
		}
		if err := s.app.queueUploaded(batch); err != nil {
			return err
		}
	}
	return nil
}

func (s *lanService) exchange(peer lanPeer) error {
	local, err := s.app.replicaForExchange()
	if err != nil {
		return err
	}
	var delta replicaDelta
	if err := s.request("POST", peer.Address, "/sync", local.manifest(), &delta); err != nil {
		return err
	}
	if delta.Manifest.Device != peer.ID {
		return fmt.Errorf("设备标识已改变")
	}
	remote, err := delta.expand(local)
	if err != nil {
		return err
	}
	if err := s.app.mergeReplica(remote); err != nil {
		return err
	}
	current, err := s.app.replicaForExchange()
	if err != nil {
		return err
	}
	outgoing := current.delta(delta.Manifest)
	if len(outgoing.Records) == 0 {
		return nil
	}
	var reply map[string]bool
	return s.request("POST", peer.Address, "/apply", outgoing, &reply)
}

func (s *lanService) refresh(manual bool) (int, error) {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	if s.ctx.Err() != nil {
		return 0, s.ctx.Err()
	}
	if manual {
		s.discovery.announce(true)
		if !sleepOrWake(s.ctx, nil, 800*time.Millisecond) {
			return 0, s.ctx.Err()
		}
	}
	s.mu.Lock()
	addresses := make(map[string]bool)
	for id, p := range s.discovered {
		if time.Since(p.at) > 18*time.Second {
			delete(s.discovered, id)
		} else {
			addresses[p.address] = true
		}
	}
	s.mu.Unlock()
	if s.address != "" {
		addresses[s.address] = true
	}
	peers := make([]lanPeer, 0)
	seen := make(map[string]bool)
	count := 0
	var syncErr error
	for _, addr := range slices.Sorted(maps.Keys(addresses)) {
		var info lanInfo
		if err := s.request("GET", addr, "/info", nil, &info); err != nil {
			syncErr = err
			continue
		}
		if info.ID == "" || info.ID == s.id || seen[info.ID] {
			continue
		}
		seen[info.ID] = true
		p := lanPeer{ID: info.ID, Name: info.Name, Platform: info.Platform, Address: addr,
			DSH: info.DSH, Paused: info.Paused, DSHAddress: peerDSHAddress(addr, info.DSH.BaseURL)}
		peers = append(peers, p)
	}
	if platformName == "android" {
		s.app.mu.Lock()
		preferred := s.app.state.LAN.DSHDevice
		s.app.mu.Unlock()
		if err := s.uploadQueue(chooseDSH(peers, preferred)); err != nil {
			syncErr = err
		}
	}
	for _, peer := range peers {
		if peer.Paused {
			syncErr = errBatchRunning
			continue
		}
		if err := s.exchange(peer); err != nil {
			syncErr = err
		} else {
			count++
		}
	}
	if s.ctx.Err() == nil {
		s.mu.Lock()
		s.peers = peers
		s.mu.Unlock()
		s.app.updateLANStatus(s)
	}
	return count, syncErr
}

func peerDSHAddress(addr, base string) string {
	u, err := url.Parse(base)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	ip := net.ParseIP(u.Hostname())
	if (ip != nil && ip.IsLoopback()) || u.Hostname() == "localhost" {
		host, _, _ := net.SplitHostPort(addr)
		port := u.Port()
		u.Host = host
		if port != "" {
			u.Host = net.JoinHostPort(host, port)
		}
	}
	return u.String()
}
