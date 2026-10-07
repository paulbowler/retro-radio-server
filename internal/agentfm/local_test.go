// SPDX-License-Identifier: GPL-3.0-only
package agentfm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type localTransport func(*http.Request) (*http.Response, error)

func (f localTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func localHTTP(body string) *http.Response {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}
func localResponse(items []LocalItem, urls []string) string {
	text, _ := json.Marshal(map[string]any{"items": items})
	sources := []map[string]string{}
	for _, url := range urls {
		sources = append(sources, map[string]string{"url": url})
	}
	raw, _ := json.Marshal(map[string]any{"status": "completed", "output": []any{map[string]any{"type": "web_search_call", "action": map[string]any{"sources": sources}}, map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": string(text)}}}}})
	return string(raw)
}
func TestLocalLocationRequiresOptInAndManualOverrideWins(t *testing.T) {
	var calls atomic.Int32
	c := New(Config{Key: "test", LocalSettings: func() LocalSettings { return LocalSettings{} }})
	c.http.Transport = localTransport(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return localHTTP(`{"success":true,"city":"Winchester","region":"England","country":"United Kingdom","timezone":{"id":"Europe/London"}}`), nil
	})
	if c.local.offer() != nil || calls.Load() != 0 {
		t.Fatal("default settings triggered research")
	}
	if _, _, err := c.local.location(context.Background(), LocalSettings{}); err == nil || calls.Load() != 0 {
		t.Fatal("location without consent")
	}
	place, _, err := c.local.location(context.Background(), LocalSettings{Location: "Winchester, UK", Automatic: true})
	if err != nil || place != "Winchester, UK" || calls.Load() != 0 {
		t.Fatal("manual location did not override IP lookup", place, err)
	}
	place, zone, err := c.local.location(context.Background(), LocalSettings{Automatic: true})
	if err != nil || !strings.Contains(place, "Winchester") || zone != "Europe/London" || calls.Load() != 1 {
		t.Fatal(place, zone, err)
	}
}
func TestLocalResearchFiltersFreshnessSourcesAndInterest(t *testing.T) {
	now := time.Date(2026, 10, 7, 13, 0, 0, 0, time.UTC)
	news := LocalItem{Kind: "news", Summary: "A useful local update.", Source: "Council", URL: "https://council.test/update", Date: "2026-10-07", Interesting: true}
	event := LocalItem{Kind: "event", Summary: "A concert on Saturday.", Source: "Venue", URL: "https://venue.test/event", Date: "2026-10-10", Interesting: true}
	weather := LocalItem{Kind: "weather", Summary: "Strong winds expected this afternoon.", Source: "Forecast", URL: "https://weather.test/forecast", Date: "2026-10-07", Interesting: true}
	old := news
	old.Date = "2025-10-07"
	staleWeather := weather
	staleWeather.Date = "2026-10-06"
	pastEvent := event
	pastEvent.Date = "2026-10-06"
	distantEvent := event
	distantEvent.Date = "2026-11-10"
	invented := news
	invented.URL = "https://unknown.test/story"
	routine := weather
	routine.Interesting = false
	data := localResponse([]LocalItem{old, staleWeather, pastEvent, distantEvent, invented, routine, news, event, weather}, []string{news.URL, event.URL, weather.URL})
	items, err := parseLocalResearch([]byte(data), now)
	if err != nil || len(items) != 3 || items[0].Kind != "news" || items[1].Kind != "event" || items[2].Kind != "weather" {
		t.Fatal(items, err)
	}
	if !items[2].expires.Equal(now.Add(time.Hour)) {
		t.Fatal("weather cache too old")
	}
	if _, err := parseLocalResearch([]byte(`{"status":"incomplete"}`), now); err == nil {
		t.Fatal("incomplete research accepted")
	}
}
func TestLocalCacheIsNonBlockingSpacedAndDeduplicated(t *testing.T) {
	cfg := LocalSettings{Enabled: true, Location: "Winchester, UK", Country: "GB"}
	c := New(Config{Key: "test", LocalSettings: func() LocalSettings { return cfg }})
	now := time.Date(2026, 10, 7, 13, 0, 0, 0, time.UTC)
	c.local.now = func() time.Time { return now }
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	item := LocalItem{Kind: "news", Summary: "Council update.", Source: "Council", URL: "https://council.test/update", Date: "2026-10-07", Interesting: true}
	c.http.Transport = localTransport(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		var payload map[string]any
		json.NewDecoder(r.Body).Decode(&payload)
		if payload["model"] != "gpt-4.1-mini" || payload["tool_choice"] != "required" || !strings.Contains(payload["input"].(string), "Winchester, UK") {
			t.Error(payload)
		}
		close(entered)
		<-release
		return localHTTP(localResponse([]LocalItem{item}, []string{item.URL})), nil
	})
	if c.local.offer() != nil {
		t.Fatal("blocked for fresh research")
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("research not started")
	}
	if c.local.offer() != nil || calls.Load() != 1 {
		t.Fatal("duplicated pending research")
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for len(c.LocalItems()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	first := c.local.offer()
	if first == nil || first.URL != item.URL {
		t.Fatal("did not offer cached update")
	}
	if c.local.offer() != nil {
		t.Fatal("announced on consecutive songs")
	}
	// Research has completed; advance the clock within its cache lifetime.
	now = now.Add(21 * time.Minute)
	if c.local.offer() != nil {
		t.Fatal("repeated the same story")
	}
	if calls.Load() != 1 {
		t.Fatal("repeated research before refresh interval")
	}
}
func TestLocalResearchOptOutCancelsPendingRequest(t *testing.T) {
	var enabled atomic.Bool
	enabled.Store(true)
	c := New(Config{Key: "test", LocalSettings: func() LocalSettings { return LocalSettings{Enabled: enabled.Load(), Location: "Winchester, UK"} }})
	entered, stopped := make(chan struct{}), make(chan struct{})
	c.http.Transport = localTransport(func(r *http.Request) (*http.Response, error) {
		close(entered)
		<-r.Context().Done()
		close(stopped)
		return nil, r.Context().Err()
	})
	c.local.offer()
	<-entered
	enabled.Store(false)
	c.LocalSettingsChanged()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("opt-out did not cancel request")
	}
	if c.local.offer() != nil || len(c.LocalItems()) != 0 {
		t.Fatal("opt-out retained updates")
	}
}
func TestDJReceivesBackReferenceAndOnlyOccasionalLocalUpdate(t *testing.T) {
	cfg := LocalSettings{Enabled: true, Location: "Winchester, UK"}
	c := New(Config{Key: "test", LocalSettings: func() LocalSettings { return cfg }})
	now := time.Now()
	c.local.key = localKey(cfg)
	c.local.refreshAt = now.Add(time.Hour)
	c.local.items = []LocalItem{{Kind: "event", Summary: "A concert in Winchester on Saturday.", Source: "Venue", URL: "https://venue.test/event", Date: now.Format("2006-01-02"), Interesting: true, expires: now.Add(time.Hour)}}
	count := 0
	c.http.Transport = localTransport(func(r *http.Request) (*http.Response, error) {
		var p map[string]any
		json.NewDecoder(r.Body).Decode(&p)
		if strings.HasSuffix(r.URL.Path, "/audio/speech") {
			return localHTTP("MP3"), nil
		}
		count++
		var input map[string]json.RawMessage
		json.Unmarshal([]byte(p["input"].(string)), &input)
		if !strings.Contains(string(input["just_played"]), "Previous Performer") {
			t.Error("missing previous performer")
		}
		_, hasLocal := input["local_update"]
		if hasLocal != (count == 1) {
			t.Error("local update not occasional", count)
		}
		text := "That’s Previous Performer with Previous. Next Performer is up next."
		if hasLocal {
			text += " " + strings.Repeat("More local detail. ", 25)
		}
		choice, _ := json.Marshal(map[string]any{"index": 0, "chat": text})
		result, _ := json.Marshal(map[string]any{"status": "completed", "output": []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": string(choice)}}}}})
		return localHTTP(string(result)), nil
	})
	for i := 0; i < 2; i++ {
		if _, err := c.Prepare(context.Background(), Track{Title: "Previous", Artist: "Previous Performer"}, []Track{{Title: "Next", Artist: "Next Performer"}}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLocalCooldownStartsAtPlaybackAfterLongTrack(t *testing.T) {
	cfg := LocalSettings{Enabled: true, Location: "Winchester, UK"}
	c := New(Config{Key: "test", LocalSettings: func() LocalSettings { return cfg }})
	now := time.Now()
	c.local.now = func() time.Time { return now }
	c.local.key = localKey(cfg)
	c.local.refreshAt = now.Add(2 * time.Hour)
	c.local.items = []LocalItem{{Kind: "news", URL: "https://council.test/one", Date: "2026-10-07", expires: now.Add(3 * time.Hour)}, {Kind: "news", URL: "https://council.test/two", Date: "2026-10-07", expires: now.Add(3 * time.Hour)}}
	if c.local.offer() == nil {
		t.Fatal("first offer missing")
	}
	now = now.Add(time.Hour)
	c.local.broadcastStarted()
	if c.local.offer() != nil {
		t.Fatal("long track bypassed broadcast cooldown")
	}
	now = now.Add(21 * time.Minute)
	if next := c.local.offer(); next == nil || next.URL != "https://council.test/two" {
		t.Fatal("fresh update missing after cooldown", next)
	}
}
