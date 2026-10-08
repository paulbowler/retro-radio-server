package agentfm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResetDerivedSpeechCachesPreservesOtherFilesAndConfiguration(t *testing.T) {
	dir := t.TempDir()
	c := &Client{config: Config{IntroDir: dir, Key: "secret", Voice: "ballad"}, intros: map[string][]byte{"Agent FM": []byte("old")}}
	c.local = newLocalService(c)
	c.local.key, c.local.place = "old", "old place"
	c.local.seen[[32]byte{1}] = time.Now()
	owned := strings.Repeat("a", 64) + ".mp3"
	for _, name := range []string{owned, "my-recording.mp3", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("data"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.ResetDerivedCaches(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, owned)); !os.IsNotExist(err) {
		t.Fatal("cached welcome retained", err)
	}
	for _, name := range []string{"my-recording.mp3", "notes.txt"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatal("other file deleted", err)
		}
	}
	if len(c.intros) != 0 || len(c.local.seen) != 0 || c.local.key != "" || c.config.Key != "secret" || c.config.Voice != "ballad" {
		t.Fatal("incorrect cache/configuration reset")
	}
}
