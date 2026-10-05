// SPDX-License-Identifier: GPL-3.0-only
// Package frontierxml implements the legacy BrowseXML wire format.
package frontierxml

import (
	"encoding/xml"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"retroradio.local/server/internal/content"
	"retroradio.local/server/internal/delivery"
	"retroradio.local/server/internal/model"
	"retroradio.local/server/internal/store"
	"strconv"
	"strings"
	"sync"
)

func text(s string) *string { return &s }

const Challenge = "<EncryptedToken>3a3f5ac48a1dab4e</EncryptedToken>"

var route = regexp.MustCompile(`(?i)^/setupapp/([a-z0-9_-]+)/asp/browsexml/([a-z]+)\.asp$`)
var identifier = regexp.MustCompile(`^[a-zA-Z0-9_-]{8,128}$`)

type Item struct {
	Type        string `xml:"ItemType"`
	ShowID      string `xml:"ShowOnDemandID,omitempty"`
	ShowTitle   string `xml:"ShowOnDemandName,omitempty"`
	ShowURL     string `xml:"ShowOnDemandURL,omitempty"`
	ShowBackup  string `xml:"ShowOnDemandURLBackUp,omitempty"`
	EpisodeID   string `xml:"ShowEpisodeID,omitempty"`
	ShowName    string `xml:"ShowName,omitempty"`
	EpisodeName string `xml:"ShowEpisodeName,omitempty"`
	EpisodeURL  string `xml:"ShowEpisodeURL,omitempty"`
	ShowDesc    string `xml:"ShowDesc,omitempty"`
	ShowMime    string `xml:"ShowMime,omitempty"`
	ShowFormat  string `xml:"ShowFormat,omitempty"`

	Title          string  `xml:"Title,omitempty"`
	Dir            string  `xml:"UrlDir,omitempty"`
	Backup         string  `xml:"UrlDirBackUp,omitempty"`
	Previous       string  `xml:"UrlPrevious,omitempty"`
	PreviousBackup string  `xml:"UrlPreviousBackUp,omitempty"`
	ID             string  `xml:"StationId,omitempty"`
	Name           string  `xml:"StationName,omitempty"`
	URL            string  `xml:"StationUrl,omitempty"`
	Desc           string  `xml:"StationDesc,omitempty"`
	Logo           *string `xml:"Logo,omitempty"`
	Format         string  `xml:"StationFormat,omitempty"`
	Location       string  `xml:"StationLocation,omitempty"`
	Bitrate        int     `xml:"StationBandWidth,omitempty"`
	Mime           string  `xml:"StationMime,omitempty"`
	Reliability    int     `xml:"Relia,omitempty"`
	SearchURL      string  `xml:"SearchURL,omitempty"`
	SearchBackup   string  `xml:"SearchURLBackUp,omitempty"`
	Caption        string  `xml:"SearchCaption,omitempty"`
	Textbox        *string `xml:"SearchTextbox,omitempty"`
	Go             string  `xml:"SearchButtonGo,omitempty"`
	Cancel         string  `xml:"SearchButtonCancel,omitempty"`
}
type list struct {
	XMLName xml.Name `xml:"ListOfItems"`
	Count   int      `xml:"ItemCount"`
	Items   []Item   `xml:"Item"`
}
type Handler struct {
	musicMu  sync.Mutex
	musicIDs map[string]musicStationID

	SupportsHTTPS bool // Operator-confirmed capability.
	Music         content.Provider
	Store         *store.Store
	Base          string
}

func send(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprint(w, body)
}
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m := route.FindStringSubmatch(r.URL.Path)
	if m == nil {
		http.NotFound(w, r)
		return
	}
	if r.Method != "GET" {
		http.Error(w, "method not allowed", 405)
		return
	}
	endpoint := strings.ToLower(m[2])
	q := r.URL.Query()
	token := q.Get("mac")
	if endpoint == "loginxml" && token == "" {
		send(w, Challenge)
		return
	}
	if !identifier.MatchString(token) {
		http.Error(w, "missing or invalid radio identifier", 401)
		return
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	d, err := h.Store.Seen(token, m[1], q.Get("fver"), ip)
	if err != nil {
		http.Error(w, "device storage unavailable", 503)
		return
	}
	if h.SupportsHTTPS {
		d.Capabilities.HTTPS = true
	}
	// Only path/endpoint, never the identifying query, is recorded.
	h.Store.Log(d.ID, "Directory request", r.URL.Path)
	base := h.Base + "/setupapp/" + m[1] + "/asp/BrowseXML/"
	root := base + "loginXML.asp?gofile="
	prev := Item{Type: "Previous", Previous: root, PreviousBackup: root}
	items := []Item{prev}
	count := 0
	dir := func(title, link string) Item { return Item{Type: "Dir", Title: title, Dir: link, Backup: link} }
	switch endpoint {
	case "loginxml":
		items = append(items, dir("Radio", base+"navXML.asp?gofile=RadioMenu"), dir("Podcasts", base+"navXML.asp?gofile=Podcasts"))
		count = 2
		if h.Music != nil {
			items = append(items, dir("Music", base+"navXML.asp?gofile=Music"))
			count++
		}
	case "navxml", "favxml", "afavxml", "search":
		if endpoint == "navxml" && q.Get("gofile") == "RadioMenu" {
			items = append(items, dir("All stations", base+"navXML.asp?gofile=Radio"), dir("By country", base+"navXML.asp?group=country"), dir("By genre", base+"navXML.asp?group=genre"), dir("Favourites", base+"FavXML.asp?empty="), Item{Type: "Search", SearchURL: base + "Search.asp?sSearchtype=1", SearchBackup: base + "Search.asp?sSearchtype=1", Caption: "Search stations", Textbox: text(""), Go: "Search", Cancel: "Cancel"})
			count = 5
			break
		}
		if (endpoint == "navxml" && (q.Get("gofile") == "Music" || q.Get("gofile") == "MyMusic" || q.Has("music"))) || (endpoint == "search" && q.Get("sSearchtype") == "3" && h.isMusicStation(q.Get("Search"))) {
			items, count, err = h.musicItems(r.Context(), base, q, d.Capabilities)
			if endpoint == "search" {
				if err != nil {
					h.Store.Log(d.ID, "Music lookup failed", err.Error())
				} else if len(items) > 1 {
					items[1].URL += "?radio=" + url.QueryEscape(d.ID)
					h.Store.Log(d.ID, "Music lookup", items[1].Name)
				}
			}
			break
		}
		if (endpoint == "navxml" && (q.Get("gofile") == "Podcasts" || q.Get("podcast") != "")) || (endpoint == "search" && q.Get("sSearchtype") == "5") {
			items, count, err = h.podcastItems(base, q)
			for i := range items {
				if items[i].EpisodeURL != "" {
					items[i].EpisodeURL += "?radio=" + url.QueryEscape(d.ID)
				}
			}
			break
		}
		// Radio listings and lookup return to their category menu.
		items[0].Previous = base + "navXML.asp?gofile=RadioMenu"
		items[0].PreviousBackup = items[0].Previous
		var stations []model.Station
		if endpoint == "search" && q.Get("sSearchtype") == "3" {
			var s model.Station
			s, err = h.Store.Station(q.Get("Search"), false)
			if err == nil {
				var play string
				preferred := h.Store.Preferred(d.ID, s.ID)
				if preferred == "" && h.Store.Settings().Quality == "low" {
					preferred = "low-data"
				}
				play, s, err = delivery.RadioPlayURL(h.Base, s, d, preferred)
				if err == nil {
					items = append(items, Item{Type: "Station", ID: s.ID, Name: s.Name, URL: play, Desc: stationDescription(s), Logo: text(h.Base + "/artwork/" + s.ID + ".jpg"), Format: "Radio", Location: s.Country, Bitrate: s.Bitrate, Mime: s.Codec, Reliability: 5})
					count = 1
					h.Store.Log(d.ID, "Station lookup", s.Name)
					if strings.HasPrefix(play, h.Base+"/stream/") {
						h.Store.Log(d.ID, "Compatibility proxy", "Upstream audio relayed over HTTP")
					}
				}
			}
			if err != nil {
				h.Store.Log(d.ID, "Station lookup failed", "The selected station ID could not be resolved")
			}
		} else {
			if endpoint == "favxml" || endpoint == "afavxml" {
				stations, err = h.Store.Favourites(d.ID)
			} else {
				term := ""
				if endpoint == "search" {
					term = q.Get("Search")
				}
				stations, err = h.Store.Stations(term)
			}
			compatible := []model.Station{}
			for _, s := range stations {
				if _, e := delivery.PlayURL(h.Base, s, d.Capabilities); e == nil {
					compatible = append(compatible, s)
				}
			}
			stations = compatible
			entries := []Item{}
			group, value := q.Get("group"), q.Get("value")
			if endpoint == "navxml" && (group == "country" || group == "genre") {
				entries = groupedItems(stations, group, value, base)
				if value != "" {
					parent := base + "navXML.asp?group=" + group
					items[0] = Item{Type: "Previous", Previous: parent, PreviousBackup: parent}
				}
			} else {
				for _, s := range stations {
					entries = append(entries, Item{Type: "Station", ID: s.ID, Name: s.Name})
				}
			}
			count = len(entries)
			start, end := 1, count
			if v := q.Get("startItems"); v != "" {
				start, _ = strconv.Atoi(v)
			}
			if v := q.Get("endItems"); v != "" {
				end, _ = strconv.Atoi(v)
			}
			if start < 1 {
				start = 1
			}
			if end > count {
				end = count
			}
			for i, item := range entries {
				if i+1 >= start && i+1 <= end {
					items = append(items, item)
				}
			}

		}
	case "addfav", "removefavs":
		id := q.Get("ID")
		if id == "" {
			id = q.Get("StationId")
		}
		if id == "" {
			id = q.Get("stationid")
		}
		if id == "" {
			id = q.Get("Search")
		}
		err = h.Store.Favourite(d.ID, id, endpoint == "addfav")
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "catalogue item unavailable", 404)
		return
	}
	b, err := xml.Marshal(list{Count: count, Items: items})
	if err != nil {
		http.Error(w, "XML encoding failed", 500)
		return
	}
	// All Item fields are element text, not XML attributes. Quotes are legal
	// literal text; some legacy displays show their numeric entities verbatim.
	body := strings.ReplaceAll(string(b), "&#39;", "'")
	body = strings.ReplaceAll(body, "&#34;", `"`)
	send(w, `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`+body)
}
func ValidateBase(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "http" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return fmt.Errorf("RETRO_PUBLIC_URL must be an HTTP origin, e.g. http://192.168.1.20")
	}
	return nil
}

func stationDescription(s model.Station) string {
	if s.Source == "seed" {
		return "Smooth UK live radio"
	}
	return s.Name + " live radio"
}
