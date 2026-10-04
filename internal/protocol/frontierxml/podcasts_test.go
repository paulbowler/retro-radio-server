// SPDX-License-Identifier: GPL-3.0-only
package frontierxml

import (
	"encoding/xml"
	"fmt"
	"net/http/httptest"
	"retroradio.local/server/internal/model"
	"strings"
	"testing"
	"time"
)

func TestPodcastMenuEpisodesAndLookup(t *testing.T) {
	h := newHandler(t)
	episodes := []model.Episode{}
	for i := 0; i < 30; i++ {
		episodes = append(episodes, model.Episode{GUID: fmt.Sprint(i), Title: fmt.Sprintf("Episode %d", i), URL: "https://audio.example.org/episode.mp3", Codec: "MP3", Published: time.Now().Add(-time.Duration(i) * time.Hour)})
	}
	p, e := h.Store.SavePodcast(model.Podcast{Feed: "https://example.org/feed", Title: "Show & Talk"}, episodes)
	if e != nil {
		t.Fatal(e)
	}
	get := func(path string) list {
		t.Helper()
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/setupapp/pure/asp/BrowseXML/"+path+"&mac=0123456789abcdef", nil))
		if w.Code != 200 || strings.Contains(w.Body.String(), "https:") {
			t.Fatal(w.Code, w.Body.String())
		}
		var l list
		if e := xml.Unmarshal(w.Body.Bytes(), &l); e != nil {
			t.Fatal(e)
		}
		return l
	}
	root := get("loginXML.asp?gofile=")
	found := false
	for _, item := range root.Items {
		if item.Title == "Podcasts" {
			found = true
		}
	}
	if !found {
		t.Fatal("podcast root missing")
	}
	shows := get("navXML.asp?gofile=Podcasts")
	if shows.Count != 1 || shows.Items[1].Type != "ShowOnDemand" || shows.Items[1].ShowID != p.ID {
		t.Fatal(shows)
	}
	first := get("navXML.asp?podcast=" + p.ID + "&startItems=1&endItems=24")
	if first.Count != 30 || len(first.Items) != 25 || first.Items[1].EpisodeName != "Episode 0" {
		t.Fatal(first)
	}
	second := get("navXML.asp?podcast=" + p.ID + "&startItems=25&endItems=48")
	if second.Count != 30 || len(second.Items) != 7 {
		t.Fatal(second)
	}
	lookup := get("Search.asp?sSearchtype=5&Search=" + first.Items[1].EpisodeID)
	if lookup.Count != 1 || lookup.Items[1].EpisodeURL != h.Base+"/episode/"+first.Items[1].EpisodeID || lookup.Items[1].ShowMime != "MP3" || lookup.Items[1].ShowName != p.Title {
		t.Fatal(lookup)
	}
}
