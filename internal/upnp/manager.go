// SPDX-License-Identifier: GPL-3.0-only
package upnp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/netip"
	"retroradio.local/server/internal/content"
	"retroradio.local/server/internal/model"
	"sort"
	"strings"
	"sync"
	"time"
)

type musicServer struct {
	key      string
	provider *Provider
	seen     time.Time
	manual   bool
}

// Manager is the My Music root. Discovery runs independently of radio requests;
// each discovered server retains its own pinned transport and opaque token registry.
type Manager struct {
	DisableTranscode bool
	mu               sync.RWMutex
	servers          map[string]musicServer
	refresh          chan struct{}
	discover         func(context.Context) ([]DiscoveredServer, error)
	newProvider      func(string) (*Provider, error)
	now              func() time.Time
}

var _ content.Provider = (*Manager)(nil)

func NewManager(manual string) (*Manager, error) {
	m := &Manager{servers: map[string]musicServer{}, refresh: make(chan struct{}, 1), discover: Discover, newProvider: New, now: time.Now}
	if manual != "" {
		p, e := New(manual)
		if e != nil {
			return m, e
		}
		key := serverKey(manual)
		m.servers[key] = musicServer{key: key, provider: p, seen: m.now(), manual: true}
	}
	return m, nil
}

// SetTranscoding is called during setup, before any background or HTTP work.
func (m *Manager) SetTranscoding(enabled bool) {
	m.DisableTranscode = !enabled
	for _, s := range m.snapshot() {
		s.provider.DisableTranscode = !enabled
	}
}
func serverKey(identity string) string {
	h := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(h[:8])
}
func (m *Manager) snapshot() []musicServer {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]musicServer, 0, len(m.servers))
	for _, s := range m.servers {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].provider.Name(), out[j].provider.Name()
		if a == b {
			return out[i].key < out[j].key
		}
		return a < b
	})
	return out
}

// Rediscover requests background refresh; browsing never waits for multicast.
func (m *Manager) Rediscover() {
	select {
	case m.refresh <- struct{}{}:
	default:
	}
}
func (m *Manager) Run(ctx context.Context) {
	m.Refresh(ctx)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-m.refresh:
		}
		m.Refresh(ctx)
	}
}
func (m *Manager) Refresh(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, 12*time.Second)
	defer cancel()
	candidates, e := m.discover(ctx)
	if e != nil && len(candidates) == 0 {
		return
	} // A failed discovery attempt must not erase working music.
	current := m.snapshot()
	known := map[string]musicServer{}
	locations := map[string]string{}
	for _, s := range current {
		known[s.key] = s
		locations[s.provider.description.String()] = s.key
	}
	additions := map[string]musicServer{}
	// At most 16 descriptions, four concurrently, with the same total refresh deadline.
	var wg sync.WaitGroup
	var mu sync.Mutex
	slots := make(chan struct{}, 4)
schedule:
	for i, c := range candidates {
		if i >= 16 {
			break
		}
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			break schedule
		}
		wg.Add(1)
		go func(c DiscoveredServer) {
			defer wg.Done()
			defer func() { <-slots }()
			source, e := netip.ParseAddr(c.Address)
			if e != nil {
				return
			}
			identity := strings.SplitN(c.USN, "::", 2)[0]
			if identity == "" {
				identity = c.Location
			}
			key := serverKey(identity)
			if existing, ok := locations[c.Location]; ok {
				key = existing
			}
			s, exists := known[key]
			if exists && s.manual {
				mu.Lock()
				additions[key] = s
				mu.Unlock()
				return
			}
			if !exists || s.provider.description.String() != c.Location || s.provider.sourceIP != source.Unmap() {
				p, e := m.newProvider(c.Location)
				if e != nil {
					return
				}
				p.sourceIP = source.Unmap()
				p.DisableTranscode = m.DisableTranscode
				probe, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()
				if e = p.ensure(probe); e != nil {
					return
				} // Only valid, source-bound ContentDirectory devices become browseable.
				s = musicServer{key: key, provider: p}
			}
			s.seen = m.now()
			mu.Lock()
			additions[key] = s
			mu.Unlock()
		}(c)
	}
	wg.Wait()
	if parent.Err() != nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, s := range additions {
		if _, exists := m.servers[key]; exists || len(m.servers) < 16 {
			m.servers[key] = s
		}
	}
	now := m.now()
	for key, s := range m.servers {
		if !s.manual && now.Sub(s.seen) > 5*time.Minute {
			delete(m.servers, key)
		}
	}
}
func (m *Manager) Browse(ctx context.Context, object string, offset, count int) (content.Page, error) {
	if offset < 0 || count < 1 || count > MaxPage {
		return content.Page{}, errors.New("invalid music page")
	}
	if object == "0" {
		servers := m.snapshot()
		page := content.Page{Title: "My Music", ParentID: "-1", Total: len(servers)}
		for i, s := range servers {
			if i >= offset && i < offset+count {
				page.Items = append(page.Items, content.Item{ID: s.key + ":0", ParentID: "0", Title: s.provider.Name(), Source: "upnp", Kind: content.Folder})
			}
		}
		page.Returned = len(page.Items)
		return page, nil
	}
	key, id, ok := strings.Cut(object, ":")
	if !ok {
		return content.Page{}, errors.New("invalid music folder")
	}
	m.mu.RLock()
	s, exists := m.servers[key]
	m.mu.RUnlock()
	if !exists {
		return content.Page{}, errors.New("music server unavailable")
	}
	page, e := s.provider.Browse(ctx, id, offset, count)
	if e != nil {
		return page, e
	}
	if page.ParentID == "-1" || page.ParentID == "" {
		page.ParentID = "0"
	} else {
		page.ParentID = key + ":" + page.ParentID
	}
	for i := range page.Items {
		page.Items[i].ID = key + ":" + page.Items[i].ID
		page.Items[i].ParentID = object
	}
	return page, nil
}
func (m *Manager) providerFor(id string) (musicServer, bool) {
	for _, s := range m.snapshot() {
		s.provider.mu.Lock()
		_, ok := s.provider.tokens[id]
		s.provider.mu.Unlock()
		if ok {
			return s, true
		}
	}
	return musicServer{}, false
}
func (m *Manager) Resolve(ctx context.Context, id string, c model.Capabilities) (content.Playback, error) {
	s, ok := m.providerFor(id)
	if !ok {
		return content.Playback{}, ErrUnknownID
	}
	play, e := s.provider.Resolve(ctx, id, c)
	if e == nil {
		play.Item.ID = s.key + ":" + play.Item.ID
		play.Item.ParentID = s.key + ":" + play.Item.ParentID
	}
	return play, e
}
func (m *Manager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/stream/upnp/")
	s, ok := m.providerFor(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.provider.ServeHTTP(w, r)
}
func (m *Manager) ServeArtwork(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/artwork/upnp/"), ".jpg")
	s, ok := m.providerFor(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.provider.ServeArtwork(w, r)
}
