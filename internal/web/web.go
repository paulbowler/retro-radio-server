// SPDX-License-Identifier: GPL-3.0-only
package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"retroradio.local/server/internal/catalogue"
	"retroradio.local/server/internal/content"
	"retroradio.local/server/internal/delivery"
	"retroradio.local/server/internal/model"
	"retroradio.local/server/internal/podcast"
	"retroradio.local/server/internal/store"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
)

//go:embed dashboard.html static/*
var assets embed.FS
var page = template.Must(template.New("dashboard.html").Funcs(template.FuncMap{
	"listeningDuration":    listeningDuration,
	"stationArtworkURL":    stationArtworkURL,
	"stationListenURL":     stationListenURL,
	"podcastDuration":      podcastDuration,
	"addOne":               func(n int) int { return n + 1 },
	"countryFlag":          countryFlag,
	"countryName":          countryName,
	"candidateStationCard": candidateStationCard,
	"seen":                 lastContact,
	"radioModel":           radioModel,
	"stationCountry":       stationCountry,
	"stationStatus":        stationStatus,
	"listeningEvents":      listeningEvents,
	"https":                func(s string) bool { return strings.HasPrefix(s, "https://") },
	"compatible": func(s model.Station) bool {
		_, e := delivery.PlayURL("http://radio.local", s, model.LegacyXML)
		return e == nil
	},
}).ParseFS(assets, "dashboard.html"))

type App struct {
	AgentVoiceDefault    string
	Music                content.Provider
	ArtworkClient        *http.Client
	artworkMu            sync.Mutex
	artworkLoads         sync.Map
	artworkPending       map[string]bool
	enrichmentMu         sync.Mutex
	enrichmentAt         map[string]time.Time
	Podcasts             *podcast.Service
	Store                *store.Store
	Relay                *delivery.Relay
	Catalogue            *catalogue.Service
	Base, User, Password string
	discoveriesMu        sync.Mutex
	discoveriesJobs      map[string]*discoveryJob
}
type card struct {
	AudioChoices []model.StreamVariant
	Preferred    string
	Discovery    bool
	Station      model.Station
	Devices      []model.Device
	Selected     string
	Health       model.Health
	Favourite    bool
	AutoCheck    bool
	Radios       int
	Message      string
	Error        bool
	Context      string
}
type candidateCard struct {
	Sequence  int
	Rank      int
	Discovery bool
	AutoCheck bool
	Health    model.Health
	Candidate model.Candidate
	Devices   []model.Device
	Selected  string
	Managed   *card
}

func candidateStationCard(item candidateCard) card {
	candidate := item.Candidate
	raw := candidate.Resolved
	if raw == "" {
		raw = candidate.URL
	}
	country := candidate.Country
	if country == "" {
		country = candidate.CountryCode
	}
	station := model.CandidateStation(candidate)
	station.Variants = []model.StreamVariant{{ID: candidate.UUID, UUID: candidate.UUID, URL: raw, Codec: candidate.Codec, Bitrate: candidate.Bitrate, HLS: candidate.HLS != 0}}
	for _, c := range candidate.Variants {
		v := model.CandidateStation(c)
		station.Variants = append(station.Variants, model.StreamVariant{ID: c.UUID, UUID: c.UUID, URL: v.URL, Codec: v.Codec, Bitrate: v.Bitrate, HLS: v.HLS})
	}
	if chosen, err := delivery.ChooseStream(station, model.LegacyXML, ""); err == nil {
		station = chosen
	}
	if item.Health.Working {
		for _, v := range station.Variants {
			if v.ID == item.Health.VariantID {
				variants := station.Variants
				station = v.Station(station)
				station.Variants = variants
				station.Codec = item.Health.Codec
				station.Bitrate = item.Health.Bitrate
				break
			}
		}
	}
	return card{Station: model.Station{Favicon: candidate.Favicon, Variants: station.Variants, Name: candidate.Name, URL: station.URL, Country: country, Codec: station.Codec, Bitrate: station.Bitrate, Tags: candidate.Tags, HLS: candidate.HLS != 0, RBUUID: candidate.UUID, Source: "radio-browser"}, Health: item.Health, Discovery: item.Discovery, AutoCheck: item.AutoCheck, Devices: item.Devices, Selected: item.Selected, Context: "/stations"}
}

type voiceOption struct{ ID, Name string }
type voiceGroup struct {
	Name   string
	Voices []voiceOption
}

type view struct {
	AgentVoiceGroups                            []voiceGroup
	MusicItems                                  []musicEntry
	AgentStation                                *agentStationCard
	MusicTitle, MusicParent                     string
	MusicRootPage                               bool
	MusicConnected                              bool
	Settings                                    store.Settings
	DiscoveryID                                 string
	DiscoveryCursor                             int
	Radios                                      []radioOverview
	PlayingRadios                               int
	DashboardVersion                            string
	Podcasts                                    []model.Podcast
	Podcast                                     *model.Podcast
	Episodes                                    []model.Episode
	PodcastResults                              []podcast.Result
	Page, Title, Base, Selected, Query, Message string
	Devices                                     []model.Device
	Cards                                       []card
	Events                                      []model.Event
	Active                                      []delivery.Active
	Results                                     catalogue.Result
	Candidates                                  []candidateCard
	Mode, Genre, PreviousURL, NextURL           string
	DiscoveryURL, Trail                         string
	PageNumber                                  int
	HasPrevious, HasNext                        bool
	Genres                                      []string
	Country                                     string
	Countries                                   []countryOption
	CountryAutomatic                            bool
	StationCount                                int
	Loading                                     bool
	Offset, Next, Previous                      int
	Custom                                      model.Station
	Device                                      *model.Device
	Error                                       bool
	Fragment                                    bool
	AutoCheck                                   bool
}

var sections = map[string]string{"/": "Dashboard", "/music": "Music", "/stations": "Stations", "/podcasts": "Podcasts", "/devices": "Radios", "/custom": "Add custom station", "/activity": "Activity", "/settings": "Help"}

func partial(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-History-Restore-Request") != "true"
}
func render(w http.ResponseWriter, name string, v any) {
	var b bytes.Buffer
	if name == "screen" {
		if data, ok := v.(view); ok {
			data.Fragment = true
			v = data
			b.WriteString("<title>" + template.HTMLEscapeString(data.Title) + " · Retro Radio</title>")
			if e := page.ExecuteTemplate(&b, "navigation", data); e != nil {
				http.Error(w, "Couldn’t open this page. Please try again.", 500)
				return
			}
		}
	}
	if e := page.ExecuteTemplate(&b, name, v); e != nil {
		http.Error(w, "Couldn’t open this page. Please try again.", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(b.Bytes())
}
func (a *App) Handler() http.Handler {
	if a.Catalogue == nil {
		a.Catalogue = catalogue.New(a.Store)
	}
	if a.Podcasts == nil {
		a.Podcasts = podcast.New(a.Store)
	}
	if a.ArtworkClient == nil {
		a.ArtworkClient = delivery.NewClient()
	}
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.FileServer(http.FS(assets)))
	mux.HandleFunc("GET /", a.screen)
	mux.HandleFunc("GET /preferences", a.preferences)
	mux.HandleFunc("POST /preferences", a.savePreferences)
	mux.HandleFunc("GET /stations/results", a.search)
	mux.HandleFunc("GET /stations/card", a.refreshCard)
	mux.HandleFunc("GET /stations/listen", a.listenCandidate)
	mux.HandleFunc("GET /music/agent", a.listenAgent)
	mux.HandleFunc("GET /activity/live", a.activity)
	mux.HandleFunc("GET /dashboard/live", a.dashboardLive)
	mux.HandleFunc("GET /stations/{station}/artwork", a.stationArtwork)
	mux.HandleFunc("GET /stations/candidate/{uuid}/artwork", a.candidateArtwork)
	mux.HandleFunc("GET /dashboard/discoveries", a.discoveries)
	mux.HandleFunc("GET /stations/discoveries", a.discoveries)
	mux.HandleFunc("POST /devices/rename", a.rename)
	mux.HandleFunc("POST /podcasts/subscribe", a.subscribePodcast)
	mux.HandleFunc("POST /podcasts/remove", a.removePodcast)
	mux.HandleFunc("GET /api/v1/podcasts", func(w http.ResponseWriter, r *http.Request) { v, e := a.Store.Podcasts(); respond(w, v, e) })
	mux.HandleFunc("GET /api/v1/episodes", func(w http.ResponseWriter, r *http.Request) {
		v, e := a.Store.Episodes(r.URL.Query().Get("podcast"))
		respond(w, v, e)
	})

	mux.HandleFunc("POST /stations/select", a.selectStation)
	mux.HandleFunc("POST /stations/remove", a.removeStation)
	mux.HandleFunc("POST /favourites", a.favourite)
	mux.HandleFunc("POST /custom", a.custom)
	mux.HandleFunc("POST /stations/check", a.check)
	mux.HandleFunc("POST /stations/candidate/check", a.checkCandidate)
	mux.HandleFunc("GET /diagnostics", a.diagnostics)
	mux.HandleFunc("POST /stations/audio", a.audioOptions)
	mux.HandleFunc("GET /api/v1/devices", func(w http.ResponseWriter, r *http.Request) { v, e := a.Store.Devices(); respond(w, v, e) })
	mux.HandleFunc("GET /api/v1/stations", func(w http.ResponseWriter, r *http.Request) {
		v, e := a.Store.Stations(r.URL.Query().Get("q"))
		respond(w, v, e)
	})
	mux.HandleFunc("GET /api/v1/activity", func(w http.ResponseWriter, r *http.Request) { v, e := a.Store.Events(); respond(w, v, e) })
	mux.HandleFunc("GET /api/v1/streams", func(w http.ResponseWriter, r *http.Request) { respond(w, a.Relay.Active(), nil) })
	mux.HandleFunc("GET /api/v1/favourites", func(w http.ResponseWriter, r *http.Request) {
		v, e := a.Store.Favourites(r.URL.Query().Get("device"))
		respond(w, v, e)
	})
	mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		v, e := a.Store.Health(r.URL.Query().Get("station"))
		if e != nil {
			http.Error(w, "Station health not available", 404)
			return
		}
		respond(w, v, nil)
	})
	mux.HandleFunc("GET /api/v1/play", func(w http.ResponseWriter, r *http.Request) {
		s, e := a.Store.Station(r.URL.Query().Get("station"), false)
		if e != nil {
			http.Error(w, "Station not found", 404)
			return
		}
		d, e := a.Store.Device(r.URL.Query().Get("device"))
		if e != nil {
			http.Error(w, "Radio not found", 404)
			return
		}
		preferred := a.Store.Preferred(d.ID, s.ID)
		if preferred == "" && a.Store.Settings().Quality == "low" {
			preferred = "low-data"
		}
		play, _, e := delivery.RadioPlayURL(a.Base, s, d, preferred)
		if e != nil {
			http.Error(w, e.Error(), 422)
			return
		}
		respond(w, map[string]string{"url": play}, nil)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Vary", "HX-Request, HX-History-Restore-Request")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; object-src 'none'; frame-ancestors 'none'")
		if asset, ok := publicAppAssets[r.URL.Path]; ok && (r.Method == "GET" || r.Method == "HEAD") {
			serveAppAsset(w, r, asset)
			return
		}
		if a.Password != "" {
			u, p, ok := r.BasicAuth()
			gotU, wantU := sha256.Sum256([]byte(u)), sha256.Sum256([]byte(a.User))
			gotP, wantP := sha256.Sum256([]byte(p)), sha256.Sum256([]byte(a.Password))
			if !ok || subtle.ConstantTimeCompare(gotU[:], wantU[:]) != 1 || subtle.ConstantTimeCompare(gotP[:], wantP[:]) != 1 {
				w.Header().Set("WWW-Authenticate", `Basic realm="Retro Radio"`)
				http.Error(w, "Authentication required", 401)
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}
func (a *App) baseView(r *http.Request) (view, error) {
	d, e := a.Store.Devices()
	if e != nil {
		return view{}, e
	}
	selected := r.URL.Query().Get("device")
	if r.Method == "POST" {
		selected = r.Form.Get("device")
	}
	valid := false
	for _, item := range d {
		if item.ID == selected {
			valid = true
		}
	}
	if !valid && len(d) > 0 {
		selected = d[0].ID
	}
	return view{Devices: d, Selected: selected, Base: a.Base, Countries: countryOptions, Query: strings.TrimSpace(r.URL.Query().Get("q"))}, nil
}
func (a *App) stationCard(s model.Station, v view) card {
	h, _ := a.Store.Health(s.ID)
	preferred := a.Store.Preferred(v.Selected, s.ID)
	if preferred == "" && a.Store.Settings().Quality == "low" {
		preferred = "low-data"
	}
	caps := model.LegacyXML
	if d, err := a.Store.Device(v.Selected); err == nil {
		caps = d.Capabilities
	}
	if chosen, err := delivery.ChooseStream(s, caps, preferred); err == nil {
		s = chosen
		for _, option := range s.Variants {
			if option.ID == s.VariantID && !option.Health.Checked.IsZero() {
				h = option.Health
				break
			}
		}
	}
	favs, _ := a.Store.Favourites(v.Selected)
	fav := false
	for _, f := range favs {
		if f.ID == s.ID {
			fav = true
		}
	}
	return card{AudioChoices: delivery.RankedStreams(s, caps, ""), Preferred: preferred, Station: s, Devices: v.Devices, Selected: v.Selected, Health: h, Favourite: fav, AutoCheck: false, Radios: a.Store.StationUsers(s.ID), Context: v.Page}
}
func (a *App) screen(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/favourites" {
		target := "/devices"
		if id := r.URL.Query().Get("device"); id != "" {
			target += "?device=" + url.QueryEscape(id)
		}
		if partial(r) {
			w.Header().Set("HX-Location", `{"path":"`+target+`","target":"#content"}`)
			w.WriteHeader(200)
		} else {
			http.Redirect(w, r, target, 303)
		}
		return
	}
	title, ok := sections[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	v, e := a.baseView(r)
	if e != nil {
		http.Error(w, "Couldn’t load your saved information. Please try again.", 503)
		return
	}
	v.Page = r.URL.Path
	v.Title = title
	v.AutoCheck = true
	switch v.Page {
	case "/":
		if e := a.dashboardView(&v); e != nil {
			http.Error(w, "Couldn’t load the overview. Please try again.", 503)
			return
		}
	case "/music":
		a.musicView(r, &v)
	case "/podcasts":
		a.podcastView(r, &v)
	case "/custom":
		stations, e := a.Store.Stations("")
		if e != nil {
			http.Error(w, "Couldn’t load your saved information. Please try again.", 503)
			return
		}
		for _, s := range stations {
			if v.Page == "/custom" && s.Source != "custom" {
				continue
			}
			v.Cards = append(v.Cards, a.stationCard(s, v))
		}
		v.Active = a.Relay.Active()
		v.Events, _ = a.Store.Events()
		if v.Page == "/custom" && r.URL.Query().Get("edit") != "" {
			s, e := a.Store.Station(r.URL.Query().Get("edit"), false)
			if e != nil || s.Source != "custom" {
				http.NotFound(w, r)
				return
			}
			v.Custom = s
			v.Title = "Edit custom station"
		}
	case "/devices":
		if r.URL.Query().Get("device") != "" {
			d, err := a.Store.Device(r.URL.Query().Get("device"))
			if err != nil {
				http.NotFound(w, r)
				return
			}
			v.Selected = d.ID
			v.Title = d.Name
			if err = a.deviceView(&v); err != nil {
				http.Error(w, "Couldn’t load your saved information. Please try again.", 503)
				return
			}
		}
	case "/favourites":
		f, e := a.Store.Favourites(v.Selected)
		if e != nil {
			http.Error(w, "Couldn’t load your saved information. Please try again.", 503)
			return
		}
		for _, s := range f {
			v.Cards = append(v.Cards, a.stationCard(s, v))
		}
	case "/activity":
		v.Events, _ = a.Store.Events()
		v.Active = a.Relay.Active()
	case "/stations":
		rememberCountry(w, r)
		v.Selected = ""
		a.searchView(r, &v)
	}
	name := "dashboard.html"
	if partial(r) {
		name = "screen"
	}
	render(w, name, v)
}
func (a *App) dashboardView(v *view) error {
	stations, err := a.Store.Stations("")
	if err != nil {
		return err
	}
	v.StationCount = len(stations)
	v.Active = a.Relay.Active()
	for _, d := range v.Devices {
		f, err := a.Store.Favourites(d.ID)
		if err != nil {
			return err
		}
		radio := radioOverview{Device: d, Favourites: len(f)}
		for _, playing := range v.Active {
			if playing.Device == d.ID && (radio.Playing == nil || playing.Since.After(radio.Playing.Since)) {
				current := playing
				radio.Playing = &current
			}
		}
		if radio.Playing != nil {
			v.PlayingRadios++
			radio.Image = radio.Playing.Artwork
			for _, station := range stations {
				if station.ID == radio.Playing.StationID {
					radio.Image = "/stations/" + station.ID + "/artwork"
					break
				}
			}
		}
		v.Radios = append(v.Radios, radio)
	}
	// Hash only visible content, so connection bookkeeping cannot trigger a swap.
	var status bytes.Buffer
	if err := page.ExecuteTemplate(&status, "dashboard-status", *v); err != nil {
		return err
	}
	v.DashboardVersion = fmt.Sprintf("%x", sha256.Sum256(status.Bytes()))
	return nil
}
func (a *App) dashboardLive(w http.ResponseWriter, r *http.Request) {
	v, err := a.baseView(r)
	if err == nil {
		err = a.dashboardView(&v)
	}
	if err != nil {
		http.Error(w, "Couldn’t load the overview. Please try again.", 503)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Dashboard-Version", v.DashboardVersion)
	if r.Header.Get("X-Dashboard-Version") == v.DashboardVersion {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	render(w, "dashboard-status", v)
}

func (a *App) searchView(r *http.Request, v *view) {
	v.Page = "/stations"
	v.Mode = "discover"
	if r.URL.Query().Get("mode") == "library" {
		v.Mode = "library"
	}
	v.Genre = strings.TrimSpace(r.URL.Query().Get("genre"))
	v.Countries = countryOptions
	if _, explicit := r.URL.Query()["country"]; explicit || v.Mode == "library" || v.Query != "" {
		v.Country = strings.ToUpper(r.URL.Query().Get("country"))
	} else {
		v.Country, v.CountryAutomatic = a.listenerCountry(r)
	}
	if (v.Country != "" && !validCountry(v.Country)) || len(v.Genre) > 100 {
		v.Error = true
		v.Message = "Choose a country and use a shorter genre name."
		return
	}
	v.Genres = stationGenres(a.Store)

	offset := 0
	if raw := r.URL.Query().Get("offset"); raw != "" {
		var err error
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 || offset > 1000 {
			v.Error = true
			v.Message = "This page is unavailable. Start again from the first page."
			return
		}
	}
	v.Offset = offset
	v.Next = offset + catalogue.PageSize
	v.Previous = offset - catalogue.PageSize
	if v.Previous < 0 {
		v.Previous = 0
	}
	v.Trail = r.URL.Query().Get("trail")
	setStationPagination(v)
	if v.Mode == "discover" && v.Query == "" && v.Genre == "" && r.URL.Query().Get("uuid") == "" {
		return
	}
	if v.Mode == "library" {
		saved, e := a.Store.NewestStations("")
		if e != nil {
			v.Error = true
			v.Message = "Couldn’t load your library. Please try again."
			return
		}
		v.Page = "/stations"
		v.Selected = ""
		for _, s := range saved {
			if !stationMatches(s, v.Query, v.Country, v.Genre) {
				continue
			}
			v.StationCount++
			if v.StationCount <= offset || len(v.Cards) >= catalogue.PageSize {
				continue
			}
			v.Cards = append(v.Cards, a.stationCard(s, *v))
		}
		v.HasNext = v.StationCount > offset+catalogue.PageSize && v.Next <= 1000
		return
	}
	var res catalogue.Result
	var e error
	if id := r.URL.Query().Get("uuid"); id != "" {
		var candidate model.Candidate
		candidate, e = a.Store.Candidate(id)
		if e != nil {
			v.Message = "This station is no longer listed. Search for it again."
			v.Error = true
			return
		}
		v.Query = candidate.Name
		v.Offset = 0
		res.Stations = []model.Candidate{candidate}
	} else {
		res, e = a.Catalogue.SearchFiltered(r.Context(), v.Query, v.Country, v.Genre, offset)
	}
	setStationPagination(v)
	v.HasNext = res.More && v.Next <= 1000
	v.Results = res
	if e != nil {
		v.Message = "Search is unavailable right now. You can still listen to saved stations."
		v.Error = true
		return
	}
	for _, c := range res.Stations {
		item := candidateCard{Candidate: c, Devices: v.Devices, Selected: v.Selected}
		if saved, e := a.Store.ChannelFor(model.CandidateStation(c)); e == nil {
			local := a.stationCard(saved, *v)
			local.Context = "/stations"
			item.Managed = &local
		}
		v.Candidates = append(v.Candidates, item)
	}
}
func (a *App) search(w http.ResponseWriter, r *http.Request) {
	rememberCountry(w, r)
	if !partial(r) {
		http.Redirect(w, r, "/stations?"+r.URL.RawQuery, 303)
		return
	}
	v, e := a.baseView(r)
	if e != nil {
		http.Error(w, "Couldn’t load your saved information. Please try again.", 503)
		return
	}
	v.AutoCheck = true
	v.Selected = ""
	a.searchView(r, &v)
	query := r.URL.Query()
	query.Del("device")
	w.Header().Set("HX-Replace-Url", "/stations?"+query.Encode())
	v.Fragment = true
	v.Page = "/stations"
	render(w, "search-response", v)
}
func (a *App) form(w http.ResponseWriter, r *http.Request) bool {
	if raw := r.Header.Get("Origin"); raw != "" {
		u, e := url.Parse(raw)
		if e != nil || !strings.EqualFold(u.Host, r.Host) || (u.Scheme != "http" && u.Scheme != "https") {
			http.Error(w, "Cross-origin request rejected", 403)
			return false
		}
	}
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		http.Error(w, "Cross-site request rejected", 403)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 12<<10)
	if e := r.ParseForm(); e != nil {
		http.Error(w, "Please check the details and try again.", 400)
		return false
	}
	return true
}
func (a *App) rename(w http.ResponseWriter, r *http.Request) {
	if !a.form(w, r) {
		return
	}
	name := strings.TrimSpace(r.Form.Get("name"))
	if len(name) < 1 || len(name) > 80 {
		http.Error(w, "Enter a short name for your radio.", 400)
		return
	}
	if e := a.Store.Rename(r.Form.Get("device"), name); e != nil {
		http.Error(w, "Radio not found", 404)
		return
	}
	if !partial(r) {
		http.Redirect(w, r, "/devices?device="+url.QueryEscape(r.Form.Get("device")), 303)
		return
	}
	v, e := a.baseView(r)
	if e != nil {
		http.Error(w, "Couldn’t load your saved information. Please try again.", 503)
		return
	}
	v.Page = "/devices"
	if e = a.deviceView(&v); e != nil {
		http.Error(w, "Couldn’t load your saved information. Please try again.", 503)
		return
	}
	v.Title = v.Device.Name
	render(w, "screen", v)
}
func (a *App) favourite(w http.ResponseWriter, r *http.Request) {
	if !a.form(w, r) {
		return
	}
	id, device := r.Form.Get("station"), r.Form.Get("device")
	if e := a.Store.Favourite(device, id, r.Form.Get("action") == "add"); e != nil {
		http.Error(w, "This radio or station is no longer available.", 400)
		return
	}
	if !partial(r) {
		http.Redirect(w, r, "/devices?device="+url.QueryEscape(device), 303)
		return
	}
	if r.Form.Get("context") == "/favourites" && r.Form.Get("action") == "remove" {
		render(w, "notice", view{Message: "Favourite removed"})
		return
	}
	s, e := a.Store.Station(id, false)
	if e != nil {
		http.NotFound(w, r)
		return
	}
	v, e := a.baseView(r)
	if e != nil {
		http.Error(w, "Couldn’t load your saved information. Please try again.", 503)
		return
	}
	v.Page = r.Form.Get("context")
	c := a.stationCard(s, v)
	renderCard(w, c)
}
func (a *App) custom(w http.ResponseWriter, r *http.Request) {
	if !a.form(w, r) {
		return
	}
	v, e := a.baseView(r)
	if e != nil {
		http.Error(w, "Couldn’t load your saved information. Please try again.", 503)
		return
	}
	bitrate, _ := strconv.Atoi(r.Form.Get("bitrate"))
	s := model.Station{ID: r.Form.Get("id"), Name: strings.TrimSpace(r.Form.Get("name")), URL: strings.TrimSpace(r.Form.Get("url")), Codec: strings.ToUpper(r.Form.Get("codec")), Bitrate: bitrate, Source: "custom", Country: strings.TrimSpace(r.Form.Get("country")), Tags: strings.TrimSpace(r.Form.Get("tags"))}
	v.Custom = s
	fail := func(message string) {
		v.Error = true
		v.Message = message
		if partial(r) {
			actionResult(w, false, "Couldn’t save station", message)
			v.Message = ""
			render(w, "custom-form", v)
			return
		}
		v.Page = "/custom"
		v.Title = "Custom stations"
		render(w, "dashboard.html", v)
	}
	if len(s.Name) < 1 || len(s.Name) > 100 || len(s.URL) > 4096 || len(s.Country) > 100 || len(s.Tags) > 500 || s.Bitrate < 0 || s.Bitrate > 2000 {
		fail("Check the station name and listening link, then try again.")
		return
	}
	if s.Codec != "MP3" && s.Codec != "AAC" && s.Codec != "UNKNOWN" {
		fail("The audio format could not be recognised. Please try again.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if e = delivery.CheckTarget(ctx, s.URL); e != nil {
		fail("This listening link cannot be reached. Use the station’s public listening link.")
		return
	}
	health, checkErr := a.Relay.Probe(r.Context(), s)
	if checkErr != nil || !health.Working {
		fail(streamProblem(health))
		return
	}
	s.Codec = health.Codec
	s.HLS = health.Adaptive != ""
	s.Bitrate = health.Bitrate
	saved, saveErr := a.Store.SaveStation(s)
	if saveErr != nil {
		fail("Couldn’t save this station. Please try again.")
		return
	}
	health.StationID = saved.ID
	if e = a.Store.SaveHealth(health); e != nil {
		fail("Couldn’t finish saving this station. Please try again.")
		return
	}
	if partial(r) {
		w.Header().Set("HX-Location", `{"path":"/stations?mode=library","target":"#content"}`)
		actionResult(w, true, "Station saved", saved.Name+" is ready to listen to on all your radios.")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/stations?mode=library", 303)
}
func (a *App) checkCandidate(w http.ResponseWriter, r *http.Request) {
	if !a.form(w, r) {
		return
	}
	candidate, err := a.Store.Candidate(r.Form.Get("uuid"))
	if err != nil {
		http.Error(w, "This station is no longer listed. Search for it again.", 404)
		return
	}
	item := candidateCard{Candidate: candidate}
	station, err := a.Catalogue.Resolve(r.Context(), candidate.UUID)
	if err != nil {
		item.Health = model.Health{Checked: time.Now().UTC(), Message: err.Error()}
	} else {
		item.Health, _ = a.Relay.Probe(r.Context(), station)
		if item.Health.Working {
			item.Candidate.Codec = item.Health.Codec
			item.Candidate.Bitrate = item.Health.Bitrate
		}
	}
	render(w, "candidate-card", item)
}

func (a *App) check(w http.ResponseWriter, r *http.Request) {
	if !a.form(w, r) {
		return
	}
	s, e := a.Store.Station(r.Form.Get("station"), false)
	if e != nil {
		http.NotFound(w, r)
		return
	}
	s = a.enrichChannel(r.Context(), s)
	_, e = a.Relay.Check(r.Context(), s)
	if e != nil {
		http.Error(w, "Couldn’t save the station check. Please try again.", 503)
		return
	}
	if !partial(r) {
		http.Redirect(w, r, "/favourites?device="+url.QueryEscape(r.Form.Get("device")), 303)
		return
	}
	s, _ = a.Store.Station(s.ID, false)
	v, e := a.baseView(r)
	if e != nil {
		http.Error(w, "Couldn’t load your saved information. Please try again.", 503)
		return
	}
	v.Page = r.Form.Get("context")
	if v.Page == "/stations" {
		v.Selected = ""
	}
	renderCard(w, a.stationCard(s, v))
}
func (a *App) activity(w http.ResponseWriter, r *http.Request) {
	v, e := a.baseView(r)
	if e != nil {
		http.Error(w, "Couldn’t load your saved information. Please try again.", 503)
		return
	}
	v.Events, e = a.Store.Events()
	if e != nil {
		http.Error(w, "Couldn’t load your saved information. Please try again.", 503)
		return
	}
	v.Active = a.Relay.Active()
	render(w, "live-activity", v)
}
func (a *App) diagnostics(w http.ResponseWriter, r *http.Request) {
	d, e := a.Store.Devices()
	if e != nil {
		respond(w, nil, e)
		return
	}
	events, e := a.Store.Events()
	if e != nil {
		respond(w, nil, e)
		return
	}
	for i := range d {
		d[i].IP = ""
		d[i].Name = "Radio"
	}
	w.Header().Set("Content-Disposition", `attachment; filename="retro-radio-diagnostics.json"`)
	respond(w, map[string]any{"version": "0.2.0", "devices": d, "activity": events}, nil)
}
func respond(w http.ResponseWriter, v any, e error) {
	w.Header().Set("Content-Type", "application/json")
	if e != nil {
		http.Error(w, `{"error":"Operation failed"}`, 500)
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

func (a *App) refreshCard(w http.ResponseWriter, r *http.Request) {
	s, e := a.Store.Station(r.URL.Query().Get("station"), false)
	if e != nil {
		http.NotFound(w, r)
		return
	}
	v, e := a.baseView(r)
	if e != nil {
		http.Error(w, "Couldn’t load your saved information. Please try again.", 503)
		return
	}
	v.Page = r.URL.Query().Get("context")
	renderCard(w, a.stationCard(s, v))
}

func renderCard(w http.ResponseWriter, c card) {
	render(w, "radio-station", c)
}
func (a *App) deviceView(v *view) error {
	d, e := a.Store.Device(v.Selected)
	if e != nil {
		return e
	}
	v.Device = &d
	stations, e := a.Store.NewestStations("")
	if e != nil {
		return e
	}
	for _, s := range stations {
		v.Cards = append(v.Cards, a.stationCard(s, *v))
	}
	return nil
}

// actionResult is handled by the persistent result dialog outside HTMX page swaps.
func actionResult(w http.ResponseWriter, success bool, title, message string) {
	payload, _ := json.Marshal(map[string]any{"station-result": map[string]any{"success": success, "title": title, "message": message}})
	// XMLHttpRequest interprets header bytes as Latin-1; use JSON Unicode escapes.
	var header strings.Builder
	for _, r := range string(payload) {
		if r < 128 {
			header.WriteRune(r)
			continue
		}
		for _, code := range utf16.Encode([]rune{r}) {
			fmt.Fprintf(&header, "\\u%04x", code)
		}
	}
	w.Header().Set("HX-Trigger", header.String())
}
func (a *App) selectStation(w http.ResponseWriter, r *http.Request) {
	if !a.form(w, r) {
		return
	}
	s, e := a.Catalogue.Resolve(r.Context(), r.Form.Get("uuid"))
	fail := func(message string) {
		if partial(r) {
			actionResult(w, false, "Couldn’t add station", message)
			w.WriteHeader(http.StatusNoContent)
		} else {
			http.Error(w, message, 422)
		}
	}
	if e != nil {
		fail("This station is unavailable. Search for it again.")
		return
	}
	if candidate, err := a.Store.Candidate(s.RBUUID); err == nil {
		if _, err = a.Catalogue.SearchFiltered(r.Context(), model.ChannelName(s.Name), candidate.CountryCode, "", 0); err == nil {
			if enriched, err := a.Catalogue.Resolve(r.Context(), s.RBUUID); err == nil {
				s = enriched
			}
		}
	}
	s, e = a.admitChannel(r.Context(), s)
	if e != nil {
		fail("We couldn’t play this station. Try again later.")
		return
	}
	if !partial(r) {
		http.Redirect(w, r, "/stations?mode=library", 303)
		return
	}
	v, e := a.baseView(r)
	if e != nil {
		http.Error(w, "Couldn’t load your saved information. Please try again.", 503)
		return
	}
	v.Page = "/stations"
	v.Selected = ""
	c := a.stationCard(s, v)
	actionResult(w, true, "Station added", s.Name+" is ready to listen to on all your radios.")
	renderCard(w, c)
}

func (a *App) removeStation(w http.ResponseWriter, r *http.Request) {
	if !a.form(w, r) {
		return
	}
	if e := a.Store.DeleteStation(r.Form.Get("station")); e != nil {
		http.Error(w, "Couldn’t remove this station. Refresh the library and try again.", 404)
		return
	}
	if partial(r) {
		w.Header().Set("HX-Location", `{"path":"/stations?mode=library","target":"#content"}`)
		w.WriteHeader(200)
		return
	}
	http.Redirect(w, r, "/stations?mode=library", 303)
}
