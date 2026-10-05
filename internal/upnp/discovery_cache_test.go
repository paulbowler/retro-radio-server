// SPDX-License-Identifier: GPL-3.0-only
package upnp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDiscoveryHelperHandoffAndFreshness(t *testing.T) {
	m, c := automaticFixture(t)
	path := filepath.Join(t.TempDir(), "discovery.json")
	now := time.Now()
	if e := writeDiscoveryCache(path, []DiscoveredServer{c}, now); e != nil {
		t.Fatal(e)
	}
	m.now = func() time.Time { return now }
	m.discover = func(context.Context) ([]DiscoveredServer, error) {
		t.Fatal("fresh helper discovery fell back to container multicast")
		return nil, nil
	}
	m.UseDiscoveryFile(path)
	m.Refresh(context.Background())
	if len(m.snapshot()) != 1 {
		t.Fatal("host helper did not connect server automatically")
	}
	if _, e := readDiscoveryCache(path, now.Add(91*time.Second)); e == nil {
		t.Fatal("stale helper cache accepted")
	}
	if _, e := readDiscoveryCache(path, now.Add(-6*time.Second)); e == nil {
		t.Fatal("future cache accepted")
	}
}
func TestDiscoveryCacheInvalidAndMissingFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "discovery.json")
	for _, data := range []string{"bad", `{"Version":999}`, string(make([]byte, 65537))} {
		if e := os.WriteFile(path, []byte(data), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e := readDiscoveryCache(path, time.Now()); e == nil {
			t.Fatal("invalid cache accepted")
		}
	}
	m, c := automaticFixture(t)
	calls := 0
	m.discover = func(context.Context) ([]DiscoveredServer, error) { calls++; return []DiscoveredServer{c}, nil }
	m.UseDiscoveryFile(path)
	m.Refresh(context.Background())
	if calls != 1 || len(m.snapshot()) != 1 {
		t.Fatal("invalid cache broke native discovery")
	}
}
