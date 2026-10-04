// SPDX-License-Identifier: GPL-3.0-only
package web

import (
	"context"
	"net/http"
	"retroradio.local/server/internal/catalogue"
	"retroradio.local/server/internal/delivery"
	"retroradio.local/server/internal/model"
	"sort"
	"sync"
	"time"
)

type discoveryJob struct {
	mu      sync.Mutex
	started time.Time
	country string
	cancel  context.CancelFunc
	items   []candidateCard
	loading bool
	message string
	done    chan struct{}
}

func (j *discoveryJob) count() int { j.mu.Lock(); defer j.mu.Unlock(); return len(j.items) }
func (a *App) discoveries(w http.ResponseWriter, r *http.Request) {
	selected := r.URL.Query().Get("country")
	if selected != "" && !validCountry(selected) {
		http.Error(w, "Invalid country", 400)
		return
	}
	country, _ := listenerCountry(r)
	if _, explicit := r.URL.Query()["country"]; explicit && selected == "" {
		country = ""
	}
	if _, explicit := r.URL.Query()["country"]; country == "" && !explicit {
		render(w, "discoveries", view{Message: "Choose your country to see popular stations."})
		return
	}
	rememberCountry(w, r)

	a.discoveriesMu.Lock()
	if a.discoveriesJobs == nil {
		a.discoveriesJobs = map[string]*discoveryJob{}
	}
	job := a.discoveriesJobs[country]
	if job == nil || time.Since(job.started) > 2*time.Minute {
		if job != nil {
			job.cancel()
		}
		if len(a.discoveriesJobs) >= 8 {
			var oldest string
			for code, item := range a.discoveriesJobs {
				if oldest == "" || item.started.Before(a.discoveriesJobs[oldest].started) {
					oldest = code
				}
			}
			a.discoveriesJobs[oldest].cancel()
			delete(a.discoveriesJobs, oldest)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		job = &discoveryJob{started: time.Now(), country: country, cancel: cancel, loading: true, done: make(chan struct{})}
		a.discoveriesJobs[country] = job
		go a.findDiscoveries(ctx, job)
	}
	a.discoveriesMu.Unlock()
	job.mu.Lock()
	v := view{Country: country, Loading: job.loading, Message: job.message, Candidates: append([]candidateCard(nil), job.items...)}
	job.mu.Unlock()
	if len(v.Candidates) > 12 {
		v.Candidates = v.Candidates[:12]
	}
	for i := range v.Candidates {
		item := &v.Candidates[i]
		if saved, err := a.Store.ByUUID(item.Candidate.UUID); err == nil {
			local := a.stationCard(saved, view{Page: "/stations"})
			local.Health, local.Discovery = item.Health, true
			item.Managed = &local
		}
	}
	render(w, "discoveries", v)
}

func (a *App) findDiscoveries(ctx context.Context, job *discoveryJob) {
	defer job.cancel()
	defer func() { job.mu.Lock(); job.loading = false; job.mu.Unlock(); close(job.done) }()
	seen := map[string]bool{}
	for offset := 0; offset < catalogue.PageSize*5 && job.count() < 12 && ctx.Err() == nil; offset += catalogue.PageSize {
		result, err := a.Catalogue.Popular(ctx, job.country, offset)
		if err != nil {
			job.mu.Lock()
			if len(job.items) == 0 {
				job.message = "Popular stations are unavailable right now."
			}
			job.mu.Unlock()
			break
		}
		for start := 0; start < len(result.Stations) && job.count() < 12 && ctx.Err() == nil; start += 4 {
			end := start + 4
			if end > len(result.Stations) {
				end = len(result.Stations)
			}
			var group sync.WaitGroup
			for index := start; index < end; index++ {
				candidate := result.Stations[index]
				if seen[candidate.UUID] {
					continue
				}
				seen[candidate.UUID] = true
				group.Add(1)
				go func(rank int, candidate model.Candidate) {
					defer group.Done()
					station, err := a.Catalogue.Resolve(ctx, candidate.UUID)
					if err != nil {
						return
					}
					health, err := a.Relay.Probe(ctx, station)
					if err != nil || !health.Working {
						return
					}
					station.Codec, station.Bitrate, station.HLS = health.Codec, health.Bitrate, health.Adaptive != ""
					if _, err := delivery.PlayURL(a.Base, station, model.LegacyXML, health); err != nil {
						return
					}
					candidate.Codec, candidate.Bitrate = health.Codec, health.Bitrate
					job.mu.Lock()
					job.items = append(job.items, candidateCard{Rank: rank, Discovery: true, Candidate: candidate, Health: health})
					sort.Slice(job.items, func(i, j int) bool { return job.items[i].Rank < job.items[j].Rank })
					job.mu.Unlock()
				}(offset+index, candidate)
			}
			group.Wait()
		}
		if len(result.Stations) < catalogue.PageSize {
			break
		}
	}
}
