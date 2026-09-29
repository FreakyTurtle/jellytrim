package main

import (
	"fmt"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/freakyturtle/jellytrim/internal/jellyfin/jellyfintest"
)

// Library names and IDs in the fake Jellyfin.
var (
	filmsLibrary = jellyfintest.Library{Name: "Films", CollectionType: "movies", ItemID: id("library:films"), Locations: []string{filmsRoot}}
	tvLibrary    = jellyfintest.Library{Name: "TV", CollectionType: "tvshows", ItemID: id("library:tv"), Locations: []string{tvRoot}}
)

// serverName is what the fake Jellyfin calls itself.
const serverName = "Home Media"

// fakeJellyfin is the in-process Jellyfin and the items in it, so a past
// job can update a file's size the way a rescan would.
type fakeJellyfin struct {
	*jellyfintest.Server
	mu    sync.Mutex
	items map[string]jellyfintest.Item // by entry key
}

// startJellyfin serves the catalogue from a jellyfintest server. sizeOf
// reports each file's current size on disk.
func startJellyfin(tb testing.TB, c *catalogue, sizeOf func(entry) int64) *fakeJellyfin {
	jf := &fakeJellyfin{
		Server: jellyfintest.New(tb, jellyfintest.WithServerName(serverName)),
		items:  map[string]jellyfintest.Item{},
	}
	jf.AddLibrary(filmsLibrary)
	jf.AddLibrary(tvLibrary)
	for _, p := range household {
		last := evening(c.now, p.ActiveDays, "active:"+p.Name)
		jf.AddUser(jellyfintest.User{ID: userID(p.Name), Name: p.Name, IsAdministrator: p.Admin, LastActivityDate: &last})
	}
	art := posters{}
	for i := range shows {
		jf.addShow(&shows[i], c.now, art)
	}
	for _, e := range c.entries {
		it := itemFor(e, sizeOf(e), art)
		jf.items[e.Key] = it
		jf.AddItem(it)
		for user, days := range e.Seen {
			played := evening(c.now, days, e.Key+user)
			jf.SetUserData(userID(user), e.ID, jellyfintest.UserData{Played: true, PlayCount: 1 + int(jitter(e.Key+user+"n")*1.4), LastPlayedDate: &played})
		}
		for _, user := range e.Fav {
			jf.setFavourite(user, e, c.now)
		}
	}
	for _, col := range collections {
		ids := make([]string, 0, len(col.Titles))
		for _, t := range col.Titles {
			ids = append(ids, c.byKey[t].ID)
		}
		jf.AddCollection(jellyfintest.Collection{ID: id("collection:" + col.Name), Name: col.Name, ItemIDs: ids})
	}
	return jf
}

// setFavourite marks an item a favourite for one user, keeping any watch
// state already set.
func (jf *fakeJellyfin) setFavourite(user string, e entry, now time.Time) {
	ud := jellyfintest.UserData{IsFavorite: true}
	if days, ok := e.Seen[user]; ok {
		played := evening(now, days, e.Key+user)
		ud.Played, ud.PlayCount, ud.LastPlayedDate = true, 2, &played
	}
	jf.SetUserData(userID(user), e.ID, ud)
}

// addShow adds a series and its seasons, which Jellyfin lists as folders.
func (jf *fakeJellyfin) addShow(s *show, now time.Time, art posters) {
	seriesID := id("series:" + s.Name)
	jf.AddItem(jellyfintest.Item{
		ID: seriesID, Name: s.Name, Type: "Series", IsFolder: true, Path: showFolder(s), ProductionYear: s.Year,
		DateCreated: daysAgo(now, float64(s.Added)), Genres: s.Genres, Tags: s.Tags, LibraryID: tvLibrary.ItemID,
		ParentID: tvLibrary.ItemID, Image: art.get(s.Name),
	})
	for si := range s.Seasons {
		jf.AddItem(jellyfintest.Item{
			ID: seasonID(s, si+1), Name: fmt.Sprintf("Season %d", si+1), Type: "Season", IsFolder: true,
			Path: fmt.Sprintf("%s/Season %02d", showFolder(s), si+1), ParentID: seriesID, SeriesID: seriesID,
			SeriesName: s.Name, IndexNumber: si + 1, LibraryID: tvLibrary.ItemID, Image: art.get(s.Name),
		})
	}
}

func seasonID(s *show, season int) string { return id(fmt.Sprintf("season:%s:%d", s.Name, season)) }

// itemFor is the Jellyfin item for an entry, with a file of size bytes.
func itemFor(e entry, size int64, art posters) jellyfintest.Item {
	ticks := e.Duration.Nanoseconds() / 100
	it := jellyfintest.Item{
		ID: e.ID, Name: e.Name, SortName: e.Name, Type: e.Type, Path: e.JFPath, ProductionYear: e.Year,
		DateCreated: e.Added, RunTimeTicks: ticks, Container: e.Container(), Genres: e.Genres, Tags: e.Tags,
		MediaSources: []jellyfintest.MediaSource{{
			ID: e.ID, Path: e.JFPath, Protocol: "File", Type: "Default", Container: e.Container(), Size: size,
			Name: e.Name, RunTimeTicks: ticks, VideoType: "VideoFile", MediaStreams: []jellyfintest.MediaStream{},
		}},
	}
	if e.Show == nil {
		it.LibraryID, it.ParentID = filmsLibrary.ItemID, filmsLibrary.ItemID
		it.Image = art.get(e.Name)
		return it
	}
	seriesID := id("series:" + e.Show.Name)
	it.LibraryID, it.ParentID = tvLibrary.ItemID, seasonID(e.Show, e.Season)
	it.SeriesID, it.SeriesName = seriesID, e.Show.Name
	it.SeasonID, it.SeasonName = seasonID(e.Show, e.Season), fmt.Sprintf("Season %d", e.Season)
	it.ParentIndexNumber, it.IndexNumber = e.Season, e.Episode
	it.SortName = fmt.Sprintf("%s %03d %03d", e.Show.Name, e.Season, e.Episode)
	it.Image = art.get(e.Show.Name)
	return it
}

// resize records a new file size for an entry, as a Jellyfin rescan
// would after JellyTrim replaced the file.
func (jf *fakeJellyfin) resize(key string, size int64) {
	jf.mu.Lock()
	defer jf.mu.Unlock()
	it := jf.items[key]
	it.MediaSources[0].Size = size
	jf.items[key] = it
	jf.AddItem(it)
}

func userID(name string) string { return id("user:" + name) }

// demoTB lets jellyfintest, which is written for tests, run in a program.
// Only the methods jellyfintest calls are implemented; the embedded
// interface is nil, so anything else would panic, which is fine for a
// development tool.
type demoTB struct {
	testing.TB
	mu       sync.Mutex
	cleanups []func()
}

func (*demoTB) Helper() {}

func (d *demoTB) Cleanup(f func()) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cleanups = append(d.cleanups, f)
}

func (*demoTB) Errorf(format string, args ...any) {
	slog.Warn("demo: fake Jellyfin: " + fmt.Sprintf(format, args...))
}

func (*demoTB) Fatalf(format string, args ...any) {
	fmt.Fprintln(os.Stderr, "demo: fake Jellyfin: "+fmt.Sprintf(format, args...))
	os.Exit(1)
}

// close runs the cleanups, last first, as the testing package does.
func (d *demoTB) close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i := len(d.cleanups) - 1; i >= 0; i-- {
		d.cleanups[i]()
	}
	d.cleanups = nil
}
