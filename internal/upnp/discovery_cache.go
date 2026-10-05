// SPDX-License-Identifier: GPL-3.0-only
package upnp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"
)

// This cache holds only short-lived SSDP announcements, never library metadata.
// The helper owns the volume; the application mounts it read-only.
type discoveryCache struct {
	Version int
	Updated time.Time
	Servers []DiscoveredServer
}

func writeDiscoveryCache(path string, servers []DiscoveredServer, now time.Time) error {
	if len(servers) > 16 {
		servers = servers[:16]
	}
	data, e := json.Marshal(discoveryCache{Version: 1, Updated: now, Servers: servers})
	if e != nil {
		return e
	}
	dir := filepath.Dir(path)
	if e = os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(dir, ".discovery-")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, e = f.Write(data); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return os.Rename(tmp, path)
}
func readDiscoveryCache(path string, now time.Time) ([]DiscoveredServer, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	data, e := io.ReadAll(io.LimitReader(f, 65537))
	if e != nil {
		return nil, e
	}
	if len(data) > 65536 {
		return nil, errors.New("discovery cache exceeds limit")
	}
	var cache discoveryCache
	if e = json.Unmarshal(data, &cache); e != nil {
		return nil, e
	}
	if cache.Version != 1 || len(cache.Servers) > 16 || now.Sub(cache.Updated) > 90*time.Second || cache.Updated.After(now.Add(5*time.Second)) {
		return nil, errors.New("discovery cache is stale or invalid")
	}
	for _, server := range cache.Servers {
		if _, e = validURL(server.Location); e != nil {
			return nil, e
		}
	}
	return cache.Servers, nil
}

// UseDiscoveryFile is a startup option. A missing/stale helper file falls back
// to direct LAN discovery, then checks for the helper's first response again.
func (m *Manager) UseDiscoveryFile(path string) {
	if path == "" {
		return
	}
	direct := m.discover
	m.discover = func(ctx context.Context) ([]DiscoveredServer, error) {
		if servers, e := readDiscoveryCache(path, m.now()); e == nil && len(servers) > 0 {
			return servers, nil
		}
		servers, e := direct(ctx)
		if helper, err := readDiscoveryCache(path, m.now()); err == nil && len(helper) > 0 {
			return helper, nil
		}
		return servers, e
	}
}
func RunDiscoveryWriter(ctx context.Context, path string) {
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		servers, e := Discover(ctx)
		if ctx.Err() != nil {
			return
		}
		if e != nil {
			log.Printf("Music discovery unavailable: %v", e)
		} else if e = writeDiscoveryCache(path, servers, time.Now()); e != nil {
			log.Printf("Music discovery handoff unavailable: %v", e)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
