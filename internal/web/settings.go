// SPDX-License-Identifier: GPL-3.0-only
package web

import (
	"net/http"
	"retroradio.local/server/internal/agentfm"
	"retroradio.local/server/internal/store"
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
	var voices []voiceOption
	for _, id := range agentfm.VoiceNames() {
		voices = append(voices, voiceOption{ID: id, Name: strings.ToUpper(id[:1]) + id[1:]})
	}
	render(w, "preferences-form", view{Settings: settings, Countries: countryOptions, AgentVoices: voices})
}
func (a *App) savePreferences(w http.ResponseWriter, r *http.Request) {
	if !a.form(w, r) {
		return
	}
	seconds, err := strconv.Atoi(r.Form.Get("buffer"))
	country := strings.ToUpper(r.Form.Get("country"))
	voice := a.Store.Settings().AgentVoice
	if r.Form.Has("agent_voice") {
		voice = r.Form.Get("agent_voice")
		if !agentfm.ValidVoice(voice) {
			http.Error(w, "Choose a listed Agent FM voice.", 400)
			return
		}
	}
	settings := store.Settings{AgentVoice: voice, BufferSeconds: seconds, AutoReconnect: r.Form.Get("reconnect") == "on", Quality: r.Form.Get("quality"), Country: country}
	if err != nil || seconds < 0 || seconds > 10 || (settings.Quality != "auto" && settings.Quality != "low") || (country != "" && !validCountry(country)) {
		http.Error(w, "Choose a buffer between 0 and 10 seconds and a listed country.", 400)
		return
	}
	if err = a.Store.SaveSettings(settings); err != nil {
		http.Error(w, "Couldn’t save these settings. Please try again.", 500)
		return
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
