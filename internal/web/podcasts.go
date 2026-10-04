// SPDX-License-Identifier: GPL-3.0-only
package web

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func (a *App) podcastView(r *http.Request, v *view) {
	var err error
	v.Podcasts, err = a.Store.Podcasts()
	if err != nil {
		v.Error = true
		v.Message = "Couldn’t load your podcasts. Please try again."
		return
	}
	v.Country, _ = listenerCountry(r)
	if v.Country == "" {
		v.Country = "GB"
	}
	if id := r.URL.Query().Get("podcast"); id != "" {
		p, e := a.Store.Podcast(id)
		if e != nil {
			v.Error = true
			v.Message = "This podcast is no longer in your library."
			return
		}
		v.Podcast = &p
		v.Title = p.Title
		episodes, e := a.Store.Episodes(id)
		if e != nil {
			v.Error = true
			v.Message = "Couldn’t load the episodes. Please try again."
			return
		}
		offset := 0
		if raw := r.URL.Query().Get("offset"); raw != "" {
			offset, e = strconv.Atoi(raw)
			if e != nil || offset < 0 || offset > 500 || offset%24 != 0 {
				v.Error = true
				v.Message = "This page is unavailable."
				return
			}
		}
		v.Offset = offset
		v.HasPrevious = offset > 0
		v.HasNext = offset+24 < len(episodes)
		v.PageNumber = offset/24 + 1
		v.PreviousURL = "/podcasts?podcast=" + url.QueryEscape(id) + "&offset=" + strconv.Itoa(max(0, offset-24))
		v.NextURL = "/podcasts?podcast=" + url.QueryEscape(id) + "&offset=" + strconv.Itoa(offset+24)
		if offset < len(episodes) {
			v.Episodes = episodes[offset:min(offset+24, len(episodes))]
		}
		return
	}
	if v.Query != "" {
		results, e := a.Podcasts.Search(r.Context(), v.Query, v.Country)
		if e != nil {
			v.Error = true
			v.Message = "Podcast search is unavailable right now. Try again later or add a podcast by its feed link."
			return
		}
		for i := range results {
			for _, p := range v.Podcasts {
				if p.Feed == results[i].Feed {
					results[i].Subscribed = true
					break
				}
			}
		}
		v.PodcastResults = results
	}
}
func (a *App) subscribePodcast(w http.ResponseWriter, r *http.Request) {
	if !a.form(w, r) {
		return
	}
	p, e := a.Podcasts.Subscribe(r.Context(), r.Form.Get("feed"))
	if e != nil {
		message := "We couldn’t open this podcast. Use a public RSS or Atom feed with MP3 or AAC episodes."
		if partial(r) {
			actionResult(w, false, "Couldn’t add podcast", message)
			w.WriteHeader(204)
		} else {
			http.Error(w, message, 422)
		}
		return
	}
	target := "/podcasts?podcast=" + url.QueryEscape(p.ID)
	if partial(r) {
		w.Header().Set("HX-Location", `{"path":"`+target+`","target":"#content"}`)
		actionResult(w, true, "Podcast added", p.Title+" is available in the Podcasts menu on all your radios.")
		w.WriteHeader(200)
		return
	}
	http.Redirect(w, r, target, 303)
}
func (a *App) removePodcast(w http.ResponseWriter, r *http.Request) {
	if !a.form(w, r) {
		return
	}
	if e := a.Store.DeletePodcast(strings.TrimSpace(r.Form.Get("podcast"))); e != nil {
		http.Error(w, "This podcast is no longer in your library.", 404)
		return
	}
	if partial(r) {
		w.Header().Set("HX-Location", `{"path":"/podcasts","target":"#content"}`)
		w.WriteHeader(200)
		return
	}
	http.Redirect(w, r, "/podcasts", 303)
}

func podcastDuration(raw string) string {
	parts := strings.Split(raw, ":")
	if len(parts) > 3 {
		return ""
	}
	seconds := 0
	for _, part := range parts {
		n, e := strconv.Atoi(part)
		if e != nil || n < 0 || n > 604800 {
			return ""
		}
		seconds = seconds*60 + n
	}
	if seconds <= 0 || seconds > 604800 {
		return ""
	}
	if seconds < 60 {
		return strconv.Itoa(seconds) + " sec"
	}
	minutes := (seconds + 30) / 60
	if minutes < 60 {
		return strconv.Itoa(minutes) + " min"
	}
	hours := minutes / 60
	minutes %= 60
	text := strconv.Itoa(hours) + " hr"
	if minutes > 0 {
		text += " " + strconv.Itoa(minutes) + " min"
	}
	return text
}
