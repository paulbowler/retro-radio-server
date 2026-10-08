// SPDX-License-Identifier: GPL-3.0-only
package upnp

import (
	"context"
	"fmt"
	"retroradio.local/server/internal/content"
	"retroradio.local/server/internal/model"
	"testing"
	"time"
)

func TestAgentRotationWindowAndSmallLibraryFallback(t *testing.T) {
	now := time.Now()
	tracks := []content.Item{}
	plays := map[string]time.Time{}
	for i := 0; i < 6; i++ {
		track := content.Item{ID: fmt.Sprint(i), PlaybackID: fmt.Sprint(i)}
		tracks = append(tracks, track)
		plays[agentTrackKey(track)] = now.Add(-time.Duration(6-i) * time.Hour)
	}
	pool := lessRecentAgentTracks(tracks, plays, now)
	if len(pool) != 3 {
		t.Fatal("fallback should offer less recently played half", pool)
	}
	for _, track := range pool {
		if track.ID >= "3" {
			t.Fatal("recent half included", track)
		}
	}
	delete(plays, agentTrackKey(tracks[4]))
	plays[agentTrackKey(tracks[5])] = now.Add(-25 * time.Hour)
	pool = lessRecentAgentTracks(tracks, plays, now)
	if len(pool) != 2 || pool[0].ID != "4" || pool[1].ID != "5" {
		t.Fatal("unplayed and outside-window tracks not preferred", pool)
	}
	q := &musicQueue{tracks: tracks, rotation: func(list []content.Item) []content.Item { return lessRecentAgentTracks(list, plays, now) }}
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		candidates := agentCandidates(q, tracks[0])
		if len(candidates) != 2 {
			t.Fatal(candidates)
		}
		seen[candidates[0].ID] = true
	}
	if len(seen) != 2 {
		t.Fatal("candidate ordering became oldest-first", seen)
	}
	if len(lessRecentAgentTracks(nil, plays, now)) != 0 {
		t.Fatal("empty library")
	}
}

func TestAgentTrackIdentitySurvivesNewTokensAndMenuViews(t *testing.T) {
	a := content.Item{ID: "server:album:42", PlaybackID: "old", Resources: []content.Resource{{URL: "http://nas:9790/music/song.flac"}}}
	b := a
	b.ID = "server:genre:42"
	b.PlaybackID = "new"
	if agentTrackKey(a) != agentTrackKey(b) {
		t.Fatal("menu view or random token changed song identity")
	}
	b.Resources = []content.Resource{{URL: "http://nas:9790/music/different.flac"}}
	if agentTrackKey(a) == agentTrackKey(b) {
		t.Fatal("different recordings conflated")
	}
}

func TestNewAgentSessionsAvoidPreviousSongWithoutRecordingSelections(t *testing.T) {
	m, folder := queueFixture(t, 4, "tracks")
	tracks, err := m.TrackList(context.Background(), folder)
	if err != nil {
		t.Fatal(err)
	}
	m.agent = agentServiceFunc(nil)
	m.agentFFmpeg = fakeAgentFFmpeg(t)
	for _, track := range tracks[:3] {
		m.recordAgentPlay(track)
	}
	for i := 0; i < 5; i++ {
		_, id, err := m.startAgentFM(context.Background(), folder, "Test FM", model.LegacyXML)
		if err != nil {
			t.Fatal(err)
		}
		if m.queues[id].tracks[0].PlaybackID != tracks[3].PlaybackID {
			t.Fatal("recent song opened a new session")
		}
	}
	if len(m.agentLastPlay) != 3 {
		t.Fatal("selection counted as listening")
	}
}
