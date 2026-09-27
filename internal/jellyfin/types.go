package jellyfin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

// SystemInfo is the subset of /System/Info and /System/Info/Public that
// JellyTrim shows on the connection screen.
type SystemInfo struct {
	ServerName             string `json:"ServerName"`
	Version                string `json:"Version"`
	ID                     string `json:"Id"`
	OperatingSystem        string `json:"OperatingSystem"`
	StartupWizardCompleted bool   `json:"StartupWizardCompleted"`
}

// Library is one Jellyfin library ("virtual folder").
type Library struct {
	Name string `json:"Name"`
	// CollectionType is "movies", "tvshows", "mixed" and so on. Empty for
	// libraries created without a type.
	CollectionType string `json:"CollectionType"`
	// ItemID is the library's folder item, used as parentId in item queries.
	ItemID string `json:"ItemId"`
	// Locations are the library's folders as Jellyfin sees them.
	Locations []string `json:"Locations"`
}

// UserPolicy holds the policy flags JellyTrim uses to decide whose watch
// state can count.
type UserPolicy struct {
	IsAdministrator bool `json:"IsAdministrator"`
	IsDisabled      bool `json:"IsDisabled"`
	IsHidden        bool `json:"IsHidden"`
}

// User is a Jellyfin user.
type User struct {
	ID     string     `json:"Id"`
	Name   string     `json:"Name"`
	Policy UserPolicy `json:"Policy"`
	// LastActivityDate is nil when the user has never been active.
	LastActivityDate *time.Time `json:"LastActivityDate"`
}

// UnmarshalJSON accepts every date format Jellyfin has used.
func (u *User) UnmarshalJSON(b []byte) error {
	type alias User
	aux := struct {
		*alias
		LastActivityDate flexTime `json:"LastActivityDate"`
	}{alias: (*alias)(u)}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	u.LastActivityDate = aux.LastActivityDate.ptr()
	return nil
}

// UserData is one user's watch state for one item.
type UserData struct {
	Played     bool `json:"Played"`
	PlayCount  int  `json:"PlayCount"`
	IsFavorite bool `json:"IsFavorite"`
	// LastPlayedDate is nil when the item has never been played.
	LastPlayedDate *time.Time `json:"LastPlayedDate"`
}

// UnmarshalJSON accepts every date format Jellyfin has used.
func (u *UserData) UnmarshalJSON(b []byte) error {
	type alias UserData
	aux := struct {
		*alias
		LastPlayedDate flexTime `json:"LastPlayedDate"`
	}{alias: (*alias)(u)}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	u.LastPlayedDate = aux.LastPlayedDate.ptr()
	return nil
}

// MediaStream is one stream in a media source, as Jellyfin reports it.
type MediaStream struct {
	// Type is "Video", "Audio", "Subtitle", "EmbeddedImage" and so on.
	Type     string `json:"Type"`
	Codec    string `json:"Codec"`
	Width    int    `json:"Width"`
	Height   int    `json:"Height"`
	BitRate  int64  `json:"BitRate"`
	Language string `json:"Language"`
	// IsDefault and IsForced are the container's disposition flags.
	IsDefault bool `json:"IsDefault"`
	IsForced  bool `json:"IsForced"`
	// VideoRange is "SDR" or "HDR"; VideoRangeType is the detail ("HDR10",
	// "HLG", "DOVI" and so on). Non-video streams report "Unknown".
	VideoRange     string `json:"VideoRange"`
	VideoRangeType string `json:"VideoRangeType"`
}

// MediaSource is one playable version of an item.
type MediaSource struct {
	ID        string `json:"Id"`
	Path      string `json:"Path"`
	Protocol  string `json:"Protocol"`
	Container string `json:"Container"`
	// Size is in bytes.
	Size int64 `json:"Size"`
	// Bitrate is the overall bitrate in bits per second.
	Bitrate      int64         `json:"Bitrate"`
	VideoType    string        `json:"VideoType"`
	MediaStreams []MediaStream `json:"MediaStreams"`
}

// Item is a movie, episode or other library item. Which fields are filled in
// depends on the fields requested.
type Item struct {
	ID                string `json:"Id"`
	Name              string `json:"Name"`
	SortName          string `json:"SortName"`
	Type              string `json:"Type"`
	Path              string `json:"Path"`
	ParentID          string `json:"ParentId"`
	SeriesID          string `json:"SeriesId"`
	SeriesName        string `json:"SeriesName"`
	SeasonID          string `json:"SeasonId"`
	SeasonName        string `json:"SeasonName"`
	ParentIndexNumber int    `json:"ParentIndexNumber"`
	IndexNumber       int    `json:"IndexNumber"`
	ProductionYear    int    `json:"ProductionYear"`
	// DateCreated is when Jellyfin first saw the item, in UTC.
	DateCreated time.Time `json:"DateCreated"`
	// RunTimeTicks is the runtime in 100 ns ticks; see Runtime.
	RunTimeTicks int64    `json:"RunTimeTicks"`
	LocationType string   `json:"LocationType"`
	VideoType    string   `json:"VideoType"`
	IsFolder     bool     `json:"IsFolder"`
	Tags         []string `json:"Tags"`
	Genres       []string `json:"Genres"`
	// ImageTags maps an image type ("Primary") to its cache tag. A missing
	// Primary entry means the item has no artwork.
	ImageTags    map[string]string `json:"ImageTags"`
	UserData     UserData          `json:"UserData"`
	MediaSources []MediaSource     `json:"MediaSources"`
}

// Runtime converts RunTimeTicks to a duration.
func (it Item) Runtime() time.Duration {
	return time.Duration(it.RunTimeTicks) * 100 * time.Nanosecond
}

// HasPrimaryImage reports whether Jellyfin holds a primary image (poster or
// thumbnail) for the item.
func (it Item) HasPrimaryImage() bool {
	return it.ImageTags["Primary"] != ""
}

// UnmarshalJSON accepts every date format Jellyfin has used.
func (it *Item) UnmarshalJSON(b []byte) error {
	type alias Item
	aux := struct {
		*alias
		DateCreated flexTime `json:"DateCreated"`
	}{alias: (*alias)(it)}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	it.DateCreated = aux.DateCreated.t
	return nil
}

// ItemPage is one page of an item query.
type ItemPage struct {
	Items            []Item `json:"Items"`
	TotalRecordCount int    `json:"TotalRecordCount"`
	StartIndex       int    `json:"StartIndex"`
}

// Collection is a Jellyfin collection (box set).
type Collection struct {
	ID   string `json:"Id"`
	Name string `json:"Name"`
}

// flexTime parses Jellyfin dates. Jellyfin writes seven fractional digits and
// usually a Z, but some versions and some fields omit the zone; those are UTC.
// The .NET minimum date means "never".
type flexTime struct{ t time.Time }

var flexLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05.9999999",
	"2006-01-02T15:04:05",
	"2006-01-02",
}

func (f *flexTime) UnmarshalJSON(b []byte) error {
	if bytes.Equal(b, []byte("null")) {
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("date is not a string: %w", err)
	}
	if s == "" {
		return nil
	}
	for _, layout := range flexLayouts {
		t, err := time.Parse(layout, s)
		if err != nil {
			continue
		}
		if t.Year() <= 1 {
			return nil
		}
		f.t = t.UTC()
		return nil
	}
	return fmt.Errorf("unrecognised date %q", s)
}

func (f flexTime) ptr() *time.Time {
	if f.t.IsZero() {
		return nil
	}
	t := f.t
	return &t
}
