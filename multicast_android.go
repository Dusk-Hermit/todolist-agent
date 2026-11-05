package main

/*
#include <ifaddrs.h>
#include <net/if.h>
#include <netinet/in.h>
*/
import "C"

import (
	"fmt"
	"net"
	"net/netip"
	"syscall"
	"unsafe"
)

// Android restricts RTM_GETLINK for apps. Bionic enumerates addresses and
// obtains interface flags via ioctl when it cannot use that netlink request.
func discoveryInterfaces() ([]discoveryInterface, error) {
	var head *C.struct_ifaddrs
	if result, err := C.getifaddrs(&head); result != 0 {
		return nil, fmt.Errorf("Android network interfaces: %w", err)
	}
	defer C.freeifaddrs(head)
	var result []discoveryInterface
	for p := head; p != nil; p = p.ifa_next {
		if p.ifa_addr == nil || p.ifa_addr.sa_family != C.AF_INET ||
			p.ifa_flags&(C.IFF_UP|C.IFF_MULTICAST) != C.IFF_UP|C.IFF_MULTICAST ||
			p.ifa_flags&C.IFF_LOOPBACK != 0 {
			continue
		}
		index := int(C.if_nametoindex(p.ifa_name))
		if index == 0 {
			continue
		}
		raw := (*C.struct_sockaddr_in)(unsafe.Pointer(p.ifa_addr))
		ip := *(*[4]byte)(unsafe.Pointer(&raw.sin_addr))
		result = append(result, discoveryInterface{
			nic: net.Interface{Index: index, Name: C.GoString(p.ifa_name)}, ip: netip.AddrFrom4(ip),
		})
	}
	return result, nil
}

func setMulticastInterface(socket *net.UDPConn, ip [4]byte) error {
	raw, err := socket.SyscallConn()
	if err != nil {
		return err
	}
	var sockErr error
	if err := raw.Control(func(fd uintptr) {
		sockErr = syscall.SetsockoptInet4Addr(int(fd), syscall.IPPROTO_IP, syscall.IP_MULTICAST_IF, ip)
		if sockErr == nil {
			sockErr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_MULTICAST_LOOP, 1)
		}
	}); err != nil {
		return err
	}
	return sockErr
}
