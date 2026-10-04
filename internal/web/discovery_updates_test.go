// SPDX-License-Identifier: GPL-3.0-only
package web

import (
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"retroradio.local/server/internal/delivery"
	"retroradio.local/server/internal/model"
	"retroradio.local/server/internal/store"
)

func TestDiscoveryUpdatesAppendOnlyNewCards(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "updates.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	started := time.Now()
	token := fmt.Sprint(started.UnixNano())
	fast := candidateCard{Sequence: 1, Rank: 1, Discovery: true, Candidate: model.Candidate{UUID: "fast", Name: "Fast station", URL: "https://1.1.1.1/fast", Codec: "MP3"}}
	job := &discoveryJob{started: started, loading: true, country: "GB", items: []candidateCard{fast}}
	app := &App{Store: s, Relay: delivery.New(s), discoveriesJobs: map[string]*discoveryJob{"GB": job}}
	h := app.Handler()
	get := func(cursor string) string {
		t.Helper()
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/stations/discoveries?country=GB&job="+token+"&since="+cursor, nil))
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	first := get("0")
	if strings.Count(first, "<article") != 1 || !strings.Contains(first, `hx-swap-oob="beforeend:#discovery-grid-`+token+`"`) || strings.Contains(first, `id="directory-discoveries"`) || !strings.Contains(first, "since=1") {
		t.Fatal(first)
	}
	// A more popular but slower stream finishes later. It must not resend the fast card.
	job.items = []candidateCard{{Sequence: 2, Rank: 0, Discovery: true, Candidate: model.Candidate{UUID: "slow", Name: "Slow station", URL: "https://1.1.1.1/slow", Codec: "MP3"}}, fast}
	second := get("1")
	if strings.Count(second, "<article") != 1 || !strings.Contains(second, "Slow station") || strings.Contains(second, "Fast station") || !strings.Contains(second, "since=2") || strings.Contains(second, `hx-swap-oob="innerHTML"`) {
		t.Fatal(second)
	}
	unchanged := get("2")
	if strings.Contains(unchanged, "<article") || strings.Contains(unchanged, "hx-swap-oob") || !strings.Contains(unchanged, `hx-trigger="every 1s"`) {
		t.Fatal(unchanged)
	}
	job.loading = false
	job.more = true
	job.nextOffset = 2
	complete := get("2")
	if strings.Contains(complete, "<article") || strings.Contains(complete, "hx-trigger") || strings.Count(complete, `hx-swap-oob="innerHTML"`) != 2 || !strings.Contains(complete, ">Next →</a>") {
		t.Fatal(complete)
	}
	// Old responses are scoped to their original grid; an evicted job cannot restart or duplicate it.
	delete(app.discoveriesJobs, "GB")
	stale := get("2")
	if strings.Contains(stale, "hx-trigger") || strings.Contains(stale, "hx-swap-oob") || strings.Contains(stale, "<article") || len(app.discoveriesJobs) != 0 {
		t.Fatal(stale)
	}
	for _, bad := range []string{"-1", "25", "invalid"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/stations/discoveries?country=GB&since="+bad, nil))
		if w.Code != 400 {
			t.Fatal("invalid cursor accepted", bad, w.Code)
		}
	}
}
