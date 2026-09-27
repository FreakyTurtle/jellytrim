package jellyfintest

import (
	"strings"
	"time"
)

// Library is a library in the fake server.
type Library struct {
	Name           string   `json:"Name"`
	CollectionType string   `json:"CollectionType"`
	ItemID         string   `json:"ItemId"`
	Locations      []string `json:"Locations"`
}

// User is a user in the fake server.
type User struct {
	ID               string
	Name             string
	IsAdministrator  bool
	IsDisabled       bool
	IsHidden         bool
	LastActivityDate *time.Time
}

// UserData is one user's watch state for one item.
type UserData struct {
	Played                bool
	PlayCount             int
	IsFavorite            bool
	LastPlayedDate        *time.Time
	PlaybackPositionTicks int64
}

// MediaStream is one stream in a media source. The JSON tags match
// Jellyfin's, so fixtures decode straight into it.
type MediaStream struct {
	Type           string `json:"Type"`
	Codec          string `json:"Codec,omitempty"`
	Profile        string `json:"Profile,omitempty"`
	Width          int    `json:"Width,omitempty"`
	Height         int    `json:"Height,omitempty"`
	BitRate        int64  `json:"BitRate,omitempty"`
	BitDepth       int    `json:"BitDepth,omitempty"`
	Language       string `json:"Language,omitempty"`
	IsDefault      bool   `json:"IsDefault"`
	IsForced       bool   `json:"IsForced"`
	IsExternal     bool   `json:"IsExternal"`
	VideoRange     string `json:"VideoRange"`
	VideoRangeType string `json:"VideoRangeType"`
	Index          int    `json:"Index"`
	DisplayTitle   string `json:"DisplayTitle,omitempty"`
}

// MediaSource is one version of an item. The JSON tags match Jellyfin's.
type MediaSource struct {
	ID           string        `json:"Id"`
	Path         string        `json:"Path"`
	Protocol     string        `json:"Protocol"`
	Type         string        `json:"Type"`
	Container    string        `json:"Container"`
	Size         int64         `json:"Size"`
	Name         string        `json:"Name"`
	IsRemote     bool          `json:"IsRemote"`
	RunTimeTicks int64         `json:"RunTimeTicks,omitempty"`
	VideoType    string        `json:"VideoType"`
	MediaStreams []MediaStream `json:"MediaStreams"`
	Bitrate      int64         `json:"Bitrate,omitempty"`
}

// Item is a movie, episode or other item in the fake server. The JSON tags
// match Jellyfin's so the captured fixtures decode into it; the server
// renders it back in Jellyfin's shape.
type Item struct {
	ID                string            `json:"Id"`
	Name              string            `json:"Name"`
	SortName          string            `json:"SortName"`
	Type              string            `json:"Type"`
	Path              string            `json:"Path"`
	ParentID          string            `json:"ParentId"`
	SeriesID          string            `json:"SeriesId"`
	SeriesName        string            `json:"SeriesName"`
	SeasonID          string            `json:"SeasonId"`
	SeasonName        string            `json:"SeasonName"`
	ParentIndexNumber int               `json:"ParentIndexNumber"`
	IndexNumber       int               `json:"IndexNumber"`
	ProductionYear    int               `json:"ProductionYear"`
	DateCreated       time.Time         `json:"DateCreated"`
	RunTimeTicks      int64             `json:"RunTimeTicks"`
	Container         string            `json:"Container"`
	LocationType      string            `json:"LocationType"`
	VideoType         string            `json:"VideoType"`
	IsFolder          bool              `json:"IsFolder"`
	Tags              []string          `json:"Tags"`
	Genres            []string          `json:"Genres"`
	ProviderIDs       map[string]string `json:"ProviderIds"`
	ImageTags         map[string]string `json:"ImageTags"`
	MediaSources      []MediaSource     `json:"MediaSources"`

	// LibraryID is the library the item belongs to. AddItem fills it in
	// from the path when it is empty.
	LibraryID string `json:"-"`
	// Image is the primary image served for the item. Leave it nil for an
	// item without artwork.
	Image []byte `json:"-"`
}

// Collection is a collection (box set) and the items in it.
type Collection struct {
	ID      string
	Name    string
	ItemIDs []string
}

// ancestors are the IDs a recursive parentId query matches the item under.
func (it *Item) ancestors() []string {
	return []string{it.LibraryID, it.ParentID, it.SeasonID, it.SeriesID}
}

// libraryFor finds the library whose location contains path.
func libraryFor(libs []Library, path string) string {
	best, bestLen := "", -1
	for _, l := range libs {
		for _, loc := range l.Locations {
			loc = strings.TrimRight(loc, "/")
			if (path == loc || strings.HasPrefix(path, loc+"/")) && len(loc) > bestLen {
				best, bestLen = l.ItemID, len(loc)
			}
		}
	}
	return best
}

// fillItemDefaults gives a hand-built item the fields Jellyfin always sends.
func fillItemDefaults(it *Item) {
	if it.SortName == "" {
		it.SortName = strings.ToLower(it.Name)
	}
	if it.LocationType == "" {
		it.LocationType = "FileSystem"
	}
	if it.Type == "Movie" || it.Type == "Episode" {
		if it.VideoType == "" {
			it.VideoType = "VideoFile"
		}
		if len(it.MediaSources) == 0 && it.Path != "" {
			it.MediaSources = []MediaSource{{
				ID: it.ID, Path: it.Path, Protocol: "File", Type: "Default",
				Container: it.Container, Name: it.Name, VideoType: it.VideoType,
				RunTimeTicks: it.RunTimeTicks, MediaStreams: []MediaStream{},
			}}
		}
	}
	if it.Tags == nil {
		it.Tags = []string{}
	}
	if it.Genres == nil {
		it.Genres = []string{}
	}
	if it.ProviderIDs == nil {
		it.ProviderIDs = map[string]string{}
	}
	if it.DateCreated.IsZero() {
		it.DateCreated = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	}
}
