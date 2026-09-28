package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/freakyturtle/jellytrim/internal/jellyfin"
	"github.com/freakyturtle/jellytrim/internal/media"
	"github.com/freakyturtle/jellytrim/internal/plan"
	"github.com/freakyturtle/jellytrim/internal/policy"
	"github.com/freakyturtle/jellytrim/internal/store"
	"github.com/freakyturtle/jellytrim/internal/units"
	"github.com/freakyturtle/jellytrim/internal/web/views"
)

// libraryPageSize is the number of rows per Library page.
const libraryPageSize = 100

// Allowed filter values. Anything else in the query string is ignored.
var (
	libraryOutcomes = []views.Option{
		{Value: "optimise", Label: "Needs optimisation"},
		{Value: "optimal", Label: "Already optimal"},
		{Value: "protected", Label: "Protected"},
		{Value: "skipped", Label: "Skipped"},
		{Value: "no_policy", Label: "No policy"},
		{Value: "pending", Label: "Not inspected"},
	}
	libraryResolutions = []views.Option{{Value: "2160", Label: "4K"}, {Value: "1080", Label: "1080p"}, {Value: "720", Label: "720p"}}
	libraryCodecs      = []views.Option{{Value: "h264", Label: "H.264"}, {Value: "hevc", Label: "HEVC"}, {Value: "av1", Label: "AV1"}}
	libraryWatched     = []views.Option{{Value: "yes", Label: "Watched"}, {Value: "no", Label: "Unwatched"}}
	librarySorts       = []views.Option{{Value: "name", Label: "Title"}, {Value: "size", Label: "Size"}, {Value: "saving", Label: "Est. saving"}}
)

// libraryQuery is the parsed Library toolbar.
type libraryQuery struct {
	Search    string
	LibraryID string
	Outcomes  []string
	Res       []string
	Codecs    []string
	HDR       bool
	Watched   []string
	Sort      string
	Page      int
}

func libraryAllowed(values []string, allowed []views.Option) []string {
	var out []string
	for _, v := range values {
		for _, o := range allowed {
			if v == o.Value && !libraryHas(out, v) {
				out = append(out, v)
			}
		}
	}
	return out
}

func libraryHas(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func libraryParse(q url.Values) libraryQuery {
	lq := libraryQuery{
		Search:    strings.TrimSpace(q.Get("q")),
		LibraryID: q.Get("library"),
		Outcomes:  libraryAllowed(q["outcome"], libraryOutcomes),
		Res:       libraryAllowed(q["res"], libraryResolutions),
		Codecs:    libraryAllowed(q["codec"], libraryCodecs),
		HDR:       q.Get("hdr") == "1",
		Watched:   libraryAllowed(q["watched"], libraryWatched),
		Sort:      "name",
		Page:      1,
	}
	if len(lq.Search) > 200 {
		lq.Search = lq.Search[:200]
	}
	if s := libraryAllowed([]string{q.Get("sort")}, librarySorts); len(s) == 1 {
		lq.Sort = s[0]
	}
	if p, err := strconv.Atoi(q.Get("page")); err == nil && p > 1 {
		lq.Page = p
	}
	return lq
}

// active counts the filters set, for "Filters (2)" and "Clear filters".
func (q libraryQuery) active() int {
	n := len(q.Outcomes) + len(q.Res) + len(q.Codecs) + len(q.Watched)
	if q.HDR {
		n++
	}
	if q.Search != "" {
		n++
	}
	if q.LibraryID != "" {
		n++
	}
	return n
}

// values encodes the query for links, with a page and sort.
func (q libraryQuery) values(page int, sortBy string) url.Values {
	v := url.Values{}
	if q.Search != "" {
		v.Set("q", q.Search)
	}
	if q.LibraryID != "" {
		v.Set("library", q.LibraryID)
	}
	v["outcome"] = q.Outcomes
	v["res"] = q.Res
	v["codec"] = q.Codecs
	v["watched"] = q.Watched
	if q.HDR {
		v.Set("hdr", "1")
	}
	if sortBy != "" && sortBy != "name" {
		v.Set("sort", sortBy)
	}
	if page > 1 {
		v.Set("page", strconv.Itoa(page))
	}
	for k, vals := range v {
		if len(vals) == 0 {
			delete(v, k)
		}
	}
	return v
}

func (q libraryQuery) href(page int, sortBy string) string {
	if enc := q.values(page, sortBy).Encode(); enc != "" {
		return "/library?" + enc
	}
	return "/library"
}

// filters expands the query into store filters. Values in one group widen
// the results; the store takes one value per group, so each combination is
// a separate filter whose results do not overlap.
func (q libraryQuery) filters() []store.LibraryFilter {
	base := store.LibraryFilter{HDR: q.HDR, LibraryID: q.LibraryID, Search: q.Search, Sort: q.Sort}
	if len(q.Watched) == 1 {
		base.Watched = q.Watched[0]
	}
	out := []store.LibraryFilter{base}
	out = libraryExpand(out, q.Outcomes, func(f *store.LibraryFilter, v string) { f.Outcome = v })
	out = libraryExpand(out, q.Res, func(f *store.LibraryFilter, v string) { f.Resolution, _ = strconv.Atoi(v) })
	return libraryExpand(out, q.Codecs, func(f *store.LibraryFilter, v string) { f.Codec = v })
}

func libraryExpand(in []store.LibraryFilter, values []string, set func(*store.LibraryFilter, string)) []store.LibraryFilter {
	if len(values) == 0 {
		return in
	}
	out := make([]store.LibraryFilter, 0, len(in)*len(values))
	for _, f := range in {
		for _, v := range values {
			g := f
			set(&g, v)
			out = append(out, g)
		}
	}
	return out
}

// libraryRows returns one page of rows and the total count.
func (s *Server) libraryRows(ctx context.Context, q libraryQuery) ([]store.LibraryRow, int, error) {
	fs := q.filters()
	offset := (q.Page - 1) * libraryPageSize
	if len(fs) == 1 {
		f := fs[0]
		f.Limit, f.Offset = libraryPageSize, offset
		return s.Store.LibraryList(ctx, f)
	}
	// Several values in a group: fetch enough of each combination, then
	// merge in the store's order.
	need := offset + libraryPageSize
	var all []store.LibraryRow
	total := 0
	for _, f := range fs {
		rows, n, err := s.libraryFetch(ctx, f, need)
		if err != nil {
			return nil, 0, err
		}
		all = append(all, rows...)
		total += n
	}
	sort.SliceStable(all, func(i, j int) bool { return libraryLess(q.Sort, all[i], all[j]) })
	if offset >= len(all) {
		return nil, total, nil
	}
	return all[offset:min(len(all), need)], total, nil
}

// libraryFetch reads the first need rows of one filter, in store-sized chunks.
func (s *Server) libraryFetch(ctx context.Context, f store.LibraryFilter, need int) ([]store.LibraryRow, int, error) {
	const chunk = 500
	var out []store.LibraryRow
	total := 0
	for off := 0; off < need; off += chunk {
		f.Limit, f.Offset = min(chunk, need-off), off
		rows, n, err := s.Store.LibraryList(ctx, f)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, rows...)
		total = n
		if len(rows) < f.Limit {
			break
		}
	}
	return out, total, nil
}

// libraryLess matches the store's ORDER BY for each sort.
func libraryLess(sortBy string, a, b store.LibraryRow) bool {
	switch sortBy {
	case "size":
		if a.Size != b.Size {
			return a.Size > b.Size
		}
	case "saving":
		if sa, sb := libraryMinSaving(a), libraryMinSaving(b); sa != sb {
			return sa > sb
		}
	default:
		if na, nb := strings.ToLower(a.Item.SortName), strings.ToLower(b.Item.SortName); na != nb {
			return na < nb
		}
	}
	return a.Item.ID < b.Item.ID
}

func libraryMinSaving(r store.LibraryRow) int64 {
	if r.EstMax == nil {
		return 0
	}
	return r.Size - *r.EstMax
}

func (s *Server) library(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := libraryParse(r.URL.Query())
	libs, err := s.Store.Libraries(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	filters := libraryFilterView(q, libs)
	if q.LibraryID != "" && !filters.KnownLibrary {
		q.LibraryID = ""
	}
	res, err := s.libraryResults(ctx, q)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-Target") == "library-results" {
		s.render(w, r, views.LibraryResultsSwap(res, filters.Active))
		return
	}
	sh, err := s.shell(r, "Library", "library")
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, views.LibraryPage(views.LibraryPageData{Shell: sh, Filters: filters, Results: res}))
}

func libraryFilterView(q libraryQuery, libs []store.Library) views.LibraryFilters {
	f := views.LibraryFilters{
		Search: q.Search, Library: q.LibraryID, Outcomes: q.Outcomes, Res: q.Res, Codecs: q.Codecs,
		HDR: q.HDR, Watched: q.Watched, Sort: q.Sort, Active: q.active(),
		OutcomeOptions: libraryOutcomes, ResOptions: libraryResolutions, CodecOptions: libraryCodecs,
		WatchedOptions: libraryWatched, SortOptions: librarySorts,
	}
	for _, l := range libs {
		if !l.Managed {
			continue
		}
		f.Libraries = append(f.Libraries, views.Option{Value: l.ID, Label: l.Name})
		if l.ID == q.LibraryID {
			f.KnownLibrary = true
		}
	}
	return f
}

func (s *Server) libraryResults(ctx context.Context, q libraryQuery) (views.LibraryResults, error) {
	res := views.LibraryResults{Sort: q.Sort, Filtered: q.active() > 0}
	totals, err := s.Store.Totals(ctx)
	if err != nil {
		return res, err
	}
	res.Synced = totals.Items > 0
	rows, total, err := s.libraryRows(ctx, q)
	if err != nil {
		return res, err
	}
	res.Total = libraryCount(total)
	if len(rows) > 0 {
		from := (q.Page-1)*libraryPageSize + 1
		res.From, res.To = libraryCount(from), libraryCount(from+len(rows)-1)
	}
	if q.Page > 1 {
		res.PrevHref = q.href(q.Page-1, q.Sort)
	}
	if q.Page*libraryPageSize < total {
		res.NextHref = q.href(q.Page+1, q.Sort)
	}
	res.SortHrefs = map[string]string{}
	for _, o := range librarySorts {
		res.SortHrefs[o.Value] = q.href(1, o.Value)
	}
	for _, row := range rows {
		res.Rows = append(res.Rows, s.libraryRow(ctx, row))
	}
	return res, nil
}

func (s *Server) libraryRow(ctx context.Context, row store.LibraryRow) views.LibraryRow {
	it := row.Item
	v := views.LibraryRow{
		ID:       it.ID,
		Href:     "/library/" + url.PathEscape(it.ID),
		Title:    libraryTitle(it),
		Library:  it.LibraryName,
		HasImage: it.HasImage,
		ImgSrc:   "/img/" + url.PathEscape(it.ID) + "?w=80",
		Initial:  libraryInitial(libraryTitle(it)),
		Size:     units.Bytes(row.Size),
		Badges:   libraryBadges(row.Resolution, row.VideoCodec, row.HDR),
	}
	v.Lamp, v.Decision = libraryOutcome(row.Outcome)
	if row.ProbeError != "" {
		v.Lamp = "bad"
	}
	if row.EstMin != nil && row.EstMax != nil {
		lo, hi := row.Size-*row.EstMax, row.Size-*row.EstMin
		v.Saving = units.Bytes(max((lo+hi)/2, 0))
	}
	v.Change = libraryCurrent(row)
	if row.Outcome == string(plan.Optimise) {
		if ev, err := s.Store.Evaluation(ctx, it.ID); err == nil {
			var p plan.Plan
			if json.Unmarshal([]byte(ev.Plan), &p) == nil && p.SourceLabel != "" {
				v.Change = p.SourceLabel + " → " + p.TargetLabel
			}
		}
	}
	return v
}

func libraryCurrent(row store.LibraryRow) string {
	if !row.Probed || row.ProbeError != "" || row.VideoCodec == "" {
		return ""
	}
	return media.Resolution(row.Resolution).Label() + " " + media.Codec(row.VideoCodec).Label()
}

// libraryTitle is "Title (Year)" for films and "Series · S01E02 · Title"
// for episodes.
func libraryTitle(it store.Item) string {
	if it.SeriesName != "" {
		parts := []string{it.SeriesName}
		code := libraryEpisodeCode(it)
		if code != "" {
			parts = append(parts, code)
		}
		// Without metadata Jellyfin names an episode after its file, which
		// already carries the code: "Show - S01E02".
		if code == "" || !strings.Contains(strings.ToUpper(it.Name), code) {
			parts = append(parts, it.Name)
		}
		return strings.Join(parts, " · ")
	}
	// Without metadata the name is the folder name, "Title (2019)".
	if it.Year != nil && *it.Year > 0 && !strings.HasSuffix(it.Name, "("+strconv.Itoa(*it.Year)+")") {
		return it.Name + " (" + strconv.Itoa(*it.Year) + ")"
	}
	return it.Name
}

func libraryEpisodeCode(it store.Item) string {
	if it.SeasonNumber == nil || it.EpisodeNumber == nil {
		return ""
	}
	return "S" + libraryTwo(*it.SeasonNumber) + "E" + libraryTwo(*it.EpisodeNumber)
}

func libraryTwo(n int) string {
	if n >= 0 && n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

func libraryInitial(title string) string {
	for _, r := range title {
		return strings.ToUpper(string(r))
	}
	return "?"
}

// libraryBadges lists the current format: resolution, codec and HDR type.
func libraryBadges(res int, codec, hdr string) []views.LibraryBadge {
	var out []views.LibraryBadge
	if res > 0 {
		label := media.Resolution(res).Label()
		if res == int(media.Res2160) {
			label = "4K"
		}
		out = append(out, views.LibraryBadge{Text: label})
	}
	if codec != "" {
		out = append(out, views.LibraryBadge{Text: media.Codec(codec).Label()})
	}
	if c := media.HDRClass(hdr); c.IsHDR() {
		out = append(out, views.LibraryBadge{Text: libraryHDRShort(c), Variant: "hdr"})
	}
	return out
}

func libraryHDRShort(c media.HDRClass) string {
	switch c {
	case media.DolbyVisionHDR10:
		return "DV HDR10"
	case media.DolbyVision:
		return "DV"
	}
	return c.Label()
}

// libraryOutcome gives an outcome's lamp and words.
func libraryOutcome(outcome string) (lamp, label string) {
	switch plan.Outcome(outcome) {
	case plan.Optimise:
		return "info", plan.Optimise.Label()
	case plan.AlreadyOptimal:
		return "ok", plan.AlreadyOptimal.Label()
	case plan.Protected:
		return "idle", plan.Protected.Label()
	case plan.Skipped:
		return "warn", plan.Skipped.Label()
	case plan.NoPolicy:
		return "idle", plan.NoPolicy.Label()
	}
	return "idle", "Not inspected"
}

// image proxies an item's Jellyfin artwork, so the browser never talks to
// Jellyfin and never sees its address or key.
func (s *Server) image(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	it, err := s.Store.Item(ctx, id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && !it.HasImage) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if s.Library == nil {
		http.NotFound(w, r)
		return
	}
	body, ctype, err := s.libraryImage(ctx, it.ID, libraryImageWidth(r.URL.Query().Get("w")))
	switch {
	case errors.Is(err, jellyfin.ErrNotFound):
		http.NotFound(w, r)
		return
	case err != nil:
		s.Log.Warn("http: artwork unavailable", "item", it.ID, "err", err)
		http.Error(w, "Artwork unavailable", http.StatusBadGateway)
		return
	}
	defer func() { _ = body.Close() }()
	if !strings.HasPrefix(ctype, "image/") {
		http.Error(w, "Artwork unavailable", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Posters are small; the cap stops a misbehaving server filling memory.
	_, _ = io.Copy(w, io.LimitReader(body, 20<<20))
}

func (s *Server) libraryImage(ctx context.Context, id string, width int) (io.ReadCloser, string, error) {
	c, err := s.Library.Client(ctx)
	if err != nil {
		return nil, "", err
	}
	return c.Image(ctx, id, width)
}

// libraryImageWidth reads ?w=, clamped to 40..600, default 200.
func libraryImageWidth(v string) int {
	n, err := strconv.Atoi(v)
	if err != nil {
		return 200
	}
	return min(max(n, 40), 600)
}

func (s *Server) item(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	it, err := s.Store.Item(ctx, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		s.itemNotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	sh, err := s.shell(r, libraryTitle(it), "library")
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	v, err := s.itemView(ctx, it)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	v.Shell = sh
	v.Inspecting = r.URL.Query().Get("inspecting") == "1" && s.Library != nil && s.Library.Status().Running
	if err := s.itemQueueState(ctx, r, it, sh.DryRun, &v); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, views.ItemPage(v))
}

func (s *Server) itemNotFound(w http.ResponseWriter, r *http.Request) {
	sh, err := s.shell(r, "Item not found", "library")
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	s.render(w, r, views.ItemNotFound(sh))
}

// reprobeItem forgets the item's cached inspection and starts a run, which
// inspects it again and re-evaluates.
func (s *Server) reprobeItem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	it, err := s.Store.Item(ctx, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if s.Library == nil {
		itemRedirect(w, r, it.ID, false)
		return
	}
	if err := s.Library.ReprobeItem(ctx, it.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.Library.RunAsync()
	itemRedirect(w, r, it.ID, true)
}

// itemRedirect sends the browser back to an item page, like redirect.
func itemRedirect(w http.ResponseWriter, r *http.Request, id string, inspecting bool) {
	to := "/library/" + url.PathEscape(id)
	if inspecting {
		to += "?inspecting=1"
	}
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", to)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, to, http.StatusSeeOther) // #nosec G710 -- a same-site path; the ID was looked up in the store and escaped
}

// itemView gathers everything the item page shows.
func (s *Server) itemView(ctx context.Context, it store.Item) (views.ItemPageData, error) {
	v := views.ItemPageData{
		ID: it.ID, Title: libraryTitle(it), Subtitle: itemSubtitle(it), HasImage: it.HasImage,
		ImgSrc: "/img/" + url.PathEscape(it.ID) + "?w=240", Initial: libraryInitial(libraryTitle(it)),
		ReprobeAction: "/library/" + url.PathEscape(it.ID) + "/reprobe",
	}
	ev, err := s.Store.Evaluation(ctx, it.ID)
	evaluated := err == nil
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return v, err
	}
	pr, err := s.Store.Probe(ctx, it.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return v, err
	}
	f := itemFile(pr, &v)
	v.Lamp, v.Outcome = libraryOutcome(ev.Outcome)
	v.Summary = ev.Summary
	if !evaluated && it.SyncSkipReason == "" {
		v.Summary = "Not inspected yet. The next sync inspects this file."
	}
	v.Badges = libraryBadges(pr.Resolution, pr.VideoCodec, pr.HDR)
	if v.Jellyfin, err = s.itemJellyfin(ctx, it); err != nil {
		return v, err
	}
	if f != nil {
		v.Probed = true
		v.Source, v.HDRFacts = itemSource(it, pr, f)
		v.Audio, v.Subtitles, v.Extras = itemStreams(f)
	}
	v.Policy = itemPolicy(ev, evaluated)
	if plan.Outcome(ev.Outcome) == plan.Optimise {
		var p plan.Plan
		if json.Unmarshal([]byte(ev.Plan), &p) == nil && p.SourceSize > 0 {
			v.Proposed = s.itemProposed(p, f)
			v.Estimate = itemEstimate(p)
		}
		v.Optimise = views.ItemOptimise{Show: true}
	}
	return v, nil
}

// itemFile parses the cached probe, recording any inspection error on v.
func itemFile(pr store.Probe, v *views.ItemPageData) *media.File {
	if pr.Error != "" {
		v.ProbeError = pr.Error
		return nil
	}
	if pr.ProbeJSON == "" {
		return nil
	}
	f, err := media.Parse([]byte(pr.ProbeJSON), []byte(pr.FrameJSON), pr.Size)
	if err != nil {
		v.ProbeError = err.Error()
		return nil
	}
	return f
}

func itemSubtitle(it store.Item) string {
	parts := []string{itemType(it.Type)}
	if it.SeasonName != "" {
		parts = append(parts, it.SeasonName)
	}
	if it.LibraryName != "" {
		parts = append(parts, it.LibraryName)
	}
	return strings.Join(parts, " · ")
}

func itemType(t string) string {
	switch t {
	case "Movie":
		return "Film"
	case "Episode":
		return "Episode"
	case "":
		return "Item"
	}
	return t
}

// itemDate formats a date as "4 Oct 2026 (143 days ago)".
func (s *Server) itemDate(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.Format("2 Jan 2006") + " (" + units.Ago(*t, s.Now()) + ")"
}

func (s *Server) itemJellyfin(ctx context.Context, it store.Item) ([]views.ItemStat, error) {
	out := []views.ItemStat{{Label: "Type", Value: itemType(it.Type)}, {Label: "Library", Value: it.LibraryName}}
	if it.SeriesName != "" {
		out = append(out, views.ItemStat{Label: "Series", Value: it.SeriesName}, views.ItemStat{Label: "Season", Value: it.SeasonName})
		if code := libraryEpisodeCode(it); code != "" {
			out = append(out, views.ItemStat{Label: "Episode", Value: code, Mono: true})
		}
	}
	out = append(out, views.ItemStat{Label: "Added", Value: s.itemDate(it.DateAdded)})
	watch, err := s.itemWatch(ctx, it.ID)
	if err != nil {
		return nil, err
	}
	out = append(out, watch...)
	out = append(out,
		views.ItemStat{Label: "Tags", Value: itemList(it.Tags)},
		views.ItemStat{Label: "Genres", Value: itemList(it.Genres)},
		views.ItemStat{Label: "Jellyfin path", Value: it.JellyfinPath, Mono: true},
	)
	return out, nil
}

func itemList(vs []string) string {
	if len(vs) == 0 {
		return "None"
	}
	return strings.Join(vs, ", ")
}

// itemWatch describes watch state for each user whose state counts.
func (s *Server) itemWatch(ctx context.Context, itemID string) ([]views.ItemStat, error) {
	users, err := s.Store.SelectedUsers(ctx)
	if err != nil {
		return nil, err
	}
	if len(users) == 0 {
		return []views.ItemStat{{Label: "Watched", Value: "No users chosen. Choose whose watch state counts in Settings."}}, nil
	}
	all, err := s.Store.AllUserData(ctx)
	if err != nil {
		return nil, err
	}
	byUser := map[string]store.UserData{}
	for _, d := range all[itemID] {
		byUser[d.UserID] = d
	}
	var out []views.ItemStat
	plays := 0
	var favs []string
	var last *time.Time
	for _, u := range users {
		d := byUser[u.ID]
		value := "Not watched"
		if d.Played {
			value = "Watched"
			if d.LastPlayedAt != nil {
				value += ", " + units.Ago(*d.LastPlayedAt, s.Now())
			}
		}
		out = append(out, views.ItemStat{Label: "Watched by " + u.Name, Value: value})
		plays += d.PlayCount
		if d.Favourite {
			favs = append(favs, u.Name)
		}
		if d.LastPlayedAt != nil && (last == nil || d.LastPlayedAt.After(*last)) {
			last = d.LastPlayedAt
		}
	}
	lastText := "Never"
	if last != nil {
		lastText = s.itemDate(last)
	}
	fav := "No"
	if len(favs) > 0 {
		fav = "Yes (" + strings.Join(favs, ", ") + ")"
	}
	return append(out,
		views.ItemStat{Label: "Last watched", Value: lastText},
		views.ItemStat{Label: "Play count", Value: strconv.Itoa(plays)},
		views.ItemStat{Label: "Favourite", Value: fav},
	), nil
}

// itemSource describes the file as inspected, and the facts behind its HDR
// classification.
func itemSource(it store.Item, pr store.Probe, f *media.File) ([]views.ItemStat, []string) {
	path := it.LocalPath
	if path == "" {
		path = pr.LocalPath
	}
	out := []views.ItemStat{{Label: "Local path", Value: path, Mono: true}}
	switch {
	case pr.IsSymlink:
		out = append(out, views.ItemStat{Label: "Links", Value: "A symbolic link to another file"})
	case pr.Nlink > 1:
		out = append(out, views.ItemStat{Label: "Links", Value: "Hard-linked: this file has " + strconv.Itoa(pr.Nlink) + " names on disk"})
	}
	out = append(out,
		views.ItemStat{Label: "Container", Value: itemContainer(f.Container, f.FormatName), Mono: true},
		views.ItemStat{Label: "Size", Value: units.Bytes(f.Size), Mono: true},
		views.ItemStat{Label: "Duration", Value: itemDuration(f.Duration), Mono: true},
	)
	v, ok := f.MainVideo()
	if !ok {
		return append(out, views.ItemStat{Label: "Video", Value: "No video stream"}), nil
	}
	codec := v.Codec.Label()
	if v.Profile != "" {
		codec += " (" + v.Profile + ")"
	}
	scan := "Progressive"
	if v.Interlaced() {
		scan = "Interlaced"
	}
	out = append(out,
		views.ItemStat{Label: "Video codec", Value: codec, Mono: true},
		views.ItemStat{Label: "Resolution", Value: itemDims(v.Width, v.Height) + " (" + v.Resolution().Label() + ")", Mono: true},
		views.ItemStat{Label: "Bit depth", Value: itemBitDepth(v.BitDepth), Mono: true},
		views.ItemStat{Label: "Frame rate", Value: itemFrameRate(v.FrameRate), Mono: true},
		views.ItemStat{Label: "Scan", Value: scan},
		views.ItemStat{Label: "Video bitrate", Value: itemBitrate(f)},
	)
	hdr := f.HDR()
	out = append(out, views.ItemStat{Label: "Dynamic range", Value: hdr.Class.Label()})
	return out, hdr.Facts
}

func itemContainer(c media.Container, formatName string) string {
	switch c {
	case media.Matroska:
		return "Matroska (MKV)"
	case media.MP4:
		return "MP4"
	}
	return formatName
}

func itemDuration(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	return units.Duration(d)
}

func itemDims(w, h int) string {
	if w <= 0 || h <= 0 {
		return "Unknown size"
	}
	return strconv.Itoa(w) + " × " + strconv.Itoa(h)
}

func itemBitDepth(bits int) string {
	if bits <= 0 {
		return ""
	}
	return strconv.Itoa(bits) + "-bit"
}

func itemFrameRate(fps float64) string {
	if fps <= 0 {
		return ""
	}
	return strconv.FormatFloat(float64(int(fps*1000+0.5))/1000, 'f', -1, 64) + " fps"
}

func itemBitrate(f *media.File) string {
	bps, known := f.VideoBitrate()
	if !known {
		return "Unknown: neither the stream nor the file size gives it"
	}
	return units.Bitrate(bps) + ", " + f.VideoBitrateSource().Label()
}

// itemStreams lists audio and subtitle streams, and counts the rest.
func itemStreams(f *media.File) ([]views.ItemStream, []views.ItemStream, []views.ItemStat) {
	var audio, subs []views.ItemStream
	for _, a := range f.Audio {
		codec := a.Codec
		if a.Profile != "" {
			codec += " (" + a.Profile + ")"
		}
		channels := a.Layout
		if channels == "" && a.Channels > 0 {
			channels = strconv.Itoa(a.Channels)
		}
		flags := itemFlags(a.Disposition, false)
		if a.Commentary() && !a.Disposition.Comment {
			flags = append(flags, "Commentary")
		}
		audio = append(audio, views.ItemStream{
			Index: strconv.Itoa(a.Index), Codec: codec, Channels: channels, Language: itemLanguage(a.Language),
			Title: a.Title, Flags: strings.Join(flags, ", "),
		})
	}
	for _, sub := range f.Subtitles {
		subs = append(subs, views.ItemStream{
			Index: strconv.Itoa(sub.Index), Codec: sub.Codec, Language: itemLanguage(sub.Language),
			Title: sub.Title, Flags: strings.Join(itemFlags(sub.Disposition, true), ", "),
		})
	}
	fonts := 0
	for _, a := range f.Attachments {
		if strings.Contains(a.MimeType, "font") || strings.HasSuffix(strings.ToLower(a.FileName), ".ttf") ||
			strings.HasSuffix(strings.ToLower(a.FileName), ".otf") {
			fonts++
		}
	}
	attach := strconv.Itoa(len(f.Attachments))
	if fonts > 0 {
		attach += " (" + strconv.Itoa(fonts) + " fonts)"
	}
	extras := []views.ItemStat{
		{Label: "Attachments", Value: attach, Mono: true},
		{Label: "Cover art", Value: strconv.Itoa(len(f.CoverArt())), Mono: true},
		{Label: "Chapters", Value: strconv.Itoa(f.Chapters), Mono: true},
	}
	if len(f.Data) > 0 {
		extras = append(extras, views.ItemStat{Label: "Data streams", Value: strconv.Itoa(len(f.Data)), Mono: true})
	}
	return audio, subs, extras
}

// itemFlags names a stream's flags in words. Subtitles call hearing
// impaired "SDH", as players do.
func itemFlags(d media.Disposition, subtitle bool) []string {
	var out []string
	if d.Default {
		out = append(out, "Default")
	}
	if d.Forced {
		out = append(out, "Forced")
	}
	if d.HearingImpaired {
		if subtitle {
			out = append(out, "SDH")
		} else {
			out = append(out, "Hearing impaired")
		}
	}
	if d.VisualImpaired {
		out = append(out, "Audio description")
	}
	if d.Comment {
		out = append(out, "Commentary")
	}
	if d.Original {
		out = append(out, "Original")
	}
	return out
}

func itemLanguage(l string) string {
	if l == "" || l == "und" {
		return "Unknown"
	}
	return l
}

// itemPolicy explains the decision: the winning policy, other matches, and
// the policies that did not match.
func itemPolicy(ev store.Evaluation, evaluated bool) views.ItemPolicy {
	p := views.ItemPolicy{Evaluated: evaluated}
	if !evaluated {
		return p
	}
	var matches []policy.Match
	_ = json.Unmarshal([]byte(ev.Explanation), &matches)
	var reasons []plan.Reason
	_ = json.Unmarshal([]byte(ev.Reasons), &reasons)
	for _, r := range reasons {
		p.Reasons = append(p.Reasons, views.ExplainLine{Text: r.Text})
	}
	var winner *policy.Match
	for i, m := range matches {
		switch {
		case ev.PolicyID != nil && m.PolicyID == *ev.PolicyID:
			winner = &matches[i]
		case m.Matched && m.Enabled:
			p.Also = append(p.Also, m.PolicyName+" (lower in the list, so it does not apply)")
		case m.Matched:
			p.Off = append(p.Off, m.PolicyName+" matches, but it is turned off")
		default:
			p.Unmatched = append(p.Unmatched, views.ItemPolicyMatch{Name: m.PolicyName, Enabled: m.Enabled, Lines: itemLines(m.Lines)})
		}
	}
	p.Heading = itemPolicyHeading(plan.Outcome(ev.Outcome), winner)
	if winner != nil {
		p.Winner = winner.PolicyName
		p.Lines = itemLines(winner.Lines)
	}
	if o := plan.Outcome(ev.Outcome); o == plan.AlreadyOptimal && ev.Summary != "" {
		p.Lines = append(p.Lines, views.ExplainLine{Neutral: true, Text: ev.Summary})
	}
	return p
}

func itemPolicyHeading(o plan.Outcome, winner *policy.Match) string {
	switch {
	case o == plan.Skipped:
		return "Skipped"
	case o == plan.Protected && winner == nil:
		return "Left alone at your request"
	case o == plan.NoPolicy || winner == nil:
		return "No enabled policy matches"
	case o == plan.Protected:
		return "Protected by " + winner.PolicyName
	}
	return "Matches " + winner.PolicyName
}

func itemLines(ls []policy.Line) []views.ExplainLine {
	out := make([]views.ExplainLine, 0, len(ls))
	for _, l := range ls {
		out = append(out, views.ExplainLine{Pass: l.Pass, Text: l.Text})
	}
	return out
}

// itemProposed describes the planned output in plain words.
func (s *Server) itemProposed(p plan.Plan, f *media.File) []views.ItemStat {
	target := media.ClassOf(p.Width, p.Height).Label()
	res := itemDims(p.Width, p.Height) + " (" + target + ", kept)"
	if p.Scale {
		from := strings.Fields(p.SourceLabel)
		res = itemDims(p.Width, p.Height) + " (" + target
		if len(from) > 0 {
			res += ", from " + from[0]
		}
		res += ")"
	}
	out := []views.ItemStat{
		{Label: "Resolution", Value: res, Mono: true},
		{Label: "Codec", Value: p.Codec.Label(), Mono: true},
		{Label: "Quality", Value: policy.QualityLabel(p.Quality)},
		{Label: "Encoder", Value: s.itemEncoder(p.Encoder)},
		{Label: "Bit depth", Value: itemBitDepth(p.BitDepth), Mono: true},
		{Label: "Dynamic range", Value: itemHDRPlan(p)},
		{Label: "Container", Value: itemContainer(p.Container, string(p.Container)), Mono: true},
	}
	audio, subs := 0, 0
	if f != nil {
		audio, subs = len(f.Audio), len(f.Subtitles)
	}
	return append(out,
		views.ItemStat{Label: "Audio", Value: itemPreserve(audio, "track", "tracks")},
		views.ItemStat{Label: "Subtitles", Value: itemPreserve(subs, "subtitle", "subtitles")},
	)
}

func itemHDRPlan(p plan.Plan) string {
	switch {
	case p.ReduceHDR:
		return "Reduce to " + p.HDR.Label()
	case p.HDR.IsHDR():
		return "Keep " + p.HDR.Label() + " and its metadata"
	}
	return "SDR, as the source"
}

func itemPreserve(n int, one, many string) string {
	switch n {
	case 0:
		return "None in the source"
	case 1:
		return "Preserve the only " + one
	}
	return "Preserve all " + strconv.Itoa(n) + " " + many
}

// itemEncoder names a backend the way the hardware test does.
func (s *Server) itemEncoder(name string) string {
	if s.Hardware != nil {
		for _, c := range s.Hardware.Capabilities() {
			if c.Backend == name && c.Label != "" {
				return c.Label
			}
		}
	}
	if name == "" {
		return "Chosen when the job starts"
	}
	return name
}

// itemEstimate is the size range with a bar comparing it to the source.
func itemEstimate(p plan.Plan) *views.ItemEstimate {
	lo, hi := p.EstSaving()
	e := &views.ItemEstimate{
		Current: units.Bytes(p.SourceSize),
		Range:   dashRange(p.EstMin, p.EstMax),
		Saving:  dashRange(lo, hi),
	}
	if p.SourceSize > 0 {
		e.LoPercent = int(min(max(p.EstMin*100/p.SourceSize, 1), 100))
		e.HiPercent = int(min(max(p.EstMax*100/p.SourceSize, int64(e.LoPercent)), 100))
	}
	return e
}
