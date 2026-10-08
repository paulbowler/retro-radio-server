// SPDX-License-Identifier: GPL-3.0-only
package agentfm

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ResetDerivedCaches is called after playback/preparation has drained. Delete
// only this client's generated, hash-named welcome files, never the whole folder.
func (c *Client) ResetDerivedCaches() error {
	c.introMu.Lock()
	defer c.introMu.Unlock()
	c.intros = nil
	if c.local != nil {
		s := c.local
		s.mu.Lock()
		if s.cancel != nil {
			s.cancel()
		}
		s.key, s.place = "", ""
		s.items = nil
		s.seen = make(map[[32]byte]time.Time)
		s.refreshAt, s.lastOffer = time.Time{}, time.Time{}
		s.mu.Unlock()
	}
	if c.config.IntroDir == "" {
		return nil
	}
	entries, err := os.ReadDir(c.config.IntroDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		hash := strings.TrimSuffix(entry.Name(), ".mp3")
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".mp3") || len(hash) != 64 {
			continue
		}
		if _, err := hex.DecodeString(hash); err != nil {
			continue
		}
		if err := os.Remove(filepath.Join(c.config.IntroDir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}
