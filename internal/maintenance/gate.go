// SPDX-License-Identifier: GPL-3.0-only
// Package maintenance drains work before a development database rebuild.
package maintenance

import (
	"context"
	"errors"
	"net/http"
	"sync"
)

var ErrBusy = errors.New("database maintenance is in progress")

type Gate struct {
	mu      sync.Mutex
	busy    bool
	jobs    map[*job]struct{}
	drained chan struct{}
}
type job struct{ cancel context.CancelFunc }

func (g *Gate) Enter(parent context.Context) (context.Context, func(), error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.busy {
		return nil, nil, ErrBusy
	}
	ctx, cancel := context.WithCancel(parent)
	j := &job{cancel: cancel}
	if g.jobs == nil {
		g.jobs = map[*job]struct{}{}
	}
	g.jobs[j] = struct{}{}
	var once sync.Once
	return ctx, func() {
		once.Do(func() {
			cancel()
			g.mu.Lock()
			defer g.mu.Unlock()
			delete(g.jobs, j)
			if g.busy && len(g.jobs) == 0 {
				close(g.drained)
			}
		})
	}, nil
}

// Begin rejects new work, cancels streams and workers, and waits for their cleanup.
// A timeout aborts maintenance without touching the database.
func (g *Gate) Begin(ctx context.Context) (func(), error) {
	g.mu.Lock()
	if g.busy {
		g.mu.Unlock()
		return nil, ErrBusy
	}
	g.busy = true
	g.drained = make(chan struct{})
	if len(g.jobs) == 0 {
		close(g.drained)
	}
	for j := range g.jobs {
		j.cancel()
	}
	drained := g.drained
	g.mu.Unlock()
	release := func() { g.mu.Lock(); g.busy = false; g.mu.Unlock() }
	select {
	case <-drained:
		return release, nil
	case <-ctx.Done():
		release()
		return nil, ctx.Err()
	}
}

func (g *Gate) Do(ctx context.Context, work func(context.Context)) {
	ctx, done, err := g.Enter(ctx)
	if err != nil {
		return
	}
	defer done()
	work(ctx)
}
func (g *Gate) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The management handler still applies authentication and form protection.
		if r.URL.Path == "/development/rebuild" {
			next.ServeHTTP(w, r)
			return
		}
		ctx, done, err := g.Enter(r.Context())
		if err != nil {
			w.Header().Set("Retry-After", "5")
			http.Error(w, ErrBusy.Error(), 503)
			return
		}
		defer done()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
