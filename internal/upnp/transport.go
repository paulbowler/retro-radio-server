// SPDX-License-Identifier: GPL-3.0-only
package upnp

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// A separate opt-in LAN policy: only the configured server's pinned addresses.
// It never changes the public-address-only radio/podcast delivery transport.
type scope struct {
	host string
	ips  []netip.Addr
}

func validURL(raw string) (*url.URL, error) {
	u, e := url.Parse(raw)
	if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Fragment != "" || len(raw) > 4096 {
		return nil, errors.New("invalid music server URL")
	}
	if p := u.Port(); p != "" {
		n, e := strconv.Atoi(p)
		if e != nil || (n != 80 && n != 443 && (n < 1024 || n > 65535)) {
			return nil, errors.New("invalid music server port")
		}
	}
	return u, nil
}
func newScope(ctx context.Context, u *url.URL, allowLoopback bool) (*scope, error) {
	ips, e := net.DefaultResolver.LookupNetIP(ctx, "ip", u.Hostname())
	if e != nil {
		return nil, e
	}
	if len(ips) == 0 {
		return nil, errors.New("music server has no address")
	}
	for _, ip := range ips {
		ip = ip.Unmap()
		if !(ip.IsGlobalUnicast() || (allowLoopback && ip.IsLoopback())) || (ip.IsLoopback() && !allowLoopback) || ip.IsLinkLocalUnicast() {
			return nil, errors.New("music server address rejected")
		}
	}
	return &scope{host: strings.ToLower(u.Hostname()), ips: ips}, nil
}
func (s *scope) check(raw string) error {
	u, e := validURL(raw)
	if e != nil {
		return e
	}
	if strings.EqualFold(u.Hostname(), s.host) {
		return nil
	}
	if ip, e := netip.ParseAddr(u.Hostname()); e == nil {
		for _, allowed := range s.ips {
			if ip.Unmap() == allowed.Unmap() {
				return nil
			}
		}
	}
	return errors.New("resource is outside configured music server")
}

type idleConn struct{ net.Conn }

func (c idleConn) Read(p []byte) (int, error) {
	if e := c.SetReadDeadline(time.Now().Add(20 * time.Second)); e != nil {
		return 0, e
	}
	return c.Conn.Read(p)
}
func (s *scope) client() *http.Client {
	tr := &http.Transport{Proxy: nil, DisableCompression: true, ResponseHeaderTimeout: 5 * time.Second, TLSHandshakeTimeout: 5 * time.Second, IdleConnTimeout: 30 * time.Second, MaxIdleConns: 8, MaxConnsPerHost: 16, MaxResponseHeaderBytes: 32 << 10}
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if e := s.check("http://" + addr); e != nil {
			return nil, e
		}
		_, port, e := net.SplitHostPort(addr)
		if e != nil {
			return nil, e
		}
		var last error
		for _, ip := range s.ips {
			c, e := (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if e == nil {
				return idleConn{c}, nil
			}
			last = e
		}
		return nil, last
	}
	return &http.Client{Transport: tr, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 4 {
			return errors.New("too many music server redirects")
		}
		return s.check(r.URL.String())
	}}
}
