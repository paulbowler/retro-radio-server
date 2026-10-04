// SPDX-License-Identifier: GPL-3.0-only
package web

import (
	"fmt"
	"retroradio.local/server/internal/model"
	"strings"
	"time"
)

func lastContact(t time.Time) string {
	if t.IsZero() {
		return "Not connected yet"
	}
	age := time.Since(t)
	if age < time.Minute {
		return "Last connected just now"
	}
	count, unit := int(age/time.Minute), "minute"
	if age >= time.Hour {
		count, unit = int(age/time.Hour), "hour"
	}
	if age >= 24*time.Hour {
		count, unit = int(age/(24*time.Hour)), "day"
	}
	if count != 1 {
		unit += "s"
	}
	return fmt.Sprintf("Last connected %d %s ago", count, unit)
}

func radioModel(s string) string {
	if strings.Contains(strings.ToLower(s), "unverified") || strings.Contains(strings.ToLower(s), "frontier xml") {
		return ""
	}
	return s
}

func stationCountry(s string) string {
	if code := countryCodes[strings.ToLower(strings.TrimSpace(s))]; code != "" {
		return countryName(code)
	}
	return s
}

// Keep low-level probe errors in health records, away from everyday station controls.
func streamProblem(h model.Health) string {
	text := strings.ToLower(h.Message)
	switch {
	case strings.Contains(text, "blocked"), strings.Contains(text, "unsafe"), strings.Contains(text, "private"):
		return "Use the station’s public listening link. This address cannot be used."
	case strings.Contains(text, "protected"), strings.Contains(text, "drm"):
		return "This station’s stream is protected and cannot be played."
	case strings.Contains(text, "404"), strings.Contains(text, "410"):
		return "This station’s listening link no longer works."
	case strings.Contains(text, "unsupported"), strings.Contains(text, "format"), strings.Contains(text, "codec"), strings.Contains(text, "not audio"):
		return "We couldn’t play this station’s audio. Try another station."
	case strings.Contains(text, "timed out"), strings.Contains(text, "timeout"), strings.Contains(text, "connection"), strings.Contains(text, "resolve"):
		return "We couldn’t reach this station. Try again later."
	default:
		return "We couldn’t play this station. Try again later."
	}
}
func stationStatus(h model.Health) string {
	if h.Checked.IsZero() {
		return "Not checked yet"
	}
	if h.Working {
		return "Ready to play"
	}
	return streamProblem(h)
}

type listeningEvent struct {
	Time    time.Time
	Summary string
}

func listeningEvents(events []model.Event) []listeningEvent {
	result := []listeningEvent{}
	for _, event := range events {
		text := ""
		switch event.Kind {
		case "Station lookup":
			text = "Selected " + event.Detail
		case "Stream connected":
			station := event.Detail
			if index := strings.LastIndex(station, ": "); index >= 0 {
				station = station[:index]
			}
			text = "Playback started: " + station
		case "Stream disconnected":
			text = "Playback stopped: " + event.Detail
		case "Stream interrupted":
			text = "Playback interrupted: " + event.Detail
		case "Stream failed":
			text = "A station could not be played."
		}
		if text != "" {
			result = append(result, listeningEvent{event.Time, text})
		}
	}
	return result
}
