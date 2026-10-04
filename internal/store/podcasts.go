// SPDX-License-Identifier: GPL-3.0-only
package store

import (
	"database/sql"
	"fmt"
	"retroradio.local/server/internal/model"
	"strconv"
	"strings"
	"time"
)

func (s *Store) migrateV7() error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`
 CREATE TABLE IF NOT EXISTS podcasts(number INTEGER PRIMARY KEY AUTOINCREMENT,feed TEXT NOT NULL UNIQUE,title TEXT NOT NULL,author TEXT NOT NULL,description TEXT NOT NULL,updated TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS podcast_episodes(number INTEGER PRIMARY KEY AUTOINCREMENT,podcast INTEGER NOT NULL REFERENCES podcasts(number) ON DELETE CASCADE,guid TEXT NOT NULL,title TEXT NOT NULL,description TEXT NOT NULL,url TEXT NOT NULL,codec TEXT NOT NULL,published TEXT NOT NULL,duration TEXT NOT NULL,UNIQUE(podcast,guid));
 CREATE INDEX IF NOT EXISTS episode_order ON podcast_episodes(podcast,published DESC,number DESC);
 INSERT OR IGNORE INTO schema_migrations(version) VALUES(7);`)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func podcastNumber(id string) (int64, error) {
	if !strings.HasPrefix(id, "P") {
		return 0, sql.ErrNoRows
	}
	n, e := strconv.ParseInt(strings.TrimPrefix(id, "P"), 10, 64)
	if e != nil || n < 1 {
		return 0, sql.ErrNoRows
	}
	return n, nil
}
func (s *Store) Podcasts() ([]model.Podcast, error) {
	rows, e := s.DB.Query(`SELECT p.number,feed,title,author,description,updated,(SELECT count(*) FROM podcast_episodes e WHERE e.podcast=p.number) FROM podcasts p ORDER BY title COLLATE NOCASE`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []model.Podcast{}
	for rows.Next() {
		var p model.Podcast
		var n int64
		var updated string
		if e = rows.Scan(&n, &p.Feed, &p.Title, &p.Author, &p.Description, &updated, &p.Episodes); e != nil {
			return nil, e
		}
		p.ID = fmt.Sprintf("P%d", n)
		p.Updated, _ = time.Parse(time.RFC3339, updated)
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Store) Podcast(id string) (model.Podcast, error) {
	all, e := s.Podcasts()
	if e != nil {
		return model.Podcast{}, e
	}
	for _, p := range all {
		if p.ID == id {
			return p, nil
		}
	}
	return model.Podcast{}, sql.ErrNoRows
}

// A refresh commits the whole feed at once. Existing episode IDs stay stable.
func (s *Store) SavePodcast(p model.Podcast, episodes []model.Episode) (model.Podcast, error) {
	tx, e := s.DB.Begin()
	if e != nil {
		return p, e
	}
	defer tx.Rollback()
	if p.ID == "" {
		_, e = tx.Exec(`INSERT INTO podcasts(feed,title,author,description,updated) VALUES(?,?,?,?,?) ON CONFLICT(feed) DO NOTHING`, p.Feed, p.Title, p.Author, p.Description, time.Now().UTC().Format(time.RFC3339))
		if e != nil {
			return p, e
		}
	}
	var n int64
	if p.ID != "" {
		n, e = podcastNumber(p.ID)
	} else {
		e = tx.QueryRow(`SELECT number FROM podcasts WHERE feed=?`, p.Feed).Scan(&n)
	}
	if e != nil {
		return p, e
	}
	result, e := tx.Exec(`UPDATE podcasts SET title=?,author=?,description=?,updated=? WHERE number=?`, p.Title, p.Author, p.Description, time.Now().UTC().Format(time.RFC3339), n)
	if e != nil {
		return p, e
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return p, sql.ErrNoRows
	}
	for _, ep := range episodes {
		_, e = tx.Exec(`INSERT INTO podcast_episodes(podcast,guid,title,description,url,codec,published,duration) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(podcast,guid) DO UPDATE SET title=excluded.title,description=excluded.description,url=excluded.url,codec=excluded.codec,published=excluded.published,duration=excluded.duration`, n, ep.GUID, ep.Title, ep.Description, ep.URL, ep.Codec, ep.Published.UTC().Format(time.RFC3339), ep.Duration)
		if e != nil {
			return p, e
		}
	}
	// Keep a bounded history even when publishers remove older entries from their feed.
	_, e = tx.Exec(`DELETE FROM podcast_episodes WHERE podcast=? AND number NOT IN (SELECT number FROM podcast_episodes WHERE podcast=? ORDER BY published DESC,number DESC LIMIT 500)`, n, n)
	if e != nil {
		return p, e
	}
	if e = tx.Commit(); e != nil {
		return p, e
	}
	return s.Podcast(fmt.Sprintf("P%d", n))
}
func (s *Store) Episodes(id string) ([]model.Episode, error) {
	n, e := podcastNumber(id)
	if e != nil {
		return nil, e
	}
	rows, e := s.DB.Query(`SELECT number,guid,title,description,url,codec,published,duration FROM podcast_episodes WHERE podcast=? ORDER BY published DESC,number DESC`, n)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []model.Episode{}
	for rows.Next() {
		var ep model.Episode
		var number int64
		var published string
		if e = rows.Scan(&number, &ep.GUID, &ep.Title, &ep.Description, &ep.URL, &ep.Codec, &published, &ep.Duration); e != nil {
			return nil, e
		}
		ep.ID = fmt.Sprintf("%sX%d", id, number)
		ep.PodcastID = id
		ep.Published, _ = time.Parse(time.RFC3339, published)
		out = append(out, ep)
	}
	return out, rows.Err()
}
func (s *Store) Episode(id string) (model.Episode, error) {
	parts := strings.Split(id, "X")
	if len(parts) != 2 {
		return model.Episode{}, sql.ErrNoRows
	}
	all, e := s.Episodes(parts[0])
	if e != nil {
		return model.Episode{}, e
	}
	for _, ep := range all {
		if ep.ID == id {
			return ep, nil
		}
	}
	return model.Episode{}, sql.ErrNoRows
}
func (s *Store) DeletePodcast(id string) error {
	n, e := podcastNumber(id)
	if e != nil {
		return e
	}
	result, e := s.DB.Exec(`DELETE FROM podcasts WHERE number=?`, n)
	if e != nil {
		return e
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		return sql.ErrNoRows
	}
	return nil
}
