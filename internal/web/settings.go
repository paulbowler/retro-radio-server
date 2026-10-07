// SPDX-License-Identifier: GPL-3.0-only
package web

import (
	"net/http"
	"retroradio.local/server/internal/agentfm"
	"strconv"
	"strings"
)

func (a *App) preferences(w http.ResponseWriter, r *http.Request) {
	settings := a.Store.Settings()
	if settings.AgentVoice == "" {
		settings.AgentVoice = a.AgentVoiceDefault
		if settings.AgentVoice == "" {
			settings.AgentVoice = "ballad"
		}
	}
	if settings.AgentVolume == 0 {
		settings.AgentVolume = 100
	}
	groups := []voiceGroup{{Name: "Male voices"}, {Name: "Female voices"}, {Name: "Neutral voice"}}
	for _, id := range agentfm.VoiceNames() {
		group := 0
		switch id {
		case "coral", "marin", "nova", "sage", "shimmer":
			group = 1
		case "alloy":
			group = 2
		}
		groups[group].Voices = append(groups[group].Voices, voiceOption{ID: id, Name: strings.ToUpper(id[:1]) + id[1:]})
	}
	localStatus := "Local updates off"
	var items []agentfm.LocalItem
	if a.AgentLocalStatus != nil {
		localStatus = a.AgentLocalStatus()
	}
	if a.AgentLocalItems != nil {
		items = a.AgentLocalItems()
	}
	render(w, "preferences-form", view{AgentLocalStatus: localStatus, LocalBulletins: items, Settings: settings, Countries: countryOptions, AgentVoiceGroups: groups})
}
func (a *App) savePreferences(w http.ResponseWriter, r *http.Request) {
	if !a.form(w, r) {
		return
	}
	seconds, err := strconv.Atoi(r.Form.Get("buffer"))
	country := strings.ToUpper(r.Form.Get("country"))
	voice := a.Store.Settings().AgentVoice
	volume := a.Store.Settings().AgentVolume
	if r.Form.Has("agent_volume") {
		var volumeErr error
		volume, volumeErr = strconv.Atoi(r.Form.Get("agent_volume"))
		if volumeErr != nil || volume < 25 || volume > 400 {
			http.Error(w, "Choose a DJ voice volume between 25% and 400%.", 400)
			return
		}
	}
	if r.Form.Has("agent_voice") {
		voice = r.Form.Get("agent_voice")
		if !agentfm.ValidVoice(voice) {
			http.Error(w, "Choose a listed Agent FM voice.", 400)
			return
		}
	}
	settings := a.Store.Settings()
	settings.AgentVoice, settings.AgentVolume = voice, volume
	settings.BufferSeconds, settings.AutoReconnect = seconds, r.Form.Get("reconnect") == "on"
	settings.Quality, settings.Country = r.Form.Get("quality"), country
	if r.Form.Has("agent_local_settings") {
		settings.AgentLocation = strings.TrimSpace(r.Form.Get("agent_location"))
		settings.AgentLocalSources = strings.TrimSpace(r.Form.Get("agent_local_sources"))
		settings.AgentLocalEnabled = r.Form.Get("agent_local_enabled") == "on"
		settings.AgentAutoLocation = r.Form.Get("agent_auto_location") == "on"
		if len([]rune(settings.AgentLocation)) > 200 || !agentfm.ValidLocalSources(settings.AgentLocalSources) {
			http.Error(w, "Enter a location of up to 200 characters and up to five public HTTPS source addresses.", 400)
			return
		}
	}
	if err != nil || seconds < 0 || seconds > 10 || (settings.Quality != "auto" && settings.Quality != "low") || (country != "" && !validCountry(country)) {
		http.Error(w, "Choose a buffer between 0 and 10 seconds and a listed country.", 400)
		return
	}
	if err = a.Store.SaveSettings(settings); err != nil {
		http.Error(w, "Couldn’t save these settings. Please try again.", 500)
		return
	}
	if a.AgentLocalReset != nil {
		a.AgentLocalReset()
	}
	http.SetCookie(w, &http.Cookie{Name: "retro_country", Value: country, Path: "/", MaxAge: -1, HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteLaxMode})
	w.WriteHeader(http.StatusNoContent)
}
func (a *App) listenerCountry(r *http.Request) (string, bool) {
	if r.URL.Query().Has("country") {
		return listenerCountry(r)
	}
	if country := a.Store.Settings().Country; validCountry(country) {
		return country, false
	}
	return listenerCountry(r)
}
