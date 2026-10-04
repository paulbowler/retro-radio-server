// SPDX-License-Identifier: GPL-3.0-only
package delivery

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"retroradio.local/server/internal/model"
	"retroradio.local/server/internal/store"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLowDataRanking(t *testing.T) {
	station := model.Station{Variants: []model.StreamVariant{{ID: "high", URL: "https://audio.example/high", Codec: "MP3", Bitrate: 320}, {ID: "low", URL: "https://audio.example/low", Codec: "AAC", Bitrate: 64}, {ID: "unknown", URL: "https://audio.example/unknown", Codec: "MP3"}}}
	if choices := RankedStreams(station, model.LegacyXML, "low-data"); choices[0].ID != "low" {
		t.Fatal(choices)
	}
	station.Variants[1].Health = model.Health{Checked: time.Now(), Working: false}
	if choices := RankedStreams(station, model.LegacyXML, "low-data"); choices[0].ID != "high" {
		t.Fatal("failed stream preferred", choices)
	}
}

type failedLiveBody struct{ sent bool }

func (b *failedLiveBody) Read(p []byte) (int, error) {
	if b.sent {
		return 0, io.ErrUnexpectedEOF
	}
	b.sent = true
	return copy(p, []byte("first")), io.ErrUnexpectedEOF
}
func (b *failedLiveBody) Close() error { return nil }
func TestLiveReconnectPreservesBufferedAudio(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reopened := make(chan struct{}, 1)
	chunks := liveChunks(ctx, &failedLiveBody{}, http.Header{}, store.Settings{AutoReconnect: true}, 128, func(ctx context.Context) (io.ReadCloser, http.Header, error) {
		reopened <- struct{}{}
		cancelAfter := io.NopCloser(strings.NewReader("second"))
		return cancelAfter, http.Header{}, nil
	})
	var got string
	for len(got) < 11 {
		select {
		case chunk := <-chunks:
			got += string(chunk.audio)
		case <-time.After(3 * time.Second):
			t.Fatal("reconnect failed", got)
		}
	}
	cancel()
	for range chunks {
	}
	if got != "firstsecond" {
		t.Fatal(got)
	}
	select {
	case <-reopened:
	default:
		t.Fatal("source was not reopened")
	}
}
func TestICYReframingAcrossReconnectIntervals(t *testing.T) {
	for _, interval := range []int{4, 8} {
		input := bytes.Repeat([]byte("A"), interval)
		input = append(input, 1)
		input = append(input, []byte("StreamTitle='x';")...)
		input = append(input, bytes.Repeat([]byte("B"), interval)...)
		title := ""
		observer := newICYObserver(map[int]string{4: "4", 8: "8"}[interval], func(s string) { title = s })
		audio := observer.Process(input, true)
		if len(audio) != interval*2 || title != "x" {
			t.Fatal("upstream metadata leaked into audio")
		}
	}
	var output bytes.Buffer
	encoder := icyOutput{writer: &output, remaining: 8192}
	if err := encoder.write(liveChunk{audio: bytes.Repeat([]byte("A"), 8190), title: "First"}); err != nil {
		t.Fatal(err)
	}
	if err := encoder.write(liveChunk{audio: bytes.Repeat([]byte("B"), 12), title: "Second"}); err != nil {
		t.Fatal(err)
	}
	var title string
	decoded := newICYObserver("8192", func(s string) { title = s }).Process(output.Bytes(), true)
	if len(decoded) != 8202 || title != "Second" {
		t.Fatal(len(decoded), title)
	}
}

type blockingLiveBody struct {
	once   sync.Once
	closed chan struct{}
	prefix []byte
}

func (b *blockingLiveBody) Read(p []byte) (int, error) {
	if len(b.prefix) > 0 {
		n := copy(p, b.prefix)
		b.prefix = b.prefix[n:]
		return n, nil
	}
	<-b.closed
	return 0, io.EOF
}
func (b *blockingLiveBody) Close() error { b.once.Do(func() { close(b.closed) }); return nil }
func TestLiveBufferPrimingPacingAndCancellation(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "buffer.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.DB.Close()
	p := New(db)
	source := &blockingLiveBody{closed: make(chan struct{}), prefix: bytes.Repeat([]byte("A"), 1024)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(ready)
		p.serveBufferedLive(w, r, model.Station{ID: "1001", Name: "Test", Codec: "MP3", Bitrate: 8}, source, http.Header{"Content-Type": {"audio/mpeg"}}, store.Settings{BufferSeconds: 1}, nil)
	}))
	defer server.Close()
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL, nil)
	res, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	<-ready
	first := make([]byte, 500)
	if _, err = io.ReadFull(res.Body, first); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	cancel()
	res.Body.Close()
	select {
	case <-source.closed:
	case <-time.After(time.Second):
		t.Fatal("source was not cancelled")
	}
	if time.Since(started) > time.Second {
		t.Fatal("buffer slowed cancellation")
	}
}
func TestReconnectRejectsChangedCodecAndPrivateTargets(t *testing.T) {
	p := &Relay{Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"audio/aac"}}, Body: io.NopCloser(strings.NewReader("aac")), Request: r}, nil
	})}}
	if _, _, err := p.reopenLive(model.Station{URL: "https://audio.example/live", Codec: "MP3"})(context.Background()); err == nil {
		t.Fatal("codec changed mid-stream")
	}
	if _, _, err := p.reopenLive(model.Station{URL: "http://127.0.0.1/live", Codec: "MP3"})(context.Background()); err == nil {
		t.Fatal("private source allowed")
	}
}

func TestLiveBufferWaitsForAudioReservoir(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "prime.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.DB.Close()
	p := New(db)
	reader, writer := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.serveBufferedLive(w, r, model.Station{ID: "1001", Codec: "MP3", Bitrate: 8}, reader, http.Header{"Content-Type": {"audio/mpeg"}}, store.Settings{BufferSeconds: 1}, nil)
	}))
	defer server.Close()
	go writer.Write(bytes.Repeat([]byte("A"), 500))
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL, nil)
	res, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	heard := make(chan error, 1)
	go func() { _, err := io.ReadFull(res.Body, make([]byte, 500)); heard <- err }()
	select {
	case err := <-heard:
		t.Fatal("audio escaped before reservoir filled", err)
	case <-time.After(50 * time.Millisecond):
	}
	go writer.Write(bytes.Repeat([]byte("B"), 524))
	select {
	case err := <-heard:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("filled buffer did not start")
	}
	cancel()
	writer.Close()
}

func TestReconnectResetsUpstreamMetadataFraming(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := append([]byte("AAAA\x01"), []byte("StreamTitle='x';")...)
	first = append(first, []byte("BBBB")...)
	second := append([]byte("CCCCCCCC\x01"), []byte("StreamTitle='y';")...)
	second = append(second, []byte("DDDDDDDD")...)
	chunks := liveChunks(ctx, io.NopCloser(bytes.NewReader(first)), http.Header{"Icy-MetaInt": {"4"}}, store.Settings{AutoReconnect: true}, 128, func(context.Context) (io.ReadCloser, http.Header, error) {
		return io.NopCloser(bytes.NewReader(second)), http.Header{"Icy-MetaInt": {"8"}}, nil
	})
	var audio string
	var titles []string
	for len(titles) < 2 {
		select {
		case chunk := <-chunks:
			audio += string(chunk.audio)
			titles = append(titles, chunk.title)
		case <-time.After(3 * time.Second):
			t.Fatal("metadata reconnect timed out")
		}
	}
	cancel()
	for range chunks {
	}
	if audio != "AAAABBBBCCCCCCCCDDDDDDDD" || titles[0] != "x" || titles[1] != "y" {
		t.Fatal(audio, titles)
	}
}
