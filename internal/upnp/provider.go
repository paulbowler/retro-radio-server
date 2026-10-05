// SPDX-License-Identifier: GPL-3.0-only
// Package upnp adapts UPnP ContentDirectory servers, without indexing music.
package upnp

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"os/exec"
	"retroradio.local/server/internal/content"
	"retroradio.local/server/internal/model"
	"strconv"
	"strings"
	"sync"
	"time"
)

const MaxPage = 100

var ErrUnknownID = errors.New("unknown or expired music playback ID")

type token struct {
	object string
	at     time.Time
}
type Provider struct {
	sourceIP         netip.Addr // A discovered description may only resolve to its SSDP sender.
	DisableTranscode bool       // Native resources are still preferred when conversion is enabled.
	ffmpegPath       string
	conversionSlots  chan struct{}
	description      *url.URL
	init             chan struct{}
	scope            *scope
	client           *http.Client
	control, service string
	mu               sync.Mutex
	name             string
	tokens           map[string]token
	objects          map[string]string
	slots            chan struct{}
	allowLoopback    bool // Only package tests can enable this.
}

var _ content.Provider = (*Provider)(nil)

func New(raw string) (*Provider, error) {
	u, e := validURL(raw)
	if e != nil {
		return nil, e
	}
	ffmpeg, _ := exec.LookPath("ffmpeg")
	return &Provider{ffmpegPath: ffmpeg, conversionSlots: make(chan struct{}, 4), description: u, init: make(chan struct{}, 1), name: "MinimServer", tokens: map[string]token{}, objects: map[string]string{}, slots: make(chan struct{}, 16)}, nil
}
func (p *Provider) Name() string { p.mu.Lock(); defer p.mu.Unlock(); return p.name }
func readXML(res *http.Response) ([]byte, error) {
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("UPnP HTTP status %d", res.StatusCode)
	}
	b, e := io.ReadAll(io.LimitReader(res.Body, (4<<20)+1))
	if e != nil {
		return nil, e
	}
	if len(b) > 4<<20 {
		return nil, errors.New("UPnP XML exceeds limit")
	}
	return b, nil
}

// Initialization is lazy and retryable: an offline music server cannot prevent startup.
func (p *Provider) ensure(ctx context.Context) error {
	select {
	case p.init <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-p.init }()
	if p.control != "" {
		return nil
	}
	s, e := newScope(ctx, p.description, p.allowLoopback)
	if e != nil {
		return e
	}
	if p.sourceIP.IsValid() {
		for _, ip := range s.ips {
			if ip.Unmap() != p.sourceIP {
				return errors.New("discovered description is outside SSDP sender")
			}
		}
	}
	client := s.client()
	defer client.CloseIdleConnections()
	req, e := http.NewRequestWithContext(ctx, "GET", p.description.String(), nil)
	if e != nil {
		return e
	}
	res, e := client.Do(req)
	if e != nil {
		return e
	}
	b, e := readXML(res)
	if e != nil {
		return e
	}
	var desc struct {
		XMLName xml.Name `xml:"root"`
		Base    string   `xml:"URLBase"`
		Device  device   `xml:"device"`
	}
	if e = xml.Unmarshal(b, &desc); e != nil {
		return e
	}
	svc, ok := directory(desc.Device)
	if !ok {
		return errors.New("ContentDirectory service not found")
	}
	base := res.Request.URL
	if desc.Base != "" {
		base, e = url.Parse(strings.TrimSpace(desc.Base))
		if e != nil {
			return e
		}
		if e = s.check(base.String()); e != nil {
			return e
		}
	}
	rel, e := url.Parse(svc.Control)
	if e != nil {
		return e
	}
	control := base.ResolveReference(rel).String()
	if e = s.check(control); e != nil {
		return e
	}
	// These fields are immutable after initialization and accessed under init.
	p.scope = s
	p.client = client
	p.control = control
	p.service = svc.Type
	p.mu.Lock()
	if desc.Device.Name != "" {
		p.name = desc.Device.Name
	}
	p.mu.Unlock()
	return nil
}
func (p *Provider) browse(ctx context.Context, object, flag string, offset, count int) (content.Page, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if object == "" || len(object) > 2048 || offset < 0 || offset > 1000000 || count < 1 || count > MaxPage {
		return content.Page{}, errors.New("invalid music page")
	}
	if e := p.ensure(ctx); e != nil {
		return content.Page{}, e
	}
	var escaped bytes.Buffer
	xml.EscapeText(&escaped, []byte(object))
	body := `<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:Browse xmlns:u="` + p.service + `"><ObjectID>` + escaped.String() + `</ObjectID><BrowseFlag>` + flag + `</BrowseFlag><Filter>*</Filter><StartingIndex>` + strconv.Itoa(offset) + `</StartingIndex><RequestedCount>` + strconv.Itoa(count) + `</RequestedCount><SortCriteria></SortCriteria></u:Browse></s:Body></s:Envelope>`
	req, e := http.NewRequestWithContext(ctx, "POST", p.control, strings.NewReader(body))
	if e != nil {
		return content.Page{}, e
	}
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	req.Header.Set("SOAPAction", `"`+p.service+`#Browse"`)
	res, e := p.client.Do(req)
	if e != nil {
		return content.Page{}, e
	}
	b, e := readXML(res)
	if e != nil {
		return content.Page{}, e
	}
	var envelope struct {
		XMLName xml.Name `xml:"Envelope"`
		Body    struct {
			Response *struct {
				Result   *string `xml:"Result"`
				Returned *int    `xml:"NumberReturned"`
				Total    *int    `xml:"TotalMatches"`
				Update   string  `xml:"UpdateID"`
			} `xml:"BrowseResponse"`
		} `xml:"Body"`
	}
	if e = xml.Unmarshal(b, &envelope); e != nil {
		return content.Page{}, e
	}
	response := envelope.Body.Response
	if response == nil || response.Result == nil || response.Returned == nil || response.Total == nil {
		return content.Page{}, errors.New("invalid UPnP Browse response")
	}
	items, e := parseDIDL(*response.Result)
	if e != nil {
		return content.Page{}, e
	}
	if *response.Returned != len(items) || *response.Returned > count || *response.Total < 0 || *response.Total < *response.Returned {
		return content.Page{}, errors.New("inconsistent UPnP page counts")
	}
	for i := range items {
		if items[i].Kind == content.PlayableItem {
			items[i].PlaybackID = p.register(items[i].ID)
			if resource, e := p.SelectResource(items[i].Resources, model.LegacyXML); e == nil {
				items[i].PlaybackCodec = resource.Codec
				if resource.Codec == "FLAC" {
					items[i].PlaybackCodec = "MP3"
					items[i].Transcoded = true
				}
			}
		}
	}
	return content.Page{Items: items, Returned: *response.Returned, Total: *response.Total, UpdateID: response.Update}, nil
}
func (p *Provider) Browse(ctx context.Context, object string, offset, count int) (content.Page, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	page, e := p.browse(ctx, object, "BrowseDirectChildren", offset, count)
	if e != nil {
		return page, e
	}
	page.ParentID = "-1"
	page.Title = p.Name()
	if object != "0" {
		metadata, e := p.browse(ctx, object, "BrowseMetadata", 0, 1)
		if e != nil {
			return content.Page{}, e
		}
		if len(metadata.Items) != 1 || metadata.Items[0].ID != object || metadata.Items[0].Kind != content.Folder {
			return content.Page{}, errors.New("music folder unavailable")
		}
		page.ParentID = metadata.Items[0].ParentID
		page.Title = metadata.Items[0].Title
	}
	return page, nil
}
func (p *Provider) register(object string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	for id, t := range p.tokens {
		if now.Sub(t.at) > 24*time.Hour {
			delete(p.tokens, id)
			delete(p.objects, t.object)
		}
	}
	if id := p.objects[object]; id != "" {
		t := p.tokens[id]
		t.at = now
		p.tokens[id] = t
		return id
	}
	if len(p.tokens) >= 4096 {
		oldest := ""
		at := now
		for id, t := range p.tokens {
			if oldest == "" || t.at.Before(at) {
				oldest = id
				at = t.at
			}
		}
		delete(p.objects, p.tokens[oldest].object)
		delete(p.tokens, oldest)
	}
	var random [16]byte
	if _, e := rand.Read(random[:]); e != nil {
		return ""
	}
	id := "upnp_" + hex.EncodeToString(random[:])
	p.tokens[id] = token{object: object, at: now}
	p.objects[object] = id
	return id
}
func (p *Provider) Metadata(ctx context.Context, id string) (content.Item, error) {
	p.mu.Lock()
	t, ok := p.tokens[id]
	p.mu.Unlock()
	if !ok || time.Since(t.at) > 24*time.Hour {
		return content.Item{}, ErrUnknownID
	}
	page, e := p.browse(ctx, t.object, "BrowseMetadata", 0, 1)
	if e != nil {
		return content.Item{}, e
	}
	if len(page.Items) != 1 || page.Items[0].ID != t.object || page.Items[0].Kind != content.PlayableItem {
		return content.Item{}, errors.New("music item no longer available")
	}
	return page.Items[0], nil
}
func (p *Provider) Resolve(ctx context.Context, id string, caps model.Capabilities) (content.Playback, error) {
	item, e := p.Metadata(ctx, id)
	if e != nil {
		return content.Playback{}, e
	}
	resource, e := p.SelectResource(item.Resources, caps)
	if e != nil {
		return content.Playback{}, e
	}
	return content.Playback{Item: item, Resource: resource, Transcode: resource.Codec == "FLAC", Direct: resource.Codec != "FLAC" && caps.HTTPS && strings.HasPrefix(resource.URL, "https://")}, nil
}

// SelectResource considers all advertised alternatives; it never guesses from file extensions.
// A future format translator can sit here without changing provider browsing or radio XML.
func (p *Provider) SelectResource(resources []content.Resource, c model.Capabilities) (content.Resource, error) {
	for _, r := range resources {
		if r.Protocol != "http-get" || !c.HTTP || !((r.Codec == "MP3" && c.MP3) || (r.Codec == "AAC" && c.AAC)) {
			continue
		}
		if p.scope != nil && p.scope.check(r.URL) == nil {
			return r, nil
		}
	}
	if !p.DisableTranscode && p.ffmpegPath != "" && c.HTTP && c.MP3 {
		for _, r := range resources {
			if r.Protocol == "http-get" && r.Codec == "FLAC" && p.scope != nil && p.scope.check(r.URL) == nil {
				return r, nil
			}
		}
	}
	return content.Resource{}, errors.New("no compatible server-hosted resource (FLAC fallback requires FFmpeg)")
}

// HTTP and converted resources are relayed: redirects stay inside the server scope.
// This leaves source codecs unchanged and deliberately excludes the live-radio reconnect/buffer.
func (p *Provider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", 405)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/stream/upnp/")
	p.mu.Lock()
	_, known := p.tokens[id]
	p.mu.Unlock()
	if !known || !strings.HasPrefix(r.URL.Path, "/stream/upnp/") {
		http.NotFound(w, r)
		return
	}
	select {
	case p.slots <- struct{}{}:
		defer func() { <-p.slots }()
	default:
		http.Error(w, "music playback busy", 503)
		return
	}
	play, e := p.Resolve(r.Context(), id, model.LegacyXML)
	if e != nil {
		if errors.Is(e, ErrUnknownID) {
			http.NotFound(w, r)
		} else {
			http.Error(w, "music item unavailable or incompatible", 502)
		}
		return
	}
	if play.Transcode {
		p.serveConverted(w, r, play.Resource)
		return
	}
	req, e := http.NewRequestWithContext(r.Context(), r.Method, play.Resource.URL, nil)
	if e != nil {
		http.Error(w, "music resource unavailable", 502)
		return
	}
	for _, h := range []string{"Range", "If-Range"} {
		if v := r.Header.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}
	res, e := p.client.Do(req)
	if e != nil {
		http.Error(w, "music server unavailable", 502)
		return
	}
	defer res.Body.Close()
	if res.StatusCode != 200 && res.StatusCode != 206 && res.StatusCode != 416 {
		http.Error(w, "music resource unavailable", 502)
		return
	}
	// Refuse a server response which changed the selected resource's media type.
	mime := strings.ToLower(strings.TrimSpace(strings.Split(res.Header.Get("Content-Type"), ";")[0]))
	if res.StatusCode != 416 && mime != "" && mime != play.Resource.MIME && mime != "application/octet-stream" {
		http.Error(w, "music resource changed format", 502)
		return
	}
	for _, h := range []string{"Content-Length", "Content-Range", "Accept-Ranges", "ETag", "Last-Modified"} {
		if v := res.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.Header().Set("Content-Type", play.Resource.MIME)
	w.Header().Set("Cache-Control", "no-store")
	if res.StatusCode == 416 {
		w.Header().Set("Content-Length", "0")
	}
	w.WriteHeader(res.StatusCode)
	if r.Method == "HEAD" || res.StatusCode == 416 {
		return
	}
	rc := http.NewResponseController(w)
	defer rc.SetWriteDeadline(time.Time{})
	buf := make([]byte, 32<<10)
	for {
		n, e := res.Body.Read(buf)
		if n > 0 {
			_ = rc.SetWriteDeadline(time.Now().Add(20 * time.Second))
			if _, err := w.Write(buf[:n]); err != nil {
				return
			}
			_ = rc.Flush()
		}
		if e != nil {
			return
		}
	}
}
