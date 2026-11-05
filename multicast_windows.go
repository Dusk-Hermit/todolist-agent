package main

import (
	"net"
	"net/netip"
	"syscall"
)

func setMulticastInterface(socket *net.UDPConn, ip [4]byte) error {
	raw, err := socket.SyscallConn()
	if err != nil {
		return err
	}
	var sockErr error
	if err := raw.Control(func(fd uintptr) {
		sockErr = syscall.SetsockoptInet4Addr(syscall.Handle(fd), syscall.IPPROTO_IP, syscall.IP_MULTICAST_IF, ip)
		if sockErr == nil {
			sockErr = syscall.SetsockoptInt(syscall.Handle(fd), syscall.IPPROTO_IP, syscall.IP_MULTICAST_LOOP, 1)
		}
	}); err != nil {
		return err
	}
	return sockErr
}

func discoveryInterfaces() ([]discoveryInterface, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var result []discoveryInterface
	for _, nic := range interfaces {
		if nic.Flags&(net.FlagUp|net.FlagMulticast) != net.FlagUp|net.FlagMulticast || nic.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, _ := nic.Addrs()
		for _, addr := range addresses {
			if prefix, err := netip.ParsePrefix(addr.String()); err == nil {
				result = append(result, discoveryInterface{nic: nic, ip: prefix.Addr()})
			}
		}
	}
	return result, nil
}
