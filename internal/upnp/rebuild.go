// SPDX-License-Identifier: GPL-3.0-only
package upnp

import (
	"context"
	"fmt"
	"time"
)

// Rebuild discards every queue, token and pinned provider after callers have
// drained playback/discovery. Manual source URLs and service configuration stay.
func (m *Manager) Rebuild(ctx context.Context) []string {
	m.mu.Lock()
	previous := m.servers
	m.servers = map[string]musicServer{}
	m.queues = map[string]*musicQueue{}
	m.agentLastPlay = map[string]time.Time{}
	m.mu.Unlock()
	var warnings []string
	for key, old := range previous {
		if old.provider.client != nil {
			old.provider.client.CloseIdleConnections()
		}
		if !old.manual {
			continue
		}
		p, err := m.newProvider(old.provider.description.String())
		if err != nil {
			warnings = append(warnings, "Manual music source could not be recreated.")
			continue
		}
		p.DisableTranscode, p.PlaybackObserver = m.DisableTranscode, m.PlaybackObserver
		m.mu.Lock()
		m.servers[key] = musicServer{key: key, provider: p, manual: true, seen: m.now()}
		m.mu.Unlock()
	}
	m.Refresh(ctx)
	for _, s := range m.snapshot() {
		if _, err := s.provider.Browse(ctx, "0", 0, 1); err != nil {
			warnings = append(warnings, fmt.Sprintf("Music source %s is unavailable.", s.provider.Name()))
		}
	}
	if len(m.snapshot()) == 0 {
		warnings = append(warnings, "No music server was discovered; normal discovery will retry.")
	}
	return warnings
}
