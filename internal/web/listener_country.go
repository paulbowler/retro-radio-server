// SPDX-License-Identifier: GPL-3.0-only
package web

import (
	"net/http"
	"sort"
	"strings"
	"unicode"
)

type countryOption struct{ Code, Name string }

var countryOptions = func() []countryOption {
	names := map[string]string{}
	for name, code := range countryCodes {
		if len(name) <= 2 {
			continue
		}
		if current := names[code]; current == "" || name < current {
			names[code] = name
		}
	}
	names["GB"], names["US"] = "united kingdom", "united states"
	var options []countryOption
	for code, name := range names {
		words := strings.Fields(name)
		for i, word := range words {
			runes := []rune(word)
			runes[0] = unicode.ToUpper(runes[0])
			words[i] = string(runes)
		}
		options = append(options, countryOption{code, strings.Join(words, " ")})
	}
	sort.Slice(options, func(i, j int) bool { return options[i].Name < options[j].Name })
	return options
}()

func validCountry(code string) bool {
	code = strings.ToUpper(code)
	for _, option := range countryOptions {
		if option.Code == code {
			return true
		}
	}
	return false
}
func countryName(code string) string {
	for _, option := range countryOptions {
		if option.Code == code {
			return option.Name
		}
	}
	return code
}

// Regional settings suggest a country; a remembered selection takes precedence.
func listenerCountry(r *http.Request) (string, bool) {
	if code := strings.ToUpper(r.URL.Query().Get("country")); validCountry(code) {
		return code, false
	}
	if cookie, err := r.Cookie("retro_country"); err == nil && validCountry(cookie.Value) {
		return strings.ToUpper(cookie.Value), false
	}
	for _, entry := range strings.Split(r.Header.Get("Accept-Language"), ",") {
		tag := strings.Split(strings.TrimSpace(entry), ";")[0]
		for _, part := range strings.Split(tag, "-")[1:] {
			if validCountry(part) {
				return strings.ToUpper(part), true
			}
		}
	}
	return "", true
}

func rememberCountry(w http.ResponseWriter, r *http.Request) {
	code := strings.ToUpper(r.URL.Query().Get("country"))
	if validCountry(code) {
		http.SetCookie(w, &http.Cookie{Name: "retro_country", Value: code, Path: "/", MaxAge: 31536000, HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteLaxMode})
	}
}
