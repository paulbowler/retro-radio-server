// SPDX-License-Identifier: GPL-3.0-only
package upnp

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Discovery records the sender. Automatic description fetching is bound to that
// address, rather than trusting arbitrary URLs received in multicast replies.
type DiscoveredServer struct{ Location, USN, Address string }

var discoveryTargets = []string{"urn:schemas-upnp-org:device:MediaServer:1", "urn:schemas-upnp-org:service:ContentDirectory:1"}

func discoveryIPs() ([]net.IP, error) {
	interfaces, e := net.Interfaces()
	if e != nil {
		return nil, e
	}
	ips := []net.IP{}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagMulticast == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, e := iface.Addrs()
		if e != nil {
			continue
		}
		for _, addr := range addresses {
			ip, _, e := net.ParseCIDR(addr.String())
			if e == nil && ip.To4() != nil && ip.IsGlobalUnicast() && !ip.IsLoopback() {
				ips = append(ips, ip.To4())
				if len(ips) >= 8 {
					return ips, nil
				}
			}
		}
	}
	return ips, nil
}
func searchMessage(target string) string {
	return "M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nMAN: \"ssdp:discover\"\r\nMX: 1\r\nST: " + target + "\r\n\r\n"
}
func discoverOn(ctx context.Context, ip net.IP) ([]DiscoveredServer, error) {
	conn, e := net.ListenUDP("udp4", &net.UDPAddr{IP: ip})
	if e != nil {
		return nil, e
	}
	defer conn.Close()
	raw, e := conn.SyscallConn()
	if e != nil {
		return nil, e
	}
	var optionErr error
	if e = raw.Control(func(fd uintptr) {
		var address [4]byte
		copy(address[:], ip.To4())
		optionErr = syscall.SetsockoptInet4Addr(int(fd), syscall.IPPROTO_IP, syscall.IP_MULTICAST_IF, address)
	}); e != nil {
		return nil, e
	}
	if optionErr != nil {
		return nil, optionErr
	}
	if deadline, ok := ctx.Deadline(); ok {
		if e = conn.SetDeadline(deadline); e != nil {
			return nil, e
		}
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	target := &net.UDPAddr{IP: net.IPv4(239, 255, 255, 250), Port: 1900}
	for _, st := range discoveryTargets {
		if _, e = conn.WriteToUDP([]byte(searchMessage(st)), target); e != nil {
			return nil, e
		}
	}
	out := []DiscoveredServer{}
	seen := map[string]bool{}
	buf := make([]byte, 8192)
	for packets := 0; packets < 128 && len(out) < 16; packets++ {
		n, source, e := conn.ReadFromUDP(buf)
		if e != nil {
			var ne net.Error
			if errors.As(e, &ne) && ne.Timeout() {
				return out, nil
			}
			if ctx.Err() != nil {
				return out, nil
			}
			return out, e
		}
		candidate, ok := parseDiscovery(string(buf[:n]), source.IP.String())
		if ok && !seen[candidate.Location] {
			seen[candidate.Location] = true
			out = append(out, candidate)
		}
	}
	return out, nil
}
func Discover(parent context.Context) ([]DiscoveredServer, error) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	ips, e := discoveryIPs()
	if e != nil {
		return nil, e
	}
	if len(ips) == 0 {
		return nil, errors.New("no multicast-capable IPv4 interface")
	}
	type result struct {
		servers []DiscoveredServer
		err     error
	}
	results := make(chan result, len(ips))
	var wg sync.WaitGroup
	for _, ip := range ips {
		wg.Add(1)
		go func(ip net.IP) { defer wg.Done(); servers, e := discoverOn(ctx, ip); results <- result{servers, e} }(ip)
	}
	wg.Wait()
	close(results)
	out := []DiscoveredServer{}
	seen := map[string]bool{}
	var last error
	successful := false
	for res := range results {
		if res.err != nil {
			last = res.err
		} else {
			successful = true
		}
		for _, s := range res.servers {
			if !seen[s.Location] && len(out) < 16 {
				seen[s.Location] = true
				out = append(out, s)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Location < out[j].Location })
	if parent.Err() != nil {
		return out, parent.Err()
	}
	if !successful && len(out) == 0 {
		return nil, last
	}
	return out, nil
}
func parseDiscovery(raw, address string) (DiscoveredServer, bool) {
	res, e := http.ReadResponse(bufio.NewReader(strings.NewReader(raw)), nil)
	if e != nil {
		return DiscoveredServer{}, false
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return DiscoveredServer{}, false
	}
	u, e := validURL(res.Header.Get("Location"))
	if e != nil {
		return DiscoveredServer{}, false
	}
	st := res.Header.Get("ST")
	if !directoryType.MatchString(st) && !strings.HasPrefix(st, "urn:schemas-upnp-org:device:MediaServer:") {
		return DiscoveredServer{}, false
	}
	return DiscoveredServer{Location: u.String(), USN: res.Header.Get("USN"), Address: address}, true
}
