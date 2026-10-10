// SPDX-License-Identifier: GPL-3.0-only
package delivery

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"retroradio.local/server/internal/model"
	"strings"
	"time"
)

func lanIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsPrivate() && ip.IsGlobalUnicast() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && ip.Zone() == ""
}

// Selection does not grant network access. Custom stations always use the relay,
// where the operator's URL/origin allowlist is enforced before any LAN fetch.
func selectionURL(s model.Station) error {
	return validateURL(s.URL, s.Source == "custom")
}

// ConfigureLANStreams must be called before serving requests. DNS answers are
// checked and pinned for the lifetime of this relay; configuration fails closed.
func (p *Relay) ConfigureLANStreams(ctx context.Context, raw string) error {
	return p.configureLANStreams(ctx, raw, net.DefaultResolver.LookupNetIP)
}

func (p *Relay) configureLANStreams(ctx context.Context, raw string, lookup func(context.Context, string, string) ([]netip.Addr, error)) error {
	clients := map[string]*http.Client{}
	if strings.TrimSpace(raw) != "" {
		for _, entry := range strings.Split(raw, ",") {
			entry = strings.TrimSpace(entry)
			if err := validateURL(entry, true); err != nil {
				return err
			}
			if strings.Contains(entry, "*") {
				u, _ := url.Parse(entry)
				if u.Path != "/*" || !strings.HasSuffix(entry, "/*") || u.RawQuery != "" || u.ForceQuery || strings.Contains(u.Host, "*") {
					return errors.New("LAN wildcard must be an HTTP(S) origin followed by /*")
				}
			}
			if adaptiveKind(entry, "", false) != "" {
				return errors.New("LAN streams must be direct MP3/AAC audio URLs")
			}
			u, _ := url.Parse(entry)
			resolveCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			ips, err := lookup(resolveCtx, "ip", u.Hostname())
			cancel()
			if err != nil {
				return errors.New("cannot resolve configured LAN stream host")
			}
			if len(ips) == 0 {
				return errors.New("no LAN stream addresses")
			}
			for _, ip := range ips {
				if !lanIP(ip) {
					return fmt.Errorf("%w: configured LAN stream must resolve exclusively to private unicast addresses", ErrUnsafeTarget)
				}
			}
			clients[entry] = newLANStreamClient(entry, ips)
		}
	}
	p.lanStreams = clients
	return nil
}

// Restrict requests and redirects to the configured exact URL or origin.
// The transport dials only the original checked addresses, never DNS again.
func newLANStreamClient(raw string, addresses []netip.Addr) *http.Client {
	u, _ := url.Parse(raw)
	ips := append([]netip.Addr(nil), addresses...)
	client := NewClient()
	tr := client.Transport.(*http.Transport)
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		expectedPort := u.Port()
		if expectedPort == "" {
			if u.Scheme == "https" {
				expectedPort = "443"
			} else {
				expectedPort = "80"
			}
		}
		if !strings.EqualFold(host, u.Hostname()) || port != expectedPort {
			return nil, ErrUnsafeTarget
		}
		var last error
		for _, ip := range ips {
			conn, err := (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return idleConn{conn}, nil
			}
			last = err
		}
		return nil, last
	}
	client.Transport = scopedStreamTransport{raw: raw, transport: tr}
	client.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if len(via) >= 8 {
			return errors.New("too many redirects")
		}
		if !lanScopeMatches(raw, r.URL.String()) {
			return ErrUnsafeTarget
		}
		return nil
	}
	return client
}

type scopedStreamTransport struct {
	raw       string
	transport *http.Transport
}

func (t scopedStreamTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if !lanScopeMatches(t.raw, r.URL.String()) {
		return nil, ErrUnsafeTarget
	}
	return t.transport.RoundTrip(r)
}
func (t scopedStreamTransport) CloseIdleConnections() { t.transport.CloseIdleConnections() }

// Only an origin-wide /* rule is supported, never wildcard hostnames. Scheme,
// authority and effective port stay fixed; credentials/fragments remain forbidden.
func lanScopeMatches(scope, raw string) bool {
	if validateURL(raw, true) != nil {
		return false
	}
	if scope == raw {
		return true
	}
	allowed, err := url.Parse(scope)
	if err != nil || allowed.Path != "/*" || !strings.HasSuffix(scope, "/*") {
		return false
	}
	target, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return target.Scheme == allowed.Scheme && strings.EqualFold(target.Hostname(), allowed.Hostname()) && originPort(target) == originPort(allowed)
}

func originPort(u *url.URL) string {
	if u.Port() != "" {
		return u.Port()
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}

func (p *Relay) lanClient(s model.Station) *http.Client {
	if s.Source == "custom" {
		if client := p.lanStreams[s.URL]; client != nil {
			return client
		}
		for scope, client := range p.lanStreams {
			if lanScopeMatches(scope, s.URL) {
				return client
			}
		}
	}
	return nil
}
func (p *Relay) validateStation(s model.Station) error {
	if p.lanClient(s) != nil {
		if s.HLS || adaptiveKind(s.URL, "", false) != "" {
			return ErrUnsafeTarget
		}
		return validateURL(s.URL, true)
	}
	return ValidateURL(s.URL)
}

func (p *Relay) CheckStationTarget(ctx context.Context, s model.Station) error {
	if p.lanClient(s) != nil {
		return p.validateStation(s)
	}
	return CheckTarget(ctx, s.URL)
}
func (p *Relay) stationRequest(s model.Station, req *http.Request) (*http.Response, error) {
	if client := p.lanClient(s); client != nil {
		return client.Do(req)
	}
	return p.request(req)
}
