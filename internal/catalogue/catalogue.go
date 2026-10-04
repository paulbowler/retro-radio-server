// SPDX-License-Identifier: GPL-3.0-only
// Package catalogue integrates a bounded Radio-Browser cache with managed stations.
package catalogue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"retroradio.local/server/internal/delivery"
	"retroradio.local/server/internal/model"
	"retroradio.local/server/internal/store"
	"strings"
	"sync"
	"time"
)

const PageSize = 24

var countryCode = regexp.MustCompile(`^[A-Z]{2}$`)

var uuid = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)

type Result struct {
	Stations []model.Candidate
	Cached   bool
	Stale    bool
	Message  string
	More     bool
}
type Service struct {
	Store      *store.Store
	Client     *http.Client
	Mirrors    []string
	mu         sync.Mutex
	discovered []string
}

func New(s *store.Store) *Service {
	c := delivery.NewClient()
	c.Timeout = 8 * time.Second
	return &Service{Store: s, Client: c}
}
func (s *Service) mirrors(ctx context.Context) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.Mirrors) > 0 {
		return append([]string{}, s.Mirrors...)
	}
	if len(s.discovered) > 0 {
		return append([]string{}, s.discovered...)
	}
	// Radio-Browser recommends forward plus reverse DNS to obtain TLS mirror names.
	dnsCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	ips, _ := net.DefaultResolver.LookupNetIP(dnsCtx, "ip", "all.api.radio-browser.info")
	seen := map[string]bool{}
	for _, ip := range ips {
		if !delivery.SafeIP(ip) {
			continue
		}
		names, _ := net.DefaultResolver.LookupAddr(dnsCtx, ip.String())
		for _, name := range names {
			name = strings.TrimSuffix(name, ".")
			if strings.HasSuffix(name, ".api.radio-browser.info") && !strings.ContainsAny(name, "/:@") && !seen[name] {
				seen[name] = true
				s.discovered = append(s.discovered, "https://"+name)
			}
		}
	}
	if len(s.discovered) == 0 {
		s.discovered = []string{"https://de1.api.radio-browser.info", "https://nl1.api.radio-browser.info"}
	}
	rand.Shuffle(len(s.discovered), func(i, j int) { s.discovered[i], s.discovered[j] = s.discovered[j], s.discovered[i] })
	return append([]string{}, s.discovered...)
}
func (s *Service) fetch(ctx context.Context, path string) ([]model.Candidate, error) {
	ctx, cancel := context.WithTimeout(ctx, 18*time.Second)
	defer cancel()
	var last error
	for _, mirror := range s.mirrors(ctx) {
		raw := strings.TrimSuffix(mirror, "/") + path
		if e := delivery.ValidateURL(raw); e != nil {
			last = e
			continue
		}
		r, e := http.NewRequestWithContext(ctx, "GET", raw, nil)
		if e != nil {
			return nil, e
		}
		r.Header.Set("User-Agent", "RetroRadio/0.2 (self-hosted legacy radio compatibility)")
		r.Header.Set("Accept", "application/json")
		res, e := s.Client.Do(r)
		if e != nil {
			last = e
			continue
		}
		if res.StatusCode != 200 {
			res.Body.Close()
			last = fmt.Errorf("catalogue HTTP %d", res.StatusCode)
			continue
		}
		b, e := io.ReadAll(io.LimitReader(res.Body, (4<<20)+1))
		res.Body.Close()
		if e != nil || len(b) > 4<<20 {
			last = errors.New("catalogue response too large or incomplete")
			continue
		}
		var items []model.Candidate
		e = json.Unmarshal(b, &items)
		if e != nil {
			last = e
			continue
		}
		out := []model.Candidate{}
		for _, a := range items {
			if uuid.MatchString(a.UUID) && len(a.Name) > 0 && len(a.Name) <= 200 && len(a.Homepage) < 4096 && len(a.URL) < 4096 && len(a.Resolved) < 4096 && len(a.Tags) <= 2000 && len(a.Country) <= 100 && len(a.Language) <= 256 {
				a.Codec = strings.ToUpper(a.Codec)
				if a.Codec == "AAC+" || a.Codec == "HE-AAC" {
					a.Codec = "AAC"
				}
				out = append(out, a)
				if len(out) == PageSize {
					break
				}
			}
		}
		return out, nil
	}
	if last == nil {
		last = errors.New("no catalogue mirror available")
	}
	return nil, last
}
func (s *Service) Search(ctx context.Context, term string, offset int) (Result, error) {
	return s.SearchFiltered(ctx, term, "", "", offset)
}

// SearchFiltered combines directory name, country and genre filters.
func (s *Service) rawSearch(ctx context.Context, term, country, genre string, offset int) (Result, error) {
	term = strings.TrimSpace(term)
	country = strings.ToUpper(country)
	genre = strings.TrimSpace(genre)
	if len(term) > 120 || len(genre) > 100 || (country != "" && !countryCode.MatchString(country)) || offset < 0 || offset > 1000 {
		return Result{}, errors.New("search is too long or page is out of range")
	}
	key := fmt.Sprintf("directory-streams:v2:%s:%d:%d", strings.ToLower(term), offset, PageSize)
	if country != "" || genre != "" {
		key += ":" + country + ":" + strings.ToLower(genre)
	}
	cached, updated, e := s.Store.Cache(key)
	var previous []model.Candidate
	if e == nil {
		_ = json.Unmarshal(cached, &previous)
		if time.Since(updated) < 15*time.Minute {
			return Result{Stations: previous, Cached: true, More: len(previous) == PageSize}, nil
		}
	}
	q := url.Values{"name": {term}, "hidebroken": {"true"}, "limit": {fmt.Sprint(PageSize)}, "offset": {fmt.Sprint(offset)}, "order": {"clickcount"}, "reverse": {"true"}}
	if country != "" {
		q.Set("countrycode", country)
	}
	if genre != "" {
		q.Set("tag", genre)
	}
	items, e := s.fetch(ctx, "/json/stations/search?"+q.Encode())
	if e != nil {
		if previous != nil {
			return Result{Stations: previous, Cached: true, Stale: true, More: len(previous) == PageSize, Message: "Search is unavailable right now. Showing earlier results."}, nil
		}
		return Result{}, errors.New("Search is unavailable right now. You can still listen to saved stations.")
	}
	if e = s.Store.CachePut(key, items); e != nil {
		return Result{}, e
	}
	return Result{Stations: items, More: len(items) == PageSize}, nil
}

// Popular returns stations ranked by Radio-Browser listener clicks.
func (s *Service) rawPopular(ctx context.Context, country string, offset int) (Result, error) {
	country = strings.ToUpper(country)
	if (country != "" && !countryCode.MatchString(country)) || offset < 0 || offset > 1000 {
		return Result{}, errors.New("country or directory page is invalid")
	}
	key := fmt.Sprintf("directory-popular-country:v2:%s:%d", country, offset)
	cached, updated, err := s.Store.Cache(key)
	var previous []model.Candidate
	if err == nil && json.Unmarshal(cached, &previous) == nil && time.Since(updated) < 30*time.Minute {
		return Result{Stations: previous, Cached: true}, nil
	}
	q := url.Values{"order": {"clickcount"}, "reverse": {"true"}, "hidebroken": {"true"}, "limit": {fmt.Sprint(PageSize)}, "offset": {fmt.Sprint(offset)}}
	if country != "" {
		q.Set("countrycode", country)
	}
	items, err := s.fetch(ctx, "/json/stations/search?"+q.Encode())
	if err != nil {
		if previous != nil {
			return Result{Stations: previous, Cached: true, Stale: true, Message: "Showing earlier popular stations while search is unavailable."}, nil
		}
		return Result{}, errors.New("Popular stations are unavailable right now.")
	}
	if err = s.Store.CachePut(key, items); err != nil {
		return Result{}, err
	}
	return Result{Stations: items}, nil
}

func (s *Service) Import(ctx context.Context, id, device string) (model.Station, error) {
	return s.ImportToDevice(ctx, id, device, true)
}
func (s *Service) ImportToDevice(ctx context.Context, id, device string, favourite bool) (model.Station, error) {
	if _, err := s.Store.Device(device); err != nil {
		return model.Station{}, err
	}
	station, err := s.Select(ctx, id)
	if err != nil {
		return station, err
	}
	return station, s.Store.Assign(device, station.ID, favourite)
}

// Select saves a validated candidate in the library without assigning it to a radio.
func (s *Service) Select(ctx context.Context, id string) (model.Station, error) {
	station, err := s.Resolve(ctx, id)
	if err != nil {
		return station, err
	}
	return s.Store.SaveStation(station)
}

// Resolve validates catalogue metadata without adding the station to the library.
func (s *Service) Resolve(ctx context.Context, id string) (model.Station, error) {
	if !uuid.MatchString(id) {
		return model.Station{}, errors.New("invalid station identifier")
	}
	a, e := s.Store.Candidate(id)
	if e != nil {
		items, err := s.fetch(ctx, "/json/stations/byuuid/"+id)
		if err != nil || len(items) != 1 || items[0].UUID != id {
			return model.Station{}, errors.New("station not found; search again")
		}
		a = items[0]
	}
	raw := a.Resolved
	if raw == "" {
		raw = a.URL
	}
	if e = delivery.CheckTarget(ctx, raw); e != nil {
		return model.Station{}, errors.New("station URL is blocked or cannot be resolved")
	}
	station := model.CandidateStation(a)
	station.Name = model.ChannelName(station.Name)
	related, _ := s.Store.CachedCandidates()
	related = append([]model.Candidate{a}, related...)
	seen := map[string]bool{}
	for _, candidate := range related {
		v := model.CandidateStation(candidate)
		if seen[candidate.UUID] || !model.SameChannel(station, v) {
			continue
		}
		seen[candidate.UUID] = true
		station.Variants = append(station.Variants, model.StreamVariant{ID: candidate.UUID, UUID: candidate.UUID, URL: v.URL, Codec: v.Codec, Bitrate: v.Bitrate, HLS: v.HLS})
	}

	return station, nil
}
