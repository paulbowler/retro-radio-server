// SPDX-License-Identifier: GPL-3.0-only
// Package content defines a source-neutral hierarchical audio catalogue.
package content

import (
	"context"
	"retroradio.local/server/internal/model"
)

type Kind string

const (
	Folder       Kind = "folder"
	PlayableItem Kind = "playable"
)

type Resource struct {
	URL                   string
	Protocol, MIME, Codec string
	Bitrate               int // kbps (UPnP res bitrate is bytes/second)
}
type Item struct {
	ID, ParentID, Title, Source     string
	Artist, Album, ArtURL, Duration string
	Kind                            Kind
	Genre                           bool // A UPnP music-genre container.
	Resources                       []Resource
	// PlaybackID is an opaque source-issued token, never a client-supplied URL.
	PlaybackID    string
	PlaybackCodec string
	Transcoded    bool
}
type Page struct {
	Items           []Item
	Total, Returned int
	ParentID, Title string
	UpdateID        string
	Genre           bool // This folder represents one genre, not the genre index.
}
type Playback struct {
	Transcode bool // FLAC input converted to the established MP3 output profile.
	Direct    bool // Device can consume the selected HTTPS resource.
	Item      Item
	Resource  Resource
}
type Provider interface {
	Browse(context.Context, string, int, int) (Page, error)
	Resolve(context.Context, string, model.Capabilities) (Playback, error)
}

func (p Playback) Codec() string {
	if p.Transcode {
		return "MP3"
	}
	return p.Resource.Codec
}
func (p Playback) Bitrate() int {
	if p.Transcode {
		return 128
	}
	return p.Resource.Bitrate
}

// SequentialProvider adds server-controlled playback of one track-only folder.
// TrackList returns nil for higher-level/mixed folders; it never traverses them.
type SequentialProvider interface {
	TrackList(context.Context, string) ([]Item, error)
	StartSequence(context.Context, string, string, model.Capabilities) (Playback, string, error)
}

// AgentProvider adds an optional online DJ to the top-level radio menu.
type AgentProvider interface {
	AgentFMAvailable() bool
	StartAgentFM(context.Context, model.Capabilities) (Playback, string, error)
}

type GenreAgentProvider interface {
	StartGenreFM(context.Context, string, model.Capabilities) (Playback, string, error)
}
