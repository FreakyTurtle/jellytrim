package main

import (
	"crypto/md5" // #nosec G501 -- IDs only, not security
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// Jellyfin's view of the media folders, and the libraries over them.
const (
	filmsRoot = "/media/films"
	tvRoot    = "/media/tv"
)

// entry is one playable item (a film or an episode) with everything the
// fake Jellyfin, the files on disk and the fake prober need.
type entry struct {
	Key      string // film title, or "Series S01E02"
	ID       string
	Name     string
	Type     string // Movie or Episode
	Year     int
	Source   source
	Size     int64
	Duration time.Duration
	Added    time.Time
	JFPath   string // as Jellyfin sees it
	Rel      string // relative to the demo's media folder
	Genres   []string
	Tags     []string
	Seen     seen
	Fav      []string
	Linked   bool

	// Episodes only.
	Show            *show
	Season, Episode int
}

// Container is the file extension, which is also the container family.
func (e entry) Container() string {
	if e.Source == uhdDV5 {
		return "mp4"
	}
	return "mkv"
}

// catalogue is the flattened library.
type catalogue struct {
	now     time.Time
	entries []entry
	byKey   map[string]*entry
}

func newCatalogue(now time.Time) (*catalogue, error) {
	c := &catalogue{now: now, byKey: map[string]*entry{}}
	for _, f := range films {
		c.entries = append(c.entries, filmEntry(f, now))
	}
	for i := range shows {
		c.entries = append(c.entries, showEntries(&shows[i], now)...)
	}
	for i := range c.entries {
		e := &c.entries[i]
		if _, dup := c.byKey[e.Key]; dup {
			return nil, fmt.Errorf("the catalogue lists %q twice", e.Key)
		}
		c.byKey[e.Key] = e
	}
	return c, c.check()
}

// check makes sure the history and queue tables name real entries.
func (c *catalogue) check() error {
	names := append([]string{running}, queued...)
	for _, h := range history {
		names = append(names, h.Item)
	}
	for _, col := range collections {
		names = append(names, col.Titles...)
	}
	for _, n := range names {
		if c.byKey[n] == nil {
			return fmt.Errorf("the catalogue has no item called %q", n)
		}
	}
	return nil
}

func filmEntry(f film, now time.Time) entry {
	folder := fileName(fmt.Sprintf("%s (%d)", f.Title, f.Year))
	e := entry{
		Key: f.Title, ID: id("film:" + f.Title), Name: f.Title, Type: "Movie", Year: f.Year, Source: f.Source,
		Size:     int64(f.GB*1e9) + int64(jitter(f.Title)*49e6),
		Duration: time.Duration(f.Minutes)*time.Minute + time.Duration(jitter(f.Title+"s")*59)*time.Second,
		Added:    daysAgo(now, float64(f.Added)).Add(-time.Duration(jitter(f.Title+"a")*10) * time.Hour),
		Genres:   f.Genres, Tags: f.Tags, Seen: f.Seen, Fav: f.Fav, Linked: f.HardLinked,
	}
	e.Rel = path.Join("films", folder, folder+"."+e.Container())
	e.JFPath = path.Join(filmsRoot, folder, folder+"."+e.Container())
	return e
}

func showEntries(s *show, now time.Time) []entry {
	var out []entry
	folder := fileName(fmt.Sprintf("%s (%d)", s.Name, s.Year))
	for si, titles := range s.Seasons {
		for ei, title := range titles {
			code := fmt.Sprintf("S%02dE%02d", si+1, ei+1)
			key := s.Name + " " + code
			src := s.Source
			if odd, ok := s.Odd[code]; ok {
				src = odd
			}
			file := fileName(fmt.Sprintf("%s - %s - %s.mkv", s.Name, code, title))
			season := fmt.Sprintf("Season %02d", si+1)
			out = append(out, entry{
				Key: key, ID: id("episode:" + key), Name: title, Type: "Episode", Year: s.Year + si, Source: src,
				Size:     int64((s.MinGB + (s.MaxGB-s.MinGB)*jitter(key)) * 1e9),
				Duration: time.Duration(s.Minutes)*time.Minute + time.Duration(jitter(key+"d")*240-120)*time.Second,
				Added:    daysAgo(now, float64(s.Added-si*120)),
				Rel:      path.Join("tv", folder, season, file), JFPath: path.Join(tvRoot, folder, season, file),
				Genres: s.Genres, Tags: s.Tags, Seen: episodeSeen(s.Watched, si+1, ei+1),
				Show: s, Season: si + 1, Episode: ei + 1,
			})
		}
	}
	return out
}

// episodeSeen works out who watched one episode, and when, from the runs.
func episodeSeen(runs []binge, season, episode int) seen {
	out := seen{}
	for _, r := range runs {
		if r.Season == season && episode <= r.Through {
			out[r.User] = r.Days + (r.Through - episode)
		}
	}
	return out
}

// showFolder is the series folder, as Jellyfin sees it.
func showFolder(s *show) string {
	return path.Join(tvRoot, fileName(fmt.Sprintf("%s (%d)", s.Name, s.Year)))
}

// Local is the entry's file under the demo's media folder.
func (e entry) Local(mediaDir string) string {
	return filepath.Join(mediaDir, filepath.FromSlash(e.Rel))
}

// id is a stable 32-character Jellyfin-style ID.
func id(s string) string {
	sum := md5.Sum([]byte("jellytrim-demo:" + s)) // #nosec G401 -- IDs only
	return hex.EncodeToString(sum[:])
}

// jitter is a stable number in [0, 1) for s, so sizes and times look
// natural but are the same on every run.
func jitter(s string) float64 {
	sum := md5.Sum([]byte(s)) // #nosec G401 -- not security
	return float64(binary.BigEndian.Uint32(sum[:4])) / (1 << 32)
}

// daysAgo is the moment that many days before now.
func daysAgo(now time.Time, days float64) time.Time {
	return now.Add(-time.Duration(days * 24 * float64(time.Hour)))
}

// fileName drops characters that do not belong in a file name.
func fileName(s string) string {
	return strings.NewReplacer(":", " -", "/", "-", "?", "").Replace(s)
}

// evening is an evening that many days before now, when people watch.
func evening(now time.Time, days int, salt string) time.Time {
	d := now.AddDate(0, 0, -days).UTC()
	t := time.Date(d.Year(), d.Month(), d.Day(), 19, 0, 0, 0, time.UTC)
	t = t.Add(time.Duration(jitter(salt) * 4 * float64(time.Hour)))
	if t.After(now) {
		t = now.Add(-2 * time.Hour)
	}
	return t
}
