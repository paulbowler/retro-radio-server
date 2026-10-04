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
	if first.Count != 30 || len(first.Items) != 25 || first.Items[1].EpisodeName != "Episode 0" || first.Items[1].EpisodeURL == "" || first.Items[1].ShowMime != "MP3" {
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

// Check wire order independently of Item unmarshalling: a streaming firmware
// parser can discard fields encountered before it knows the item type.
func TestPodcastWireFieldOrder(t *testing.T) {
	cases := []struct {
		item   Item
		fields []string
	}{
		{Item{Type: "ShowOnDemand", ShowID: "P1", ShowTitle: "History & Talk", ShowURL: "http://radio.local/episodes?podcast=P1", ShowBackup: "http://radio.local/episodes?podcast=P1"}, []string{"ItemType", "ShowOnDemandID", "ShowOnDemandName", "ShowOnDemandURL", "ShowOnDemandURLBackUp", "BookmarkShow"}},
		{episodeItem("http://radio.local", model.Episode{ID: "P1X1", Title: "Episode", Codec: "MP3", Description: strings.Repeat("é", 1000)}, model.Podcast{Title: "History & Talk"}, true), []string{"ItemType", "ShowEpisodeID", "ShowName", "Logo", "ShowEpisodeName", "ShowEpisodeURL", "BookmarkShow", "ShowDesc", "ShowFormat", "Lang", "Country", "ShowMime"}},
	}
	for _, c := range cases {
		raw, e := xml.Marshal(c.item)
		if e != nil {
			t.Fatal(e)
		}
		decoder := xml.NewDecoder(strings.NewReader(string(raw)))
		fields := []string{}
		depth := 0
		for {
			token, e := decoder.Token()
			if e != nil {
				break
			}
			switch token := token.(type) {
			case xml.StartElement:
				depth++
				if depth == 2 {
					fields = append(fields, token.Name.Local)
				}
			case xml.EndElement:
				depth--
			}
		}
		if strings.Join(fields, ",") != strings.Join(c.fields, ",") {
			t.Fatalf("wire fields %v; want %v\n%s", fields, c.fields, raw)
		}
		if !strings.Contains(string(raw), "History &amp; Talk") {
			t.Fatal("show title not escaped")
		}
		if c.item.Type == "ShowEpisode" && (len([]rune(c.item.ShowDesc)) > 267 || !strings.Contains(string(raw), "<ShowMime>MP3</ShowMime>")) {
			t.Fatal("unbounded or incomplete episode", string(raw))
		}
	}
}
