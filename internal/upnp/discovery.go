// SPDX-License-Identifier: GPL-3.0-only
package upnp

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"
)

// Discovery returns candidate description links only. Multicast announcements never
// authorize LAN fetching; an operator must configure the selected URL explicitly.
type DiscoveredServer struct{ Location, USN, Address string }

func Discover(ctx context.Context) ([]DiscoveredServer, error) {
	conn, e := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero})
	if e != nil {
		return nil, e
	}
	defer conn.Close()
	deadline := time.Now().Add(2 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if e = conn.SetDeadline(deadline); e != nil {
		return nil, e
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	target := &net.UDPAddr{IP: net.IPv4(239, 255, 255, 250), Port: 1900}
	msg := "M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nMAN: \"ssdp:discover\"\r\nMX: 1\r\nST: urn:schemas-upnp-org:service:ContentDirectory:1\r\n\r\n"
	if _, e = conn.WriteToUDP([]byte(msg), target); e != nil {
		return nil, e
	}
	out := []DiscoveredServer{}
	seen := map[string]bool{}
	buf := make([]byte, 8192)
	for packets := 0; packets < 128 && len(out) < 16; packets++ {
		n, source, e := conn.ReadFromUDP(buf)
		if e != nil {
			if ctx.Err() != nil {
				return out, ctx.Err()
			}
			var ne net.Error
			if errors.As(e, &ne) && ne.Timeout() {
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
	if !strings.HasPrefix(res.Header.Get("ST"), "urn:schemas-upnp-org:service:ContentDirectory:") {
		return DiscoveredServer{}, false
	}
	return DiscoveredServer{Location: u.String(), USN: res.Header.Get("USN"), Address: address}, true
}
