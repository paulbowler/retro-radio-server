// SPDX-License-Identifier: GPL-3.0-only
package web

import (
	"context"
	"errors"
	"net/http"
	"retroradio.local/server/internal/delivery"
	"retroradio.local/server/internal/model"
	"sync"
	"time"
)

// Each alternative is tested before it becomes part of the shared library.
func (a *App) admitChannel(ctx context.Context, channel model.Station) (model.Station, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	choices := delivery.RankedStreams(channel, model.LegacyXML, "")
	if len(choices) > 16 {
		choices = choices[:16]
	}
	type checked struct {
		s model.Station
		h model.Health
	}
	results := make([]checked, len(choices))
	slots := make(chan struct{}, 4)
	var group sync.WaitGroup
	for i, v := range choices {
		group.Add(1)
		go func(i int, v model.StreamVariant) {
			defer group.Done()
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-ctx.Done():
				return
			}
			stream := v.Station(channel)
			stream.ID = channel.ID
			stream.StreamID = ""
			health, err := a.Relay.Probe(ctx, stream)
			if err != nil || !health.Working {
				return
			}
			stream.Codec = health.Codec
			stream.Bitrate = health.Bitrate
			stream.HLS = health.Adaptive != ""
			if _, err = delivery.PlayURL(a.Base, stream, model.LegacyXML, health); err != nil {
				return
			}
			results[i] = checked{stream, health}
		}(i, v)
	}
	group.Wait()
	var saved model.Station
	for _, result := range results {
		if !result.h.Working {
			continue
		}
		stream, err := a.Store.SaveStation(result.s)
		if err != nil {
			return saved, err
		}
		result.h.StationID = stream.ID
		result.h.VariantID = stream.VariantID
		if err = a.Store.SaveHealth(result.h); err != nil {
			return saved, err
		}
		saved = stream
	}
	if saved.ID == "" {
		return saved, errors.New("no usable stream")
	}
	return a.Store.Station(saved.ID, false)
}
func (a *App) audioOptions(w http.ResponseWriter, r *http.Request) {
	if !a.form(w, r) {
		return
	}
	station, err := a.Store.Station(r.Form.Get("station"), false)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	device, err := a.Store.Device(r.Form.Get("device"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	variant := r.Form.Get("variant")
	if variant != "" {
		supported := false
		for _, v := range delivery.RankedStreams(station, device.Capabilities, "") {
			supported = supported || v.ID == variant
		}
		if !supported {
			http.Error(w, "Choose an audio option available for this radio.", 422)
			return
		}
	}
	if err = a.Store.SetPreferred(device.ID, station.ID, variant); err != nil {
		http.Error(w, "Couldn’t save audio option.", 503)
		return
	}
	if !partial(r) {
		http.Redirect(w, r, "/devices?device="+device.ID, 303)
		return
	}
	renderCard(w, a.stationCard(station, view{Selected: device.ID, Page: "/devices"}))
}

// Existing library channels acquire newly discovered alternatives without
// requiring the user to remove and re-add them. Failed alternatives are not saved.
func (a *App) enrichChannel(ctx context.Context, s model.Station) model.Station {
	if s.Source == "custom" {
		return s
	}
	a.enrichmentMu.Lock()
	if a.enrichmentAt == nil {
		a.enrichmentAt = map[string]time.Time{}
	}
	if time.Since(a.enrichmentAt[s.ID]) < 5*time.Minute {
		a.enrichmentMu.Unlock()
		return s
	}
	a.enrichmentAt[s.ID] = time.Now()
	a.enrichmentMu.Unlock()
	candidates, err := a.Store.CachedCandidates()
	if err != nil {
		return s
	}
	known := map[string]bool{}
	for _, v := range s.Variants {
		known[v.URL] = true
	}
	pending := []model.StreamVariant{}
	homepage := s.Homepage
	for _, c := range candidates {
		station := model.CandidateStation(c)
		match := model.SameChannel(s, station)
		for _, v := range s.Variants {
			match = match || model.SameChannel(v.Station(s), station)
		}
		if !match || known[station.URL] {
			continue
		}
		known[station.URL] = true
		if homepage == "" {
			homepage = station.Homepage
		}
		pending = append(pending, model.StreamVariant{ID: c.UUID, UUID: c.UUID, URL: station.URL, Codec: c.Codec, Bitrate: c.Bitrate, HLS: c.HLS != 0})
	}
	if len(pending) == 0 {
		return s
	}
	s.Homepage = homepage
	s.Variants = pending
	if saved, err := a.admitChannel(ctx, s); err == nil {
		return saved
	}
	if saved, err := a.Store.Station(s.ID, false); err == nil {
		return saved
	}
	return s
}
