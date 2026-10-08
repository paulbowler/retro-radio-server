// SPDX-License-Identifier: GPL-3.0-only
package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"html/template"
	"log"
	"net/http"
	"strings"
	"time"
)

func randomRebuildToken() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

var rebuildTemplate = template.Must(template.New("rebuild").Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Development database rebuild</title><link rel="stylesheet" href="/static/app.css"></head><body><main><h1>Development database rebuild</h1>
{{if .Result}}<p role="status">{{.Result}}</p>{{else}}<p><strong>Destructive development operation.</strong> All listening stops. Saved radios and their names, saved and custom stations, favourites, audio preferences, activity, listening history, podcast episodes, catalogue and artwork caches are deleted. Old music links and queues expire. Radios register again on their next connection.</p><p>Current app settings, environment configuration and secrets are retained. Podcast feed subscriptions are fetched again from their publishers. The current migrations and seed importer build a fresh database, then normal catalogue and UPnP discovery run. Unavailable sources are reported and can be retried. A private SQLite backup is kept beside the database.</p><form method="post" action="/development/rebuild"><input type="hidden" name="token" value="{{.Token}}"><label>Type REBUILD to confirm deletion <input name="confirm" required autocomplete="off" pattern="REBUILD"></label><button class="destructive" type="submit">Delete development data and rebuild</button></form>{{end}}<p><a href="/">Back to dashboard</a></p></main></body></html>`))

func (a *App) rebuildPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = rebuildTemplate.Execute(w, struct{ Token, Result string }{Token: a.rebuildToken})
}
func (a *App) rebuildDatabase(w http.ResponseWriter, r *http.Request) {
	if !a.form(w, r) {
		return
	}
	if r.Form.Get("confirm") != "REBUILD" || subtle.ConstantTimeCompare([]byte(r.Form.Get("token")), []byte(a.rebuildToken)) != 1 {
		http.Error(w, "Type REBUILD and use the development confirmation form.", 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	release, err := a.Maintenance.Begin(ctx)
	if err != nil {
		http.Error(w, "Could not stop all work; database was not changed. Try again.", 409)
		return
	}
	defer release()
	feeds, err := a.Store.Podcasts()
	if err != nil {
		http.Error(w, "Could not read subscriptions; database was not changed.", 500)
		return
	}
	backup, err := a.Store.Rebuild(ctx)
	if err != nil {
		log.Printf("Development rebuild failed (backup %s): %v", backup, err)
		http.Error(w, "Database rebuild failed; the original database was retained. See server logs.", 500)
		return
	}
	log.Printf("Development database rebuilt; backup: %s", backup)
	a.discoveriesMu.Lock()
	a.discoveriesJobs = nil
	a.discoveriesMu.Unlock()
	a.enrichmentMu.Lock()
	a.enrichmentAt = nil
	a.enrichmentMu.Unlock()
	a.Relay.ClearPlaybackState()
	a.Podcasts.ClearSearchCache()
	a.Catalogue.ClearDiscoveryCache()
	var warnings []string
	for _, feed := range feeds {
		// Preserve the feed identity even when the publisher is offline, so its
		// normal hourly refresh can retry without resurrecting stale episode data.
		feed.ID = ""
		if _, err := a.Store.SavePodcast(feed, nil); err != nil {
			warnings = append(warnings, "Could not retain a podcast subscription.")
			continue
		}
		if _, err := a.Podcasts.Subscribe(ctx, feed.Feed); err != nil {
			warnings = append(warnings, "A podcast publisher was unavailable; its subscription remains and episodes will retry later.")
		}
	}
	if a.RebuildSources != nil {
		warnings = append(warnings, a.RebuildSources(ctx)...)
	}
	result := "Database rebuilt. Open music menus again and reconnect listeners."
	if len(warnings) > 0 {
		result += " Some sources need attention: " + strings.Join(warnings, " ")
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = rebuildTemplate.Execute(w, struct{ Token, Result string }{Result: result})
}

// Register detached UI work before launching it, so maintenance cannot race a
// late catalogue/artwork write into the newly built database.
func (a *App) background(parent context.Context, work func(context.Context)) bool {
	if a.Maintenance == nil {
		go work(parent)
		return true
	}
	ctx, done, err := a.Maintenance.Enter(parent)
	if err != nil {
		return false
	}
	go func() { defer done(); work(ctx) }()
	return true
}
