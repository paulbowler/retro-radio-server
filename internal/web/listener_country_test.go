// SPDX-License-Identifier: GPL-3.0-only
package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestListenerCountryPreference(t *testing.T) {
	for _, test := range []struct {
		query, cookie, language, want string
		automatic                     bool
	}{
		{"GB", "FR", "en-US", "GB", false},
		{"", "FR", "en-US", "FR", false},
		{"", "", "en-AU,en;q=0.8", "AU", true},
		{"", "", "en", "", true},
		{"<invalid>", "GB", "en-US", "GB", false},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		q := r.URL.Query()
		q.Set("country", test.query)
		r.URL.RawQuery = q.Encode()
		r.Header.Set("Accept-Language", test.language)
		if test.cookie != "" {
			r.AddCookie(&http.Cookie{Name: "retro_country", Value: test.cookie})
		}
		got, automatic := listenerCountry(r)
		if got != test.want || automatic != test.automatic {
			t.Fatal(test, got, automatic)
		}
	}
}
func TestCountrySelectionIsRememberedAndJobsAreSeparate(t *testing.T) {
	gb := &discoveryJob{started: time.Now(), country: "GB", message: "British results"}
	de := &discoveryJob{started: time.Now(), country: "DE", message: "German results"}
	app := &App{discoveriesJobs: map[string]*discoveryJob{"GB": gb, "DE": de}}
	for _, code := range []string{"GB", "DE"} {
		w := httptest.NewRecorder()
		app.discoveries(w, httptest.NewRequest("GET", "/dashboard/discoveries?country="+code, nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), "Popular in "+countryName(code)) {
			t.Fatal(code, w.Code, w.Body.String())
		}
		cookies := w.Result().Cookies()
		if len(cookies) != 1 || cookies[0].Value != code || !cookies[0].HttpOnly || cookies[0].MaxAge <= 0 {
			t.Fatal("country not remembered", cookies)
		}
		if code == "DE" && strings.Contains(w.Body.String(), "British results") {
			t.Fatal("countries mixed")
		}
	}
	w := httptest.NewRecorder()
	app.discoveries(w, httptest.NewRequest("GET", "/dashboard/discoveries?country=invalid", nil))
	if w.Code != 400 {
		t.Fatal("invalid country accepted", w.Code)
	}
}
