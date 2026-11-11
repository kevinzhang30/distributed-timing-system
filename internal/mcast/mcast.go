// Package mcast wraps the UDP multicast sockets used for race traffic.
package mcast

import (
	"fmt"
	"net"
)

// DefaultGroup is in 239.0.0.0/8, the "administratively scoped" range:
// routers don't forward it off the local network.
const DefaultGroup = "239.0.0.42:9999"

// Dial returns a socket for sending to the group. Multicast loopback is on by
// default, so receivers on this same machine also get what we send.
func Dial(group string) (*net.UDPConn, error) {
	addr, err := net.ResolveUDPAddr("udp4", group)
	if err != nil {
		return nil, err
	}
	if !addr.IP.IsMulticast() {
		return nil, fmt.Errorf("mcast: %s is not a multicast address", addr.IP)
	}
	return net.DialUDP("udp4", nil, addr)
}

// Listen joins the group and returns a socket for receiving from it.
// iface may be "" to let the OS choose (usually the default-route interface).
// Several processes on one host can Listen on the same group concurrently;
// Go sets SO_REUSEADDR/SO_REUSEPORT on multicast listeners.
func Listen(group, iface string) (*net.UDPConn, error) {
	addr, err := net.ResolveUDPAddr("udp4", group)
	if err != nil {
		return nil, err
	}
	var ifi *net.Interface
	if iface != "" {
		if ifi, err = net.InterfaceByName(iface); err != nil {
			return nil, fmt.Errorf("mcast: interface %q: %w", iface, err)
		}
	}
	conn, err := net.ListenMulticastUDP("udp4", ifi, addr)
	if err != nil {
		return nil, err
	}
	// Bursts of splits at the finish shouldn't overflow the kernel buffer.
	_ = conn.SetReadBuffer(1 << 20)
	return conn, nil
}
