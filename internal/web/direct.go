// SPDX-License-Identifier: GPL-3.0-only
package web

import "net/http"

func (a *App) stationDelivery(w http.ResponseWriter, r *http.Request) {
	if !a.form(w, r) {
		return
	}
	station, err := a.Store.Station(r.Form.Get("station"), false)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	mode := r.Form.Get("delivery")
	if mode != "direct" && mode != "proxy" && mode != "" {
		http.Error(w, "Choose a connection method.", 422)
		return
	}
	if err = a.Store.SetStationDirect(station.ID, mode == "direct"); err != nil {
		http.Error(w, "Couldn’t save connection method.", 503)
		return
	}
	station.Direct = mode == "direct"
	context := "/stations"
	if r.Form.Get("context") == "/devices" {
		context = "/devices"
	}
	if !partial(r) {
		http.Redirect(w, r, context, 303)
		return
	}
	renderCard(w, a.stationCard(station, view{Page: context, Selected: r.Form.Get("device")}))
}
