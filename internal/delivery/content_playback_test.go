// SPDX-License-Identifier: GPL-3.0-only
package delivery

import (
	"io"
	"net/http"
	"net/http/httptest"
	"retroradio.local/server/internal/content"
	"retroradio.local/server/internal/model"
	"strings"
	"testing"
	"time"
)

func TestMusicOwnershipCompletionSupersedingAndExpiry(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	relay := testRelay(t, server)
	device, _ := relay.Store.Seen("music-radio", "pure", "", "192.168.1.20")
	r := httptest.NewRequest("GET", "/stream/upnp/token?radio="+device.ID, nil)
	play := content.Playback{Item: content.Item{Title: "Chan Chan", Artist: "Buena Vista Social Club", Album: "Buena Vista Social Club", Duration: "0:04:17", ArtURL: "http://music/art", PlaybackID: "upnp_test"}, Resource: content.Resource{Codec: "FLAC"}, Transcode: true}
	finish := relay.TrackMusic(r, play)
	entries := relay.Active()
	if len(entries) != 1 || entries[0].Device != device.ID || entries[0].Kind != "Music" || entries[0].Codec != "MP3" || entries[0].Bitrate != 128 || entries[0].Artwork != "/artwork/upnp/upnp_test.jpg" || entries[0].Until.IsZero() {
		t.Fatal(entries)
	}
	finish(true)
	if len(relay.Active()) != 1 {
		t.Fatal("buffered track disappeared while still playing")
	}
	next := play
	next.Item.Title = "Next track"
	next.Item.PlaybackID = "upnp_next"
	stop := relay.TrackMusic(r, next)
	if entries = relay.Active(); len(entries) != 1 || entries[0].Station != "Next track" {
		t.Fatal("previous buffered track retained", entries)
	}
	stop(false)
	if len(relay.Active()) != 0 {
		t.Fatal("failed playback retained")
	}
	finish = relay.TrackMusic(r, play)
	finish(true)
	relay.mu.Lock()
	for id, v := range relay.active {
		v.Until = time.Now().Add(-time.Second)
		relay.active[id] = v
	}
	relay.mu.Unlock()
	if len(relay.Active()) != 0 {
		t.Fatal("finished track never expired")
	}
	web := httptest.NewRequest("GET", "/stream/upnp/token?listener=web&radio="+device.ID, nil)
	finish = relay.TrackMusic(web, play)
	finish(true)
	if len(relay.Active()) != 0 {
		t.Fatal("web playback assigned to radio")
	}
}
func TestPodcastMetadataPersistsAfterBuffering(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	relay := testRelay(t, server)
	device, _ := relay.Store.Seen("podcast-radio", "pure", "", "192.168.1.21")
	show, e := relay.Store.SavePodcast(model.Podcast{Title: "The Show", Feed: "https://feeds.example.org/show"}, []model.Episode{{GUID: "one", Title: "The Episode", URL: "https://audio.example.org/episode.mp3", Codec: "MP3", Duration: "20:00"}})
	if e != nil {
		t.Fatal(e)
	}
	episodes, _ := relay.Store.Episodes(show.ID)
	relay.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Request: r, Header: http.Header{"Content-Type": {"audio/mpeg"}, "Content-Length": {"5"}}, Body: io.NopCloser(strings.NewReader("audio"))}, nil
	})}
	r := httptest.NewRequest("GET", "/episode/"+episodes[0].ID+"?radio="+device.ID, nil)
	w := httptest.NewRecorder()
	relay.ServeEpisode(w, r)
	entries := relay.Active()
	if w.Code != 200 || len(entries) != 1 || entries[0].Kind != "Podcast" || entries[0].Station != "The Episode" || entries[0].Description != "The Show" || entries[0].Device != device.ID {
		t.Fatal(w.Code, entries)
	}
}
