// SPDX-License-Identifier: GPL-3.0-only
package agentfm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOnlineChoiceAndSpeech(t *testing.T) {
	var calls []string
	selected, expectedVoice := "", "ballad"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Error("missing authentication")
		}
		var p map[string]any
		if json.NewDecoder(r.Body).Decode(&p) != nil {
			t.Error("invalid request")
		}
		switch r.URL.Path {
		case "/responses":
			if p["store"] != false || p["model"] != "gpt-6.1-sol" || p["max_output_tokens"] != float64(2048) || p["reasoning"].(map[string]any)["effort"] != "low" {
				t.Error(p)
			}
			if !strings.Contains(p["input"].(string), "Next artist") || !strings.Contains(p["instructions"].(string), "untrusted") {
				t.Error(p)
			}
			format := p["text"].(map[string]any)["format"].(map[string]any)
			if format["strict"] != true || format["type"] != "json_schema" {
				t.Error(format)
			}
			fmt.Fprint(w, `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"{\"index\":1,\"chat\":\"Next artist, with Next song.\"}"}]}]}`)
		case "/audio/speech":
			if p["voice"] != expectedVoice || p["response_format"] != "mp3" || p["input"] != "That's Previous. Up next, Next artist with Next song." || !strings.Contains(p["instructions"].(string), "British") {
				t.Error(p)
			}
			w.Write([]byte("downloaded-MP3"))
		default:
			t.Error(r.URL.Path)
		}
	}))
	defer server.Close()
	c := New(Config{Key: "test-secret", VoiceSelection: func() string { return selected }})
	c.base = server.URL
	segment, e := c.Prepare(context.Background(), Track{Title: "Previous"}, []Track{{Title: "Other"}, {Title: "Next song", Artist: "Next artist"}})
	if e != nil || segment.Index != 1 || string(segment.Audio) != "downloaded-MP3" || strings.Join(calls, ",") != "/responses,/audio/speech" {
		t.Fatal(segment, e, calls)
	}
	selected, expectedVoice = "fable", "fable"
	if _, e = c.Prepare(context.Background(), Track{Title: "Previous"}, []Track{{Title: "Other"}, {Title: "Next song", Artist: "Next artist"}}); e != nil {
		t.Fatal(e)
	}
	selected, expectedVoice = "invalid", "ballad"
	if _, e = c.Prepare(context.Background(), Track{Title: "Previous"}, []Track{{Title: "Other"}, {Title: "Next song", Artist: "Next artist"}}); e != nil {
		t.Fatal(e)
	}
	if New(Config{}) != nil {
		t.Fatal("enabled without credentials")
	}
}
func TestInvalidChoiceAndSpeechFailure(t *testing.T) {
	for _, kind := range []string{"out-of-range", "empty-chat", "long-chat", "incomplete", "speech-error", "large-audio", "selection-error"} {
		t.Run(kind, func(t *testing.T) {
			speech := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/audio/speech" {
					speech = true
					if kind == "speech-error" {
						http.Error(w, "test-secret", 429)
					} else {
						w.Write(bytes.Repeat([]byte{'x'}, MaxAudio+1))
					}
					return
				}
				if kind == "selection-error" {
					http.Error(w, "test-secret", 401)
					return
				}
				choice := map[string]any{"index": 1, "chat": "Next artist with Next song."}
				status := "completed"
				switch kind {
				case "out-of-range":
					choice["index"] = 9
				case "empty-chat":
					choice["chat"] = ""
				case "long-chat":
					choice["chat"] = strings.Repeat("word ", 120)
				case "incomplete":
					status = "incomplete"
				}
				text, _ := json.Marshal(choice)
				json.NewEncoder(w).Encode(map[string]any{"status": status, "output": []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": string(text)}}}}})
			}))
			defer server.Close()
			c := New(Config{Key: "test-secret"})
			c.base = server.URL
			s, e := c.Prepare(context.Background(), Track{}, []Track{{}, {}})
			if e == nil || strings.Contains(e.Error(), "test-secret") {
				t.Fatal(e)
			}
			if kind == "speech-error" || kind == "large-audio" {
				if !speech || s.Index != 1 || len(s.Audio) != 0 {
					t.Fatal(s)
				}
			} else if speech {
				t.Fatal("invalid choice sent to speech API")
			}
		})
	}
}
func TestPreparationCancellation(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release }))
	defer server.Close()
	defer close(release)
	c := New(Config{Key: "test-secret"})
	c.base = server.URL
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, e := c.Prepare(ctx, Track{}, []Track{{}}); done <- e }()
	<-entered
	cancel()
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("ignored cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("online call did not cancel")
	}
}

func TestEditorialContextSentToOnlineDJ(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/audio/speech" {
			w.Write([]byte("MP3"))
			return
		}
		var request struct {
			Input string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		var input struct {
			Current    Track   `json:"just_played"`
			Candidates []Track `json:"candidates"`
		}
		if err := json.Unmarshal([]byte(request.Input), &input); err != nil {
			t.Error(err)
			return
		}
		if len(input.Current.Recent) != 5 || input.Current.Recent[0].Title != "b" || len(input.Current.Recent[0].Recent) != 0 || len(input.Current.Recent[0].RecentLinks) != 0 {
			t.Error(input.Current)
		}
		if len(input.Current.RecentLinks) != MaxRecentLinks || input.Current.RecentLinks[0] != "Link 2" || len([]rune(input.Current.RecentLinks[MaxRecentLinks-1])) != MaxLinkChars {
			t.Error("missing or unbounded programme memory", input.Current.RecentLinks)
		}
		track := input.Candidates[1]
		if track.Artist != "Bonnie Tyler" || track.Composer != "Jim Steinman" || track.Genre != "Pop" || track.Date != "1983" || track.Duration != "0:06:57" {
			t.Error(track)
		}
		fmt.Fprint(w, `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"{\"index\":1,\"chat\":\"Bonnie Tyler with Total Eclipse of the Heart.\"}"}]}]}`)
	}))
	defer server.Close()
	c := New(Config{Key: "test-secret"})
	c.base = server.URL
	current := Track{Title: "Previous"}
	for _, title := range []string{"a", "b", "c", "d", "e", "f"} {
		current.Recent = append(current.Recent, Track{Title: title, Recent: []Track{{Title: "nested"}}, RecentLinks: []string{"nested chatter"}})
	}
	for i := 0; i < MaxRecentLinks+1; i++ {
		current.RecentLinks = append(current.RecentLinks, fmt.Sprintf("Link %d", i))
	}
	current.RecentLinks = append(current.RecentLinks, strings.Repeat("é", MaxLinkChars+20))
	segment, err := c.Prepare(context.Background(), current, []Track{{Title: "Other"}, {Title: "Total Eclipse of the Heart", Artist: "Bonnie Tyler", Composer: "Jim Steinman", Genre: "Pop", Date: "1983", Duration: "0:06:57"}})
	if err != nil || segment.Index != 1 || !strings.Contains(segment.Text, "Bonnie Tyler") {
		t.Fatal(segment, err)
	}
}

func TestTextModelReasoningBudget(t *testing.T) {
	for _, model := range []string{"gpt-6.1-sol", "gpt-6-luna", "gpt-6-astra", "gpt-4o-mini"} {
		t.Run(model, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/audio/speech" {
					w.Write([]byte("MP3"))
					return
				}
				var request map[string]any
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				if request["model"] != model {
					t.Error(request["model"])
				}
				if model == "gpt-4o-mini" {
					if request["reasoning"] != nil || request["max_output_tokens"] != float64(500) {
						t.Error(request)
					}
				} else if request["reasoning"].(map[string]any)["effort"] != "low" || request["max_output_tokens"] != float64(2048) {
					t.Error(request)
				}
				fmt.Fprint(w, `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"{\"index\":0,\"chat\":\"Here is the next track.\"}"}]}]}`)
			}))
			defer server.Close()
			c := New(Config{Key: "test", TextModel: model})
			c.base = server.URL
			if _, err := c.Prepare(context.Background(), Track{}, []Track{{Title: "Next"}}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSessionIntroUsesSelectedVoiceAndOnlySpeechAPI(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/audio/speech" {
			t.Error("greeting made a text-model request", r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			return
		}
		text, _ := payload["input"].(string)
		if !strings.HasPrefix(text, "You're listening to Retro Radio.") || !strings.Contains(text, "Welcome to Jazz FM.") || payload["voice"] != "nova" || payload["response_format"] != "mp3" {
			t.Error(payload)
		}
		w.Write([]byte("MP3"))
	}))
	defer server.Close()
	dir := t.TempDir()
	c := New(Config{Key: "test", IntroDir: dir, VoiceSelection: func() string { return "nova" }})
	c.base = server.URL
	s, err := c.Intro(context.Background(), "Jazz FM")
	if err != nil || calls != 1 || string(s.Audio) != "MP3" || !strings.HasPrefix(s.Text, "You're listening to Retro Radio.") {
		t.Fatal(s, err, calls)
	}
}

func TestStoredStationWelcomeSurvivesRestartAndVoiceChange(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte("stored MP3"))
	}))
	defer server.Close()
	config := Config{Key: "test", IntroDir: t.TempDir(), Voice: "ballad"}
	first := New(config)
	first.base = server.URL
	for i := 0; i < 2; i++ {
		s, err := first.Intro(context.Background(), "Jazz FM")
		if err != nil || string(s.Audio) != "stored MP3" {
			t.Fatal(s, err)
		}
	}
	config.Voice = "nova"
	restarted := New(config)
	restarted.base = server.URL
	s, err := restarted.Intro(context.Background(), "Jazz FM")
	if err != nil || string(s.Audio) != "stored MP3" || calls != 1 {
		t.Fatal("welcome regenerated", err, calls)
	}
	if _, err := restarted.Intro(context.Background(), "Pop FM"); err != nil || calls != 2 {
		t.Fatal("stations did not have separate welcomes", err, calls)
	}
}

func TestSpokenIdentitiesFollowPlaybackInsteadOfPresenterMemory(t *testing.T) {
	for _, tc := range []struct {
		name, script string
		history      bool
	}{
		{"opening transition", "{{previous_track}}. A change of pace. {{next_track}}.", false},
		{"later transition", "{{previous_track}}. A change of pace. {{next_track}}.", true},
		{"wrong previous name", "That's Stale Performer with Stale Song. Up next, Someone Else.", true},
		{"reversed placeholders", "{{next_track}}. {{previous_track}}.", true},
		{"duplicate previous", "{{previous_track}}. {{previous_track}}. {{next_track}}.", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current := Track{Title: "Don't You (Forget About Me) [FLAC 24bit 96kHz] (2009 Remaster)", Artist: "Actual Performer", Composer: "Different Writer"}
			if tc.history {
				current.Recent = []Track{{Title: "Stale Song", Artist: "Stale Performer"}}
				current.RecentLinks = []string{"That's Stale Performer with Stale Song."}
			}
			c := New(Config{Key: "test"})
			var spoken string
			c.http.Transport = localTransport(func(r *http.Request) (*http.Response, error) {
				var p map[string]any
				if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
					t.Fatal(err)
				}
				if strings.HasSuffix(r.URL.Path, "/audio/speech") {
					spoken = p["input"].(string)
					return localHTTP("MP3"), nil
				}
				choice, _ := json.Marshal(map[string]any{"index": 1, "chat": tc.script})
				response, _ := json.Marshal(map[string]any{"status": "completed", "output": []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": string(choice)}}}}})
				return localHTTP(string(response)), nil
			})
			s, err := c.Prepare(context.Background(), current, []Track{{Title: "Wrong candidate", Artist: "Wrong artist"}, {Title: "Selected Song", Artist: "Selected Performer"}})
			if err != nil || s.Index != 1 || s.Text != spoken || string(s.Audio) != "MP3" {
				t.Fatal(s, err, spoken)
			}
			if !strings.HasPrefix(spoken, "That's Actual Performer with Don't You (Forget About Me).") || !strings.HasSuffix(spoken, "Up next, Selected Performer with Selected Song.") {
				t.Fatal(spoken)
			}
			for _, unwanted := range []string{"Stale", "Someone Else", "Different Writer", "FLAC", "Remaster", "{{"} {
				if strings.Contains(spoken, unwanted) {
					t.Fatal(spoken)
				}
			}
		})
	}
}

func TestTrackIdentityMissingTagsAndLiteralPlaceholders(t *testing.T) {
	if got := trackIdentity(Track{Title: "Instrumental"}); got != "Instrumental" {
		t.Fatal(got)
	}
	if got := trackIdentity(Track{Artist: "Performer"}); got != "Performer with an untitled track" {
		t.Fatal(got)
	}
	got, valid := renderTrackLink("{{previous_track}}. {{next_track}}.", Track{Title: "{{next_track}}", Artist: "A"}, Track{Title: "Next", Artist: "B"})
	if !valid || got != "That's A with {{next_track}}. Up next, B with Next." {
		t.Fatal(got, valid)
	}
}

func TestInvalidIdentityTemplateDoesNotMarkLocalUpdateBroadcast(t *testing.T) {
	cfg := LocalSettings{Enabled: true, Location: "Winchester, UK"}
	c := New(Config{Key: "test", LocalSettings: func() LocalSettings { return cfg }})
	now := time.Now()
	c.local.key = localKey(cfg)
	c.local.refreshAt = now.Add(time.Hour)
	c.local.items = []LocalItem{{Kind: "news", Summary: "Local news", Interesting: true, URL: "https://council.test/news", Date: now.Format("2006-01-02"), expires: now.Add(time.Hour)}}
	c.http.Transport = localTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/audio/speech") {
			return localHTTP("MP3"), nil
		}
		return localHTTP(`{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"{\"index\":0,\"chat\":\"Wrong previous song, then local news.\"}"}]}]}`), nil
	})
	s, err := c.Prepare(context.Background(), Track{Title: "Current"}, []Track{{Title: "Next"}})
	if err != nil || s.LocalStarted != nil || s.LocalAllowed != nil || !s.Expires.IsZero() || s.Text != "That's Current. Up next, Next." {
		t.Fatal(s, err)
	}
}

func TestComposerCreditCorrectionsKeepCurrentAndChosenTitles(t *testing.T) {
	for _, valid := range []bool{true, false} {
		t.Run(fmt.Sprint(valid), func(t *testing.T) {
			c := New(Config{Key: "test"})
			c.http.Transport = localTransport(func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, "/audio/speech") {
					return localHTTP("MP3"), nil
				}
				chat := "{{previous_track}}. A different interpretation next. {{next_track}}."
				if !valid {
					chat = "That's an unrelated earlier song."
				}
				choice, _ := json.Marshal(map[string]any{"index": 1, "chat": chat, "previous_performer": "Recording Performer", "next_performer": "Next Recording Performer"})
				response, _ := json.Marshal(map[string]any{"status": "completed", "output": []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": string(choice)}}}}})
				return localHTTP(string(response)), nil
			})
			s, err := c.Prepare(context.Background(), Track{Title: "Current Song", Artist: "Composer Tag", Composer: "Composer Tag"}, []Track{{Title: "Unselected Song"}, {Title: "Chosen Song", Artist: "Next Composer Tag", Composer: "Next Composer Tag"}})
			if err != nil || s.Index != 1 {
				t.Fatal(s, err)
			}
			previous, next := "Recording Performer", "Next Recording Performer"
			if !valid {
				previous, next = "Composer Tag", "Next Composer Tag"
			}
			if !strings.HasPrefix(s.Text, "That's "+previous+" with Current Song.") || !strings.HasSuffix(s.Text, "Up next, "+next+" with Chosen Song.") || strings.Contains(s.Text, "Unselected") {
				t.Fatal(s.Text)
			}
		})
	}
}
