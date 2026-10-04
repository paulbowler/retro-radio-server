// SPDX-License-Identifier: GPL-3.0-only
package podcast

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"retroradio.local/server/internal/store"
	"strings"
	"testing"
	"time"
)

const rss = `<?xml version="1.0"?><rss xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd" version="2.0"><channel><title>Test &amp; Talk</title><description>&lt;p&gt;A &lt;b&gt;good&lt;/b&gt; show.&lt;/p&gt;</description><itunes:author>Presenter</itunes:author><item><title>First</title><guid>one</guid><pubDate>Sun, 4 Oct 2026 12:00:00 +0000</pubDate><itunes:duration>30:45</itunes:duration><description>&lt;script&gt;bad&lt;/script&gt; Summary</description><enclosure url="audio/one.mp3" type="audio/mpeg"/></item><item><title>Private</title><enclosure url="http://127.0.0.1/secret.mp3" type="audio/mpeg"/></item><item><title>Video</title><enclosure url="https://example.org/movie.mp4" type="video/mp4"/></item><item><title>Duplicate</title><guid>one</guid><enclosure url="https://example.org/second.mp3" type="audio/mpeg"/></item></channel></rss>`

func TestParseRSSAndAtom(t *testing.T) {
	p, episodes, e := Parse([]byte(rss), "https://feeds.example.org/show.xml")
	if e != nil || p.Title != "Test & Talk" || p.Author != "Presenter" || p.Description != "A good show." || len(episodes) != 1 {
		t.Fatal(p, episodes, e)
	}
	ep := episodes[0]
	if ep.URL != "https://feeds.example.org/audio/one.mp3" || ep.Codec != "MP3" || ep.Duration != "30:45" || ep.Published.IsZero() || strings.Contains(ep.Description, "<") {
		t.Fatal(ep)
	}
	atom := `<feed xmlns="http://www.w3.org/2005/Atom"><title>Atom show</title><author><name>Author</name></author><entry><id>urn:ep:1</id><title>Episode</title><published>2026-10-04T12:00:00Z</published><link rel="enclosure" href="/episode.aac" type="audio/aac"/></entry></feed>`
	p, episodes, e = Parse([]byte(atom), "https://example.org/feed")
	if e != nil || p.Author != "Author" || len(episodes) != 1 || episodes[0].GUID != "urn:ep:1" || episodes[0].Codec != "AAC" {
		t.Fatal(p, episodes, e)
	}
	for _, bad := range []string{`<html><title>Website</title></html>`, `<rss><channel><title>Empty</title></channel></rss>`, `<rss>`, `<!DOCTYPE rss [<!ENTITY secret SYSTEM "file:///etc/passwd">]><rss><channel><title>&secret;</title></channel></rss>`} {
		if _, _, e = Parse([]byte(bad), "https://example.org/feed"); e == nil {
			t.Fatal("bad feed accepted", bad)
		}
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestRefreshKeepsIDsAndOfflineEpisodes(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "podcasts.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.DB.Close()
	s := New(db)
	body := rss
	calls := 0
	offline := false
	s.Client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if offline {
			return nil, errors.New("offline")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	p, e := s.Subscribe(context.Background(), "https://feeds.example.org/show.xml")
	if e != nil {
		t.Fatal(e)
	}
	eps, _ := db.Episodes(p.ID)
	id := eps[0].ID
	body = strings.ReplaceAll(rss, "First", "Revised")
	_, e = s.Subscribe(context.Background(), p.Feed)
	if e != nil {
		t.Fatal(e)
	}
	all, _ := db.Podcasts()
	eps, _ = db.Episodes(p.ID)
	if len(all) != 1 || eps[0].ID != id || eps[0].Title != "Revised" {
		t.Fatal(all, eps)
	}
	if _, e = db.DB.Exec(`UPDATE podcasts SET updated=?`, time.Now().Add(-2*time.Hour).UTC().Format(time.RFC3339)); e != nil {
		t.Fatal(e)
	}
	offline = true
	s.Refresh(context.Background())
	eps, _ = db.Episodes(p.ID)
	if len(eps) != 1 || eps[0].ID != id {
		t.Fatal("offline refresh lost episodes")
	}
	before := calls
	if _, e = s.Subscribe(context.Background(), "http://127.0.0.1/feed"); e == nil || calls != before {
		t.Fatal("unsafe feed requested")
	}
	offline = false
	body = strings.Repeat("x", maxFeed+1)
	if _, e = s.Subscribe(context.Background(), p.Feed); e == nil {
		t.Fatal("oversized feed accepted")
	}
	eps, _ = db.Episodes(p.ID)
	if eps[0].ID != id {
		t.Fatal("failed refresh changed saved episodes")
	}
}
func TestSearchCachesAndRejectsUnsafeFeeds(t *testing.T) {
	s := New(nil)
	calls := 0
	s.Client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Query().Get("media") != "podcast" || r.URL.Query().Get("limit") != "24" || r.URL.Query().Get("country") != "GB" {
			t.Fatal(r.URL)
		}
		return &http.Response{StatusCode: 200, Request: r, Body: io.NopCloser(strings.NewReader(`{"results":[{"collectionName":"Show","artistName":"Author","feedUrl":"https://example.org/feed"},{"collectionName":"Bad","feedUrl":"http://127.0.0.1/feed"}]}`))}, nil
	})}
	for i := 0; i < 2; i++ {
		results, e := s.Search(context.Background(), "show", "GB")
		if e != nil || len(results) != 1 || results[0].Title != "Show" {
			t.Fatal(results, e)
		}
		results[0].Title = "Changed"
	}
	if calls != 1 {
		t.Fatal("cache not used", calls)
	}
}
