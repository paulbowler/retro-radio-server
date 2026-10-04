// SPDX-License-Identifier: GPL-3.0-only
package model

import "testing"

func TestChannelIdentityRetainsProgrammes(t *testing.T) {
	base := Station{Name: "BBC Radio 1", Country: "United Kingdom", Homepage: "https://www.bbc.co.uk/radio1", URL: "https://stream.example/one", Source: "radio-browser"}
	cases := []struct {
		name, country, homepage, raw, source string
		merge                                bool
	}{
		{"BBC Radio 1 (AAC 128kbps)", base.Country, "https://www.bbc.co.uk/sounds/play/live:bbc_radio_one", "https://other.example/aac", "radio-browser", true},
		{" BBC Radio 1 - MP3 · 320 kbps", base.Country, "", "https://stream.example/one-mp3", "radio-browser", true},
		{"BBC Radio 1 Dance (MP3)", base.Country, base.Homepage, base.URL, "radio-browser", false},
		{"BBC Radio 1", "Australia", base.Homepage, base.URL, "radio-browser", false},
		{"BBC Radio 1", base.Country, "", "https://unrelated.example/audio", "radio-browser", false},
		{"BBC Radio 1", base.Country, base.Homepage, base.URL, "custom", false},
	}
	for _, c := range cases {
		other := Station{Name: c.name, Country: c.country, Homepage: c.homepage, URL: c.raw, Source: c.source}
		if got := SameChannel(base, other); got != c.merge {
			t.Errorf("%s: merge=%t", c.name, got)
		}
	}
}

func TestDirectoryCountryAndLanguageAliases(t *testing.T) {
	a := Station{Name: "BBC Radio 2 (128k)", Country: "The United Kingdom Of Great Britain And Northern Ireland", Homepage: "https://www.bbc.co.uk/radio2", Language: "english"}
	b := Station{Name: "BBC Radio 2", Country: "United Kingdom", Homepage: "https://www.bbc.co.uk/sounds/play/live/bbc_radio_two", Language: "british english,english"}
	if !SameChannel(a, b) {
		t.Fatal("directory aliases split channel")
	}
	b.Language = "arabic"
	if SameChannel(a, b) {
		t.Fatal("different language merged")
	}
}
