// SPDX-License-Identifier: GPL-3.0-only
package upnp

import (
	"crypto/sha256"
	"fmt"
	"log"
	"retroradio.local/server/internal/content"
	"sort"
	"time"
)

type musicHistoryStore interface {
	MusicLastPlays() (map[string]time.Time, error)
	RecordMusicPlay(string, time.Time) error
}

// Called at startup. History is shared by all stations and listeners.
func (m *Manager) SetAgentHistory(store musicHistoryStore) error {
	plays, err := store.MusicLastPlays()
	if err != nil {
		return err
	}
	m.agentHistory, m.agentLastPlay = store, plays
	return nil
}

// The source resource is stable across menu views and sessions; playback tokens
// are random and must never be used as persistent song identities.
func agentTrackKey(item content.Item) string {
	identity := item.ID
	if len(item.Resources) > 0 && item.Resources[0].URL != "" {
		identity = item.Resources[0].URL
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(identity)))
}

func (m *Manager) agentRotation(tracks []content.Item) []content.Item {
	m.mu.RLock()
	defer m.mu.RUnlock()
	now := m.now()
	return lessRecentAgentTracks(tracks, m.agentLastPlay, now)
}

func lessRecentAgentTracks(tracks []content.Item, plays map[string]time.Time, now time.Time) []content.Item {
	fresh := make([]content.Item, 0, len(tracks))
	for _, track := range tracks {
		if at := plays[agentTrackKey(track)]; at.IsZero() || !at.After(now.Add(-24*time.Hour)) {
			fresh = append(fresh, track)
		}
	}
	if len(fresh) > 0 || len(tracks) == 0 {
		return fresh
	}
	// Exhausted collections relax the window, without becoming oldest-first.
	ranked := append([]content.Item(nil), tracks...)
	sort.SliceStable(ranked, func(i, j int) bool { return plays[agentTrackKey(ranked[i])].Before(plays[agentTrackKey(ranked[j])]) })
	n := (len(ranked) + 1) / 2
	cutoff := plays[agentTrackKey(ranked[n-1])]
	for n < len(ranked) && plays[agentTrackKey(ranked[n])].Equal(cutoff) {
		n++
	}
	return ranked[:n]
}

func (m *Manager) recordAgentPlay(item content.Item) {
	key, at := agentTrackKey(item), m.now()
	m.mu.Lock()
	if m.agentLastPlay == nil {
		m.agentLastPlay = make(map[string]time.Time)
	}
	if at.After(m.agentLastPlay[key]) {
		m.agentLastPlay[key] = at
	}
	m.mu.Unlock()
	if m.agentHistory != nil {
		if err := m.agentHistory.RecordMusicPlay(key, at); err != nil {
			log.Printf("Agent FM: save listening history: %v", err)
		}
	}
}
