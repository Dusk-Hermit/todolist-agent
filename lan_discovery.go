package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"time"
)

var discoveryGroup = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 167), Port: lanPort}

type lanAnnouncement struct {
	ID       string `json:"id"`
	Port     int    `json:"port"`
	Announce bool   `json:"announce"`
}

type lanPacket struct {
	Announcement lanAnnouncement `json:"todolist-v1"`
	MAC          []byte          `json:"mac"`
}

func (s *lanService) discoveryMAC(a lanAnnouncement) []byte {
	body, _ := json.Marshal(a)
	mac := hmac.New(sha256.New, []byte(s.code))
	mac.Write(body)
	return mac.Sum(nil)
}

type discoverySocket struct {
	receive, send *net.UDPConn
}

type discoveryInterface struct {
	nic net.Interface
	ip  netip.Addr
}

type lanDiscovery struct {
	service *lanService
	mu      sync.Mutex
	sockets map[netip.Addr]discoverySocket
	readers sync.WaitGroup
}

func newLANDiscovery(s *lanService) *lanDiscovery {
	return &lanDiscovery{service: s, sockets: make(map[netip.Addr]discoverySocket)}
}

func (d *lanDiscovery) run() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		d.updateInterfaces()
		d.announce(true)
		select {
		case <-d.service.ctx.Done():
			d.mu.Lock()
			for _, pair := range d.sockets {
				pair.receive.Close()
				pair.send.Close()
			}
			clear(d.sockets)
			d.mu.Unlock()
			d.readers.Wait()
			return
		case <-ticker.C:
		}
	}
}

func isLANIP(ip netip.Addr) bool {
	return ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLoopback()
}

func (d *lanDiscovery) updateInterfaces() {
	interfaces, err := discoveryInterfaces()
	if err != nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	active := make(map[netip.Addr]bool)
	for _, iface := range interfaces {
		ip := iface.ip
		if !ip.Is4() || !isLANIP(ip) {
			continue
		}
		active[ip] = true
		if _, present := d.sockets[ip]; present {
			continue
		}
		receive, err := net.ListenMulticastUDP("udp4", &iface.nic, discoveryGroup)
		if err != nil {
			continue
		}
		send, err := net.ListenUDP("udp4", &net.UDPAddr{IP: ip.AsSlice()})
		if err != nil {
			receive.Close()
			continue
		}
		if err := setMulticastInterface(send, ip.As4()); err != nil {
			receive.Close()
			send.Close()
			continue
		}
		// Windows applies multicast loopback on the receiving socket. The
		// device ID suppresses our own packets while permitting local peers.
		if err := setMulticastInterface(receive, ip.As4()); err != nil {
			receive.Close()
			send.Close()
			continue
		}
		d.sockets[ip] = discoverySocket{receive: receive, send: send}
		d.readers.Go(func() { d.listen(receive) })
	}
	for ip, pair := range d.sockets {
		if !active[ip] {
			pair.receive.Close()
			pair.send.Close()
			delete(d.sockets, ip)
		}
	}
}

func (d *lanDiscovery) announce(announce bool) {
	s := d.service
	a := lanAnnouncement{ID: s.id, Port: s.port, Announce: announce}
	body, _ := json.Marshal(lanPacket{Announcement: a, MAC: s.discoveryMAC(a)})
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, pair := range d.sockets {
		_, _ = pair.send.WriteToUDP(body, discoveryGroup)
	}
}

func (d *lanDiscovery) listen(socket *net.UDPConn) {
	s := d.service
	buf := make([]byte, 1024)
	for {
		n, src, err := socket.ReadFromUDPAddrPort(buf)
		if err != nil {
			return
		}
		var packet lanPacket
		if !isLANIP(src.Addr()) || json.Unmarshal(buf[:n], &packet) != nil {
			continue
		}
		a := packet.Announcement
		if a.ID == "" || a.ID == s.id || a.Port < 1 || a.Port > 65535 || !hmac.Equal(packet.MAC, s.discoveryMAC(a)) {
			continue
		}
		address := net.JoinHostPort(src.Addr().String(), strconv.Itoa(a.Port))
		s.mu.Lock()
		key := a.ID + "|" + address
		prev := s.discovered[key]
		s.discovered[key] = lanSighting{address: address, at: time.Now()}
		s.mu.Unlock()
		if prev.address != address {
			s.signal()
		}
		if a.Announce {
			d.announce(false)
		}
	}
}
