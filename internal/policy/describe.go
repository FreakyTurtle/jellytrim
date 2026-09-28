package policy

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/freakyturtle/jellytrim/internal/media"
)

// Names resolves Jellyfin IDs to display names for descriptions.
type Names interface {
	Library(id string) string
	Series(id string) string
	Collection(id string) string
}

// Describe renders a policy as one plain-English sentence, for example:
// "In Movies, when watched, last watched more than 90 days ago and not a
// favourite: convert to 1080p HEVC at High quality."
func Describe(p Policy, n Names) string {
	var b strings.Builder
	scope := DescribeScope(p.Scope, n)
	conds := make([]string, 0, len(p.Conditions.All))
	for _, c := range p.Conditions.All {
		conds = append(conds, DescribeCondition(c))
	}
	b.WriteString(scope)
	if len(conds) > 0 {
		b.WriteString(", when ")
		b.WriteString(joinAnd(conds))
	}
	b.WriteString(": ")
	b.WriteString(DescribeAction(p.Action))
	b.WriteString(".")
	return b.String()
}

// DescribeScope renders the scope, for example "In Movies" or "Everything".
func DescribeScope(s Scope, n Names) string {
	var parts []string
	if len(s.Libraries) > 0 {
		parts = append(parts, "In "+joinOr(mapNames(s.Libraries, n.Library)))
	}
	if len(s.Series) > 0 {
		parts = append(parts, "for "+joinOr(mapNames(s.Series, n.Series)))
	}
	if len(s.Seasons) > 0 {
		var ss []string
		for _, x := range s.Seasons {
			ss = append(ss, strconv.Itoa(x))
		}
		label := "season "
		if len(ss) > 1 {
			label = "seasons "
		}
		parts = append(parts, label+joinAnd(ss))
	}
	if len(s.Collections) > 0 {
		parts = append(parts, "in the collection "+joinOr(mapNames(s.Collections, n.Collection)))
	}
	if len(s.Types) == 1 {
		switch s.Types[0] {
		case "Movie":
			parts = append(parts, "for films")
		case "Episode":
			parts = append(parts, "for episodes")
		}
	}
	if len(parts) == 0 {
		return "In every managed library"
	}
	out := strings.Join(parts, ", ")
	return strings.ToUpper(out[:1]) + out[1:]
}

func mapNames(ids []string, f func(string) string) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		name := f(id)
		if name == "" {
			name = "an unknown item"
		}
		out[i] = name
	}
	return out
}

// DescribeCondition renders one condition in words.
func DescribeCondition(c Condition) string {
	yes := c.Bool != nil && *c.Bool
	switch c.Field {
	case FieldWatched:
		if yes {
			return "watched"
		}
		return "not watched"
	case FieldFavourite:
		if yes {
			return "a favourite"
		}
		return "not a favourite"
	case FieldLastWatched:
		return fmt.Sprintf("last watched %s %d days ago", opWords(c.Op), int(c.Number))
	case FieldAdded:
		return fmt.Sprintf("added %s %d days ago", opWords(c.Op), int(c.Number))
	case FieldResolution:
		r, _ := ParseResolution(c.Text)
		return "resolution " + opWords(c.Op) + " " + r.Label()
	case FieldCodec:
		return "codec " + opWords(c.Op) + " " + codecLabels(c.List)
	case FieldBitrate:
		return "bitrate " + opWords(c.Op) + " " + trimNumber(c.Number) + " Mbps"
	case FieldSize:
		return "size " + opWords(c.Op) + " " + trimNumber(c.Number) + " GB"
	case FieldHDR:
		var names []string
		for _, v := range c.List {
			names = append(names, media.HDRClass(v).Label())
		}
		return "dynamic range " + opWords(c.Op) + " " + joinOr(names)
	case FieldTag:
		return opWords(c.Op) + " tag " + joinOr(quote(c.List))
	case FieldGenre:
		return "genre " + opWords(c.Op) + " " + joinOr(c.List)
	}
	return c.Field
}

func opWords(op string) string {
	switch op {
	case OpMoreThanDays:
		return "more than"
	case OpLessThanDays:
		return "less than"
	case OpAbove:
		return "above"
	case OpBelow:
		return "below"
	case OpAtMost:
		return "at most"
	case OpIsNot:
		return "is not"
	case OpHas:
		return "has"
	case OpHasNot:
		return "does not have"
	}
	return "is"
}

// DescribeAction renders the action, for example "convert to 1080p HEVC at
// High quality".
func DescribeAction(a Action) string {
	if a.Kind == KindProtect {
		return "never change these files"
	}
	var target []string
	if r := a.MaxRes(); r != 0 {
		target = append(target, r.Label())
	}
	if c := a.TargetCodec(); c != "" {
		target = append(target, c.Label())
	}
	s := "convert to " + strings.Join(target, " ")
	if a.MaxRes() != 0 && a.TargetCodec() == "" {
		s = "reduce to at most " + a.MaxRes().Label() + ", keeping the codec"
	} else if a.MaxRes() == 0 {
		s += ", keeping the resolution"
	}
	return s + " at " + QualityLabel(a.Quality) + " quality"
}

// QualityLabel is the user-facing name of a quality tier.
func QualityLabel(q string) string {
	switch q {
	case QualityMaximum:
		return "Maximum"
	case QualityBalanced:
		return "Balanced"
	case QualitySpaceSaver:
		return "Space Saver"
	}
	return "High"
}

func quote(list []string) []string {
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = `"` + s + `"`
	}
	return out
}

func trimNumber(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func joinAnd(items []string) string { return joinWith(items, "and") }
func joinOr(items []string) string  { return joinWith(items, "or") }

func joinWith(items []string, word string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " " + word + " " + items[len(items)-1]
}

// Starter returns the starter policies offered by the setup wizard. Only
// "Protect favourites" is enabled: it changes nothing on disk.
func Starter() []Policy {
	return []Policy{
		{
			Name: "Protect favourites", Enabled: true, Priority: 10,
			Scope:      Scope{Version: 1},
			Conditions: Conditions{Version: 1, All: []Condition{{Field: FieldFavourite, Op: OpIs, Bool: B(true)}}},
			Action:     Action{Version: 1, Kind: KindProtect},
		},
		{
			Name: "Archive watched 4K", Priority: 20,
			Scope: Scope{Version: 1},
			Conditions: Conditions{Version: 1, All: []Condition{
				{Field: FieldWatched, Op: OpIs, Bool: B(true)},
				{Field: FieldLastWatched, Op: OpMoreThanDays, Number: 90},
				{Field: FieldFavourite, Op: OpIs, Bool: B(false)},
				{Field: FieldResolution, Op: OpAbove, Text: "1080p"},
			}},
			Action: Action{Version: 1, Kind: KindOptimise, MaxResolution: "1080p", Codec: "hevc", Quality: QualityHigh, Encoder: "auto", Audio: "preserve", Subtitles: "preserve"},
		},
		{
			Name: "Space-saving television", Priority: 30,
			Scope:      Scope{Version: 1, Types: []string{"Episode"}},
			Conditions: Conditions{Version: 1, All: []Condition{{Field: FieldResolution, Op: OpAbove, Text: "720p"}}},
			Action:     Action{Version: 1, Kind: KindOptimise, MaxResolution: "720p", Codec: "hevc", Quality: QualityBalanced, Encoder: "auto", Audio: "preserve", Subtitles: "preserve"},
		},
		{
			Name: "Efficient encoding", Priority: 40,
			Scope:      Scope{Version: 1},
			Conditions: Conditions{Version: 1, All: []Condition{{Field: FieldCodec, Op: OpIs, List: []string{"h264"}}}},
			Action:     Action{Version: 1, Kind: KindOptimise, MaxResolution: "keep", Codec: "hevc", Quality: QualityHigh, Encoder: "auto", Audio: "preserve", Subtitles: "preserve"},
		},
	}
}
