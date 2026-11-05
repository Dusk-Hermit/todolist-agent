package main

import (
	"fmt"
	"testing"
	"time"
)

func TestDiscoveryLimitsRepeatedAnnouncements(t *testing.T) {
	s := &lanService{discovered: make(map[string]lanSighting)}
	now := time.Now()
	if newAddr, reply := s.recordSighting("phone", "192.168.1.2:17891", true, now); !newAddr || !reply {
		t.Fatal("first announcement was ignored")
	}
	if newAddr, reply := s.recordSighting("phone", "192.168.1.2:17891", true, now.Add(time.Millisecond)); newAddr || reply {
		t.Fatal("immediate replay triggered a second announcement")
	}
	for i := 3; i < 100; i++ {
		s.recordSighting("phone", fmt.Sprintf("192.168.1.%d:17891", i), true, now)
	}
	if len(s.discovered) > maxDiscoveryAddressesPerDevice {
		t.Fatal("one device filled discovery cache")
	}
	for i := 0; i < 100; i++ {
		s.recordSighting(fmt.Sprintf("device-%d", i), fmt.Sprintf("192.168.2.%d:17891", i), false, now)
	}
	if len(s.discovered) != maxDiscoverySightings {
		t.Fatalf("discovery cache size: %d", len(s.discovered))
	}
	if newAddr, _ := s.recordSighting("new", "192.168.1.100:17891", false, now.Add(lanSightingTTL+time.Second)); !newAddr {
		t.Fatal("expired entries prevented discovery")
	}
}
