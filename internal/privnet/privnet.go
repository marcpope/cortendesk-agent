// Package privnet decides where plain http is allowed: only to an address
// on a private network. The console keeps the same list.
package privnet

import (
	"fmt"
	"net"
	"syscall"
)

// privateNets: loopback, RFC 1918, carrier-grade NAT (Tailscale uses it)
// and IPv6 unique local.
var privateNets = func() []*net.IPNet {
	var nets []*net.IPNet
	for _, c := range []string{"127.0.0.0/8", "::1/128", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10", "fc00::/7"} {
		_, n, _ := net.ParseCIDR(c)
		nets = append(nets, n)
	}
	return nets
}()

// IsPrivate reports whether ip is on a network plain http is allowed to.
func IsPrivate(ip net.IP) bool {
	for _, n := range privateNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// Control refuses a plain http connection to anything but a private
// address. It runs on the resolved address, just before connecting.
func Control(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); ip == nil || !IsPrivate(ip) {
		return fmt.Errorf("plain http is only allowed to a private network address, not %s; use https", host)
	}
	return nil
}
