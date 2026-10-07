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
			if p["store"] != false || p["model"] != "gpt-4o-mini" {
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
			if p["voice"] != expectedVoice || p["response_format"] != "mp3" || p["input"] != "Next artist, with Next song." || !strings.Contains(p["instructions"].(string), "British") {
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
		if len(input.Current.Recent) != 5 || input.Current.Recent[0].Title != "b" || len(input.Current.Recent[0].Recent) != 0 {
			t.Error(input.Current)
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
		current.Recent = append(current.Recent, Track{Title: title, Recent: []Track{{Title: "nested"}}})
	}
	segment, err := c.Prepare(context.Background(), current, []Track{{Title: "Other"}, {Title: "Total Eclipse of the Heart", Artist: "Bonnie Tyler", Composer: "Jim Steinman", Genre: "Pop", Date: "1983", Duration: "0:06:57"}})
	if err != nil || segment.Index != 1 || !strings.Contains(segment.Text, "Bonnie Tyler") {
		t.Fatal(segment, err)
	}
}
