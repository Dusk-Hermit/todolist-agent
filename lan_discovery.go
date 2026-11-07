package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"
)

var discoveryGroup = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 167), Port: lanPort}

const (
	lanSightingTTL                 = 18 * time.Second
	maxDiscoverySightings          = 32
	maxDiscoveryAddressesPerDevice = 4
)

type lanAnnouncement struct {
	ID       string `json:"id"`
	Port     int    `json:"port"`
	Announce bool   `json:"announce"`
	SyncAt   int64  `json:"syncAt,omitempty"`
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
	d.send(announce, 0)
}

func (d *lanDiscovery) notifySync() {
	d.send(false, time.Now().UnixMilli())
}

func (d *lanDiscovery) send(announce bool, syncAt int64) {
	s := d.service
	a := lanAnnouncement{ID: s.id, Port: s.port, Announce: announce, SyncAt: syncAt}
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
		now := time.Now()
		isNew, reply := s.recordSighting(a.ID, address, a.Announce, now)
		notify := s.acceptSyncNotice(a.ID, address, a.SyncAt, now)
		if isNew || notify {
			s.signal()
		}
		if reply {
			d.announce(false)
		}
	}
}

func (s *lanService) acceptSyncNotice(id, address string, stamp int64, now time.Time) bool {
	if stamp == 0 || now.Sub(time.UnixMilli(stamp)).Abs() > lanSightingTTL {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := id + "|" + address
	p, exists := s.discovered[key]
	if !exists || stamp <= p.syncAt {
		return false
	}
	p.syncAt = stamp
	s.discovered[key] = p
	return true
}

func (s *lanService) recordSighting(id, address string, announce bool, now time.Time) (isNew, reply bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prefix := id + "|"
	key := prefix + address
	for key, sighting := range s.discovered {
		if now.Sub(sighting.at) > lanSightingTTL {
			delete(s.discovered, key)
		}
	}
	previous, existing := s.discovered[key]
	if !existing {
		if len(s.discovered) >= maxDiscoverySightings {
			return false, false
		}
		deviceAddresses := 0
		for key := range s.discovered {
			if strings.HasPrefix(key, prefix) {
				deviceAddresses++
			}
		}
		if deviceAddresses >= maxDiscoveryAddressesPerDevice {
			return false, false
		}
	}
	s.discovered[key] = lanSighting{address: address, at: now, syncAt: previous.syncAt}
	if announce && now.Sub(s.lastDiscoveryReply) >= 250*time.Millisecond {
		s.lastDiscoveryReply = now
		reply = true
	}
	return !existing, reply
}
