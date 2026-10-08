// SPDX-License-Identifier: GPL-3.0-only
package podcast

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"retroradio.local/server/internal/delivery"
	"retroradio.local/server/internal/maintenance"
	"retroradio.local/server/internal/model"
	"retroradio.local/server/internal/store"
	"strings"
	"sync"
	"time"
)

const maxFeed = 8 << 20

type Result struct {
	Title      string `json:"collectionName"`
	Author     string `json:"artistName"`
	Feed       string `json:"feedUrl"`
	Subscribed bool   `json:"-"`
}
type cached struct {
	Results []Result
	At      time.Time
}
type Service struct {
	Maintenance *maintenance.Gate
	Store       *store.Store
	Client      *http.Client
	SearchURL   string
	mu          sync.Mutex
	refresh     sync.Mutex
	cache       map[string]cached
}

func New(s *store.Store) *Service {
	return &Service{Store: s, Client: delivery.NewClient(), SearchURL: "https://itunes.apple.com/search", cache: map[string]cached{}}
}
func (s *Service) Subscribe(ctx context.Context, raw string) (model.Podcast, error) {
	return s.fetch(ctx, model.Podcast{Feed: strings.TrimSpace(raw)})
}
func (s *Service) fetch(ctx context.Context, p model.Podcast) (model.Podcast, error) {
	if len(p.Feed) > 4096 || delivery.ValidateURL(p.Feed) != nil {
		return p, errors.New("invalid public feed")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, e := http.NewRequestWithContext(ctx, "GET", p.Feed, nil)
	if e != nil {
		return p, e
	}
	req.Header.Set("User-Agent", "RetroRadio/0.3")
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml, text/xml")
	res, e := s.Client.Do(req)
	if e != nil {
		return p, e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return p, errors.New("feed unavailable")
	}
	data, e := io.ReadAll(io.LimitReader(res.Body, maxFeed+1))
	if e != nil {
		return p, e
	}
	if len(data) > maxFeed {
		return p, errors.New("feed too large")
	}
	parsed, episodes, e := Parse(data, res.Request.URL.String())
	if e != nil {
		return p, e
	}
	parsed.Feed = p.Feed
	parsed.ID = p.ID
	return s.Store.SavePodcast(parsed, episodes)
}
func (s *Service) Search(ctx context.Context, term, country string) ([]Result, error) {
	if len(term) > 120 {
		return nil, errors.New("search too long")
	}
	term = strings.TrimSpace(term)
	if term == "" {
		return []Result{}, nil
	}
	key := country + ":" + strings.ToLower(term)
	s.mu.Lock()
	defer s.mu.Unlock()
	if hit, ok := s.cache[key]; ok && time.Since(hit.At) < 15*time.Minute {
		return append([]Result(nil), hit.Results...), nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	u, e := url.Parse(s.SearchURL)
	if e != nil {
		return nil, e
	}
	q := u.Query()
	q.Set("term", term)
	q.Set("media", "podcast")
	q.Set("entity", "podcast")
	q.Set("limit", "24")
	q.Set("country", country)
	u.RawQuery = q.Encode()
	req, e := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if e != nil {
		return nil, e
	}
	res, e := s.Client.Do(req)
	if e != nil {
		return nil, e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, errors.New("search unavailable")
	}
	data, e := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	if e != nil {
		return nil, e
	}
	var body struct {
		Results []Result `json:"results"`
	}
	if e = json.Unmarshal(data, &body); e != nil {
		return nil, e
	}
	out := []Result{}
	seen := map[string]bool{}
	for _, r := range body.Results {
		if delivery.ValidateURL(r.Feed) != nil || len(r.Feed) > 4096 || seen[r.Feed] {
			continue
		}
		seen[r.Feed] = true
		r.Title = plain(r.Title, 200)
		r.Author = plain(r.Author, 160)
		out = append(out, r)
		if len(out) == 24 {
			break
		}
	}
	if len(s.cache) >= 128 {
		s.cache = map[string]cached{}
	}
	s.cache[key] = cached{out, time.Now()}
	return append([]Result(nil), out...), nil
}

// The last successful episode list remains available if a publisher is offline.
func (s *Service) Refresh(ctx context.Context) {
	s.refresh.Lock()
	defer s.refresh.Unlock()
	all, e := s.Store.Podcasts()
	if e != nil {
		return
	}
	for _, p := range all {
		if ctx.Err() != nil {
			return
		}
		if time.Since(p.Updated) < time.Hour {
			continue
		}
		_, _ = s.fetch(ctx, p)
	}
}
func (s *Service) Run(ctx context.Context) {
	s.runRefresh(ctx)
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.runRefresh(ctx)
		}
	}
}

func (s *Service) runRefresh(ctx context.Context) {
	if s.Maintenance == nil {
		s.Refresh(ctx)
		return
	}
	s.Maintenance.Do(ctx, s.Refresh)
}

func (s *Service) ClearSearchCache() { s.mu.Lock(); defer s.mu.Unlock(); s.cache = map[string]cached{} }
