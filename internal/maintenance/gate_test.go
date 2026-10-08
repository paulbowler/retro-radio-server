package maintenance

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMaintenanceCancelsAndDrainsBeforeReset(t *testing.T) {
	g := &Gate{}
	ctx, done, err := g.Enter(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() { <-ctx.Done(); done(); close(released) }()
	timeout, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	end, err := g.Begin(timeout)
	if err != nil {
		t.Fatal(err)
	}
	<-released
	if _, _, err := g.Enter(context.Background()); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	if _, err := g.Begin(timeout); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	g.Handler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("request admitted") })).ServeHTTP(w, httptest.NewRequest("GET", "/stream/test", nil))
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
	end()
	_, finish, err := g.Enter(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	finish()
}
func TestDrainTimeoutDoesNotStartRebuild(t *testing.T) {
	g := &Gate{}
	_, done, _ := g.Enter(context.Background())
	defer done()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if end, err := g.Begin(ctx); !errors.Is(err, context.Canceled) || end != nil {
		t.Fatal("unexpected maintenance result", err)
	}
	_, finish, err := g.Enter(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	finish()
}
