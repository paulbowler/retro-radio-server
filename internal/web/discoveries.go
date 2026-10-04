// SPDX-License-Identifier: GPL-3.0-only
package web

import (
	"context"
	"fmt"
	"net/http"
	"retroradio.local/server/internal/catalogue"
	"retroradio.local/server/internal/delivery"
	"retroradio.local/server/internal/model"
	"sort"
	"strconv"
	"sync"
	"time"
)

type discoveryJob struct {
	mu                 sync.Mutex
	started            time.Time
	country            string
	offset, nextOffset int
	more               bool
	cancel             context.CancelFunc
	items              []candidateCard
	loading            bool
	message            string
	done               chan struct{}
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
	offset := 0
	if raw := r.URL.Query().Get("offset"); raw != "" {
		var err error
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 || offset > 1000 {
			http.Error(w, "Invalid page", 400)
			return
		}
	}
	trail, err := discoveryTrail(r.URL.Query().Get("trail"), offset)
	if err != nil {
		http.Error(w, "Invalid page history", 400)
		return
	}
	rememberCountry(w, r)

	a.discoveriesMu.Lock()
	if a.discoveriesJobs == nil {
		a.discoveriesJobs = map[string]*discoveryJob{}
	}
	key := country
	if offset > 0 {
		key += fmt.Sprintf(":%d", offset)
	}
	job := a.discoveriesJobs[key]
	if job == nil || time.Since(job.started) > 2*time.Minute {
		if job != nil {
			job.cancel()
		}
		if len(a.discoveriesJobs) >= 8 {
			var oldest string
			found := false
			for code, item := range a.discoveriesJobs {
				if !found || item.started.Before(a.discoveriesJobs[oldest].started) {
					oldest = code
					found = true
				}
			}
			a.discoveriesJobs[oldest].cancel()
			delete(a.discoveriesJobs, oldest)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		job = &discoveryJob{started: time.Now(), country: country, offset: offset, cancel: cancel, loading: true, done: make(chan struct{})}
		a.discoveriesJobs[key] = job
		go a.findDiscoveries(ctx, job)
	}
	a.discoveriesMu.Unlock()
	job.mu.Lock()
	v := view{Mode: "discover", Country: country, Offset: offset, Trail: cursorTrail(trail), PageNumber: len(trail) + 1, HasPrevious: offset > 0, HasNext: job.more && !job.loading, Next: job.nextOffset, Loading: job.loading, Message: job.message, Candidates: append([]candidateCard(nil), job.items...)}
	job.mu.Unlock()
	if len(v.Candidates) > catalogue.PageSize {
		v.Candidates = v.Candidates[:catalogue.PageSize]
	}
	v.Previous = 0
	previousTrail := ""
	if len(trail) > 0 {
		v.Previous = trail[len(trail)-1]
		previousTrail = cursorTrail(trail[:len(trail)-1])
	}
	if offset > 0 && len(trail) == 0 {
		v.PageNumber = 2
	}
	v.PreviousURL = stationPageURL(v, v.Previous, previousTrail)
	v.NextURL = stationPageURL(v, v.Next, cursorTrail(append(trail, offset)))
	v.DiscoveryURL = "/stations/discoveries?" + r.URL.Query().Encode()
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
	defer func() {
		job.mu.Lock()
		if len(job.items) >= catalogue.PageSize {
			job.nextOffset = job.items[catalogue.PageSize-1].Rank + 1
			job.more = job.more || len(job.items) > catalogue.PageSize
		}
		job.more = job.more && job.nextOffset <= 1000
		job.loading = false
		job.mu.Unlock()
		close(job.done)
	}()
	seen := map[string]bool{}
	for offset := job.offset; offset < job.offset+catalogue.PageSize*5 && offset <= 1000 && job.count() < catalogue.PageSize && ctx.Err() == nil; offset += catalogue.PageSize {
		result, err := a.Catalogue.Popular(ctx, job.country, offset)
		if err != nil {
			job.mu.Lock()
			if len(job.items) == 0 {
				job.message = "Popular stations are unavailable right now."
			}
			job.mu.Unlock()
			break
		}
		for start := 0; start < len(result.Stations) && job.count() < catalogue.PageSize && ctx.Err() == nil; start += 4 {
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
			job.mu.Lock()
			job.nextOffset = offset + end
			job.more = end < len(result.Stations) || len(result.Stations) == catalogue.PageSize
			job.mu.Unlock()
		}
		if len(result.Stations) < catalogue.PageSize {
			break
		}
	}
}
