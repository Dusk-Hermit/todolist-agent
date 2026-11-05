package main

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"testing"
)

func TestLANRefreshDistinguishesUnavailablePeerFromLocalBatch(t *testing.T) {
	a := isolatedApp(t)
	a.dsh = newDSHClient(a)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	// An accepted TCP connection alone does not prove the peer can answer /info.
	defer listener.Close()
	address := "127.0.0.1:" + strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	s, err := newLAN(a, lanConfig{Code: randomToken(), Address: address}, a.replicaSnapshot().Device, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	if count, err := s.refresh(false); count != 0 || !errors.Is(err, errLANPeerUnreachable) {
		t.Fatalf("expected a peer-unavailable result, got count=%d err=%v", count, err)
	}
	phone := isolatedApp(t)
	if _, err := phone.bindingAddTodo(TodoInput{Title: "手机任务"}); err != nil {
		t.Fatal(err)
	}
	client, err := newLAN(phone, lanConfig{Code: s.code}, phone.replicaSnapshot().Device, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer client.close()
	if err := client.exchange(lanPeer{ID: s.id, Address: fmt.Sprintf("127.0.0.1:%d", s.port)}); err != nil {
		t.Fatal(err)
	}
	if len(a.snapshot().Todos) != 1 {
		t.Fatal("phone could not push changes without a working reverse connection")
	}
	if count, err := s.refresh(false); count != 0 || !errors.Is(err, errLANPeerPulling) {
		t.Fatalf("expected a recent inbound sync result, got count=%d err=%v", count, err)
	}
	a.mu.Lock()
	a.state.ActivePrompt = &activePrompt{}
	a.mu.Unlock()
	if count, err := s.refresh(false); count != 0 || !errors.Is(err, errBatchRunning) {
		t.Fatalf("local batch should take precedence over the failed reverse probe, got count=%d err=%v", count, err)
	}
}
