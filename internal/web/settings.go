// SPDX-License-Identifier: GPL-3.0-only
package web

import (
	"net/http"
	"retroradio.local/server/internal/store"
	"strconv"
	"strings"
)

func (a *App) preferences(w http.ResponseWriter, r *http.Request) {
	render(w, "preferences-form", view{Settings: a.Store.Settings(), Countries: countryOptions})
}
func (a *App) savePreferences(w http.ResponseWriter, r *http.Request) {
	if !a.form(w, r) {
		return
	}
	seconds, err := strconv.Atoi(r.Form.Get("buffer"))
	country := strings.ToUpper(r.Form.Get("country"))
	settings := store.Settings{BufferSeconds: seconds, AutoReconnect: r.Form.Get("reconnect") == "on", Quality: r.Form.Get("quality"), Country: country}
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
