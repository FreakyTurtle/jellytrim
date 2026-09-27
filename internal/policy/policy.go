// Package policy holds JellyTrim's policy model and evaluates policies
// against library items, explaining every decision. It is pure: callers pass
// in an item snapshot and the current time. See docs/POLICIES.md.
package policy

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/freakyturtle/jellytrim/internal/media"
)

// Policy is one user-defined rule.
type Policy struct {
	ID         int64
	Name       string
	Enabled    bool
	Priority   int // lower runs first
	Scope      Scope
	Conditions Conditions
	Action     Action
}

// Scope limits what a policy applies to. Empty lists mean "any".
type Scope struct {
	Version     int      `json:"version"`
	Libraries   []string `json:"libraries,omitempty"`   // Jellyfin library IDs
	Types       []string `json:"types,omitempty"`       // "Movie", "Episode"
	Series      []string `json:"series,omitempty"`      // Jellyfin series IDs
	Seasons     []int    `json:"seasons,omitempty"`     // season numbers
	Collections []string `json:"collections,omitempty"` // Jellyfin collection IDs
}

// Conditions must all pass for a policy to match.
type Conditions struct {
	Version int         `json:"version"`
	All     []Condition `json:"all"`
}

// Field names for conditions.
const (
	FieldWatched     = "watched"
	FieldFavourite   = "favourite"
	FieldLastWatched = "last_watched"
	FieldAdded       = "added"
	FieldResolution  = "resolution"
	FieldCodec       = "codec"
	FieldBitrate     = "bitrate" // Mbps
	FieldSize        = "size"    // GB
	FieldHDR         = "hdr"
	FieldTag         = "tag"
	FieldGenre       = "genre"
)

// Operators.
const (
	OpIs           = "is"
	OpIsNot        = "is_not"
	OpMoreThanDays = "more_than_days"
	OpLessThanDays = "less_than_days"
	OpAbove        = "above"
	OpBelow        = "below"
	OpAtMost       = "at_most"
	OpHas          = "has"
	OpHasNot       = "has_not"
)

// Condition is one test. Which value field is used depends on Field:
// Bool for watched and favourite; Number for days, Mbps and GB; Text for a
// resolution such as "1080p"; List for codecs, HDR classes, tags and genres.
type Condition struct {
	Field  string   `json:"field"`
	Op     string   `json:"op"`
	Bool   *bool    `json:"bool,omitempty"`
	Number float64  `json:"number,omitempty"`
	Text   string   `json:"text,omitempty"`
	List   []string `json:"list,omitempty"`
}

// Action kinds.
const (
	KindOptimise = "optimise"
	KindProtect  = "protect"
)

// Quality tiers.
const (
	QualityMaximum    = "maximum"
	QualityHigh       = "high"
	QualityBalanced   = "balanced"
	QualitySpaceSaver = "space_saver"
)

// Action is what a matching policy asks for. It states intent; internal/plan
// decides whether and how it can be done safely.
type Action struct {
	Version           int    `json:"version"`
	Kind              string `json:"kind"`
	MaxResolution     string `json:"max_resolution,omitempty"` // "keep", "2160p", "1080p", "720p", "480p"
	Codec             string `json:"codec,omitempty"`          // "keep", "hevc", "h264", "av1"
	Quality           string `json:"quality,omitempty"`
	Encoder           string `json:"encoder,omitempty"` // "auto" or a backend name
	Audio             string `json:"audio,omitempty"`   // "preserve"
	Subtitles         string `json:"subtitles,omitempty"`
	AllowHDRReduction bool   `json:"allow_hdr_reduction,omitempty"`
}

// MaxRes returns the resolution cap, or 0 for "keep".
func (a Action) MaxRes() media.Resolution {
	r, _ := ParseResolution(a.MaxResolution)
	return r
}

// TargetCodec returns the codec asked for, or "" for "keep existing".
func (a Action) TargetCodec() media.Codec {
	switch a.Codec {
	case "", "keep":
		return ""
	}
	return media.Codec(a.Codec)
}

// ParseResolution reads "1080p" style values. "keep" and "" give 0.
func ParseResolution(s string) (media.Resolution, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "keep":
		return 0, nil
	case "480p":
		return media.Res480, nil
	case "576p":
		return media.Res576, nil
	case "720p":
		return media.Res720, nil
	case "1080p":
		return media.Res1080, nil
	case "1440p":
		return media.Res1440, nil
	case "2160p", "4k":
		return media.Res2160, nil
	case "4320p", "8k":
		return media.Res4320, nil
	}
	return 0, fmt.Errorf("unknown resolution %q", s)
}

// UserState is one Jellyfin user's view of an item.
type UserState struct {
	UserID     string
	Name       string
	Played     bool
	Favourite  bool
	LastPlayed *time.Time
}

// WatchMode says how several users' watch states combine.
type WatchMode string

// Watch modes.
const (
	WatchAny WatchMode = "any"
	WatchAll WatchMode = "all"
)

// Item is the snapshot a policy is evaluated against.
type Item struct {
	ID           string
	Name         string
	Type         string // "Movie" or "Episode"
	LibraryID    string
	LibraryName  string
	SeriesID     string
	SeriesName   string
	SeasonNumber *int
	Collections  []string
	Tags         []string
	Genres       []string
	DateAdded    *time.Time
	Users        []UserState // only the users whose watch state counts
	WatchMode    WatchMode
	File         *media.File // nil until probed
	Size         int64       // bytes on disk, when known without a probe
}

// ErrInvalidPolicy is returned by Validate.
var ErrInvalidPolicy = errors.New("invalid policy")

// Validate checks that a policy can be evaluated and stored.
func (p Policy) Validate() error {
	var problems []string
	if strings.TrimSpace(p.Name) == "" {
		problems = append(problems, "it needs a name")
	}
	for i, c := range p.Conditions.All {
		if err := c.validate(); err != nil {
			problems = append(problems, fmt.Sprintf("condition %d: %v", i+1, err))
		}
	}
	if err := p.Action.validate(); err != nil {
		problems = append(problems, err.Error())
	}
	if len(problems) > 0 {
		return fmt.Errorf("%w: %s", ErrInvalidPolicy, strings.Join(problems, "; "))
	}
	return nil
}

func (a Action) validate() error {
	switch a.Kind {
	case KindProtect:
		return nil
	case KindOptimise:
	default:
		return fmt.Errorf("unknown action %q", a.Kind)
	}
	if _, err := ParseResolution(a.MaxResolution); err != nil {
		return err
	}
	switch a.Codec {
	case "", "keep", "hevc", "h264", "av1":
	default:
		return fmt.Errorf("unknown codec %q", a.Codec)
	}
	switch a.Quality {
	case "", QualityMaximum, QualityHigh, QualityBalanced, QualitySpaceSaver:
	default:
		return fmt.Errorf("unknown quality %q", a.Quality)
	}
	if a.MaxResolution == "keep" || a.MaxResolution == "" {
		if a.Codec == "keep" || a.Codec == "" {
			return errors.New("an optimise action must change the codec or cap the resolution")
		}
	}
	return nil
}

func (c Condition) validate() error {
	ops := map[string][]string{
		FieldWatched:     {OpIs},
		FieldFavourite:   {OpIs},
		FieldLastWatched: {OpMoreThanDays, OpLessThanDays},
		FieldAdded:       {OpMoreThanDays, OpLessThanDays},
		FieldResolution:  {OpAbove, OpAtMost, OpIs},
		FieldCodec:       {OpIs, OpIsNot},
		FieldBitrate:     {OpAbove, OpBelow},
		FieldSize:        {OpAbove, OpBelow},
		FieldHDR:         {OpIs, OpIsNot},
		FieldTag:         {OpHas, OpHasNot},
		FieldGenre:       {OpIs, OpIsNot},
	}
	allowed, ok := ops[c.Field]
	if !ok {
		return fmt.Errorf("unknown field %q", c.Field)
	}
	if !contains(allowed, c.Op) {
		return fmt.Errorf("%s cannot use %q", c.Field, c.Op)
	}
	switch c.Field {
	case FieldWatched, FieldFavourite:
		if c.Bool == nil {
			return errors.New("needs yes or no")
		}
	case FieldLastWatched, FieldAdded, FieldBitrate, FieldSize:
		if c.Number <= 0 {
			return errors.New("needs a number above zero")
		}
	case FieldResolution:
		if r, err := ParseResolution(c.Text); err != nil || r == 0 {
			return fmt.Errorf("needs a resolution such as 1080p, got %q", c.Text)
		}
	default:
		if len(c.List) == 0 {
			return errors.New("needs at least one value")
		}
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// B returns a pointer to b, for building conditions.
func B(b bool) *bool { return &b }
