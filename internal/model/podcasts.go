// SPDX-License-Identifier: GPL-3.0-only
package model

import "time"

type Podcast struct {
	ID          string    `json:"id"`
	Feed        string    `json:"-"`
	Title       string    `json:"title"`
	Author      string    `json:"author"`
	Description string    `json:"description"`
	Updated     time.Time `json:"updated"`
	Episodes    int       `json:"episodes"`
}
type Episode struct {
	ID          string    `json:"id"`
	PodcastID   string    `json:"podcast_id"`
	GUID        string    `json:"-"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	URL         string    `json:"-"`
	Codec       string    `json:"codec"`
	Published   time.Time `json:"published"`
	Duration    string    `json:"duration"`
}
