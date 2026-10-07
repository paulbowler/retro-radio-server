// SPDX-License-Identifier: GPL-3.0-only
package upnp

import (
	"bytes"
	"encoding/binary"
	"math"
	"path/filepath"
	"testing"

	"retroradio.local/server/internal/content"
	"retroradio.local/server/internal/store"
)

func agentTestSamples(frames int, level float32) []byte {
	data := make([]byte, frames*agentPCMFrame)
	for i := 0; i < len(data); i += 4 {
		binary.LittleEndian.PutUint32(data[i:i+4], math.Float32bits(level))
	}
	return data
}
func agentTestRMS(data []byte) float64 {
	var sum float64
	for i := 0; i+4 <= len(data); i += 4 {
		v := float64(math.Float32frombits(binary.LittleEndian.Uint32(data[i : i+4])))
		sum += v * v
	}
	return math.Sqrt(sum / float64(len(data)/4))
}
func TestAgentSavedVolumeChangesActualPCM(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.DB.Close()
	q := &musicQueue{volume: func() int { return db.Settings().AgentVolume }}
	speech := agentTestSamples(agentPCMRate, .08)
	render := func(level int) []byte {
		t.Helper()
		settings := store.DefaultSettings()
		settings.AgentVolume = level
		if err := db.SaveSettings(settings); err != nil {
			t.Fatal(err)
		}
		var output bytes.Buffer
		p := &agentProgram{sink: &output, timeline: &agentTimeline{}}
		if err := p.transition(q, nil, speech, content.Item{}); err != nil {
			t.Fatal(err)
		}
		return output.Bytes()
	}
	quiet, loud := render(25), render(400)
	if ratio := agentTestRMS(loud) / agentTestRMS(quiet); math.Abs(ratio-16) > .001 {
		t.Fatal("saved volume failed to reach samples", ratio)
	}
}
func TestAgentTalksOverTailWithoutSilentBoundary(t *testing.T) {
	music := agentTestSamples(8*agentPCMRate, .1)
	music = append(music, make([]byte, agentPCMSecond)...)
	var output bytes.Buffer
	p := &agentProgram{sink: &output, timeline: &agentTimeline{}}
	tail, err := p.music(bytes.NewReader(music))
	if err != nil {
		t.Fatal(err)
	}
	speech := append(make([]byte, agentPCMSecond), agentTestSamples(4*agentPCMRate, .08)...)
	speech = append(speech, make([]byte, agentPCMSecond)...)
	q := &musicQueue{name: "Agent FM", volume: func() int { return 200 }}
	if err := p.transition(q, tail, speech, content.Item{Title: "Next"}); err != nil {
		t.Fatal(err)
	}
	// Eight seconds of music + four of chat minus three seconds of overlap.
	if got := output.Len(); got != 9*agentPCMSecond {
		t.Fatal("gap or incorrect overlap", got)
	}
	for _, tc := range []struct {
		second int
		level  float64
	}{{4, .1}, {5, .185}, {7, .185}, {8, .16}} {
		i := tc.second * agentPCMSecond
		actual := float64(math.Float32frombits(binary.LittleEndian.Uint32(output.Bytes()[i : i+4])))
		if math.Abs(actual-tc.level) > .0001 {
			t.Fatal("ducking/gain wrong", tc.second, actual)
		}
	}
	cues := p.timeline.ready(int64(output.Len()))
	if len(cues) != 2 || cues[0].offset != 5*agentPCMSecond || cues[1].offset != 8*agentPCMSecond || !cues[1].end {
		t.Fatal("metadata does not follow audio", cues)
	}
}
func TestAgentPreservesQuietMusicAndBoundsSilenceTrimming(t *testing.T) {
	quiet := agentTestSamples(3*agentPCMRate, .0015)
	silence := make([]byte, 4*agentPCMSecond)
	var output bytes.Buffer
	p := &agentProgram{sink: &output, timeline: &agentTimeline{}}
	tail, err := p.music(bytes.NewReader(append(quiet, silence...)))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.transition(&musicQueue{}, tail, nil, content.Item{}); err != nil {
		t.Fatal(err)
	}
	if output.Len() != 5*agentPCMSecond || !bytes.Equal(output.Bytes()[:len(quiet)], quiet) {
		t.Fatal("trimmed musical content or unbounded silence")
	}
}
