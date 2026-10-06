// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"retroradio.local/server/internal/catalogue"
	"retroradio.local/server/internal/delivery"
	"retroradio.local/server/internal/podcast"
	"retroradio.local/server/internal/protocol/frontierxml"
	"retroradio.local/server/internal/store"
	"retroradio.local/server/internal/upnp"
	"retroradio.local/server/internal/web"
	"strings"
	"syscall"
	"time"
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
func main() {
	discoveryHelper := flag.Bool("upnp-discovery-helper", false, "discover LAN music servers for the application container")
	health := flag.Bool("healthcheck", false, "check the running server")
	flag.Parse()
	if *discoveryHelper {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		upnp.RunDiscoveryWriter(ctx, env("RETRO_UPNP_DISCOVERY_FILE", "/run/retro-upnp/discovery.json"))
		return
	}
	if *health {
		c := http.Client{Timeout: 3 * time.Second}
		r, e := c.Get(env("RETRO_HEALTH_URL", "http://127.0.0.1:8080/healthz"))
		if e != nil {
			os.Exit(1)
		}
		r.Body.Close()
		if r.StatusCode != 200 {
			os.Exit(1)
		}
		return
	}
	base := strings.TrimSuffix(env("RETRO_PUBLIC_URL", "http://pure.wifiradiofrontier.com"), "/")
	if e := frontierxml.ValidateBase(base); e != nil {
		log.Fatal(e)
	}
	path := env("RETRO_DB", "data/retro-radio.db")
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		log.Fatal(e)
	}
	s, e := store.Open(path)
	if e != nil {
		log.Fatal(e)
	}
	defer s.DB.Close()
	relay := delivery.New(s)
	cat := catalogue.New(s)
	if mirror := os.Getenv("RETRO_CATALOGUE_URL"); mirror != "" {
		if e := delivery.ValidateURL(mirror); e != nil {
			log.Fatal(e)
		}
		cat.Mirrors = []string{mirror}
	}
	podcasts := podcast.New(s)
	music, e := upnp.NewManager(os.Getenv("RETRO_MUSIC_SERVER_URL"))
	if e != nil {
		log.Printf("Manual music configuration ignored; automatic discovery enabled: %v", e)
	}
	music.SetPlaybackObserver(relay.TrackMusic)
	music.SetTranscoding(os.Getenv("RETRO_MUSIC_TRANSCODE") != "false")
	music.UseDiscoveryFile(os.Getenv("RETRO_UPNP_DISCOVERY_FILE"))

	app := &web.App{Music: music, Podcasts: podcasts, Catalogue: cat, Store: s, Relay: relay, Base: base, User: env("RETRO_ADMIN_USER", "admin"), Password: os.Getenv("RETRO_ADMIN_PASSWORD")}
	mux := http.NewServeMux()
	mux.Handle("/setupapp/", &frontierxml.Handler{Store: s, Base: base, Music: music, SupportsHTTPS: os.Getenv("RETRO_RADIO_HTTPS") == "true"})
	mux.Handle("/stream/", relay)
	mux.Handle("/stream/upnp/", music)
	mux.HandleFunc("/stream/upnp-queue/", music.ServeQueue)
	mux.HandleFunc("/artwork/upnp/", music.ServeArtwork)
	mux.HandleFunc("/episode/", relay.ServeEpisode)
	mux.HandleFunc("/artwork/", app.ServeRadioArtwork)
	mux.Handle("/", app.Handler())
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if e := s.DB.PingContext(ctx); e != nil {
			http.Error(w, "database unavailable", 503)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("ok\n"))
	})
	srv := &http.Server{Addr: env("RETRO_LISTEN", ":8080"), Handler: mux, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go podcasts.Run(ctx)
	go music.Run(ctx)
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if e := srv.Shutdown(shutdown); e != nil {
			srv.Close()
		}
	}()
	log.Printf("Retro Radio listening on %s; radio playback origin %s", srv.Addr, base)
	if app.Password == "" {
		log.Print("Management authentication disabled: trusted LAN deployment")
	}
	if e := srv.ListenAndServe(); e != nil && e != http.ErrServerClosed {
		log.Fatal(e)
	}
}
