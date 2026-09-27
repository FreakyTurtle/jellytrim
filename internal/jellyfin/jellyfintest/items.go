package jellyfintest

import (
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// allFields is what a single-item request returns: Jellyfin sends the full
// item there whatever fields asks for.
var allFields = map[string]bool{
	"path": true, "mediasources": true, "datecreated": true, "tags": true,
	"genres": true, "providerids": true, "parentid": true, "sortname": true,
}

// renderOpts says which optional parts of an item to send.
type renderOpts struct {
	fields   map[string]bool
	userID   string
	userData bool
	images   bool
}

func optsFromQuery(q url.Values, userID string) renderOpts {
	o := renderOpts{
		fields:   map[string]bool{},
		userID:   userID,
		userData: userID != "" && !strings.EqualFold(q.Get("enableUserData"), "false"),
		images:   !strings.EqualFold(q.Get("enableImages"), "false"),
	}
	for _, f := range splitList(q.Get("fields")) {
		o.fields[f] = true
	}
	return o
}

func splitList(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (s *Server) listItems(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	s.mu.Lock()
	defer s.mu.Unlock()
	userID := q.Get("userId")
	if userID != "" && !s.hasUserLocked(userID) {
		// Jellyfin 12.1 answers an unknown userId with a plain-text 404.
		http.Error(w, "Error processing request.", http.StatusNotFound)
		return
	}
	s.applyMutationsLocked()
	s.itemPages++

	matches := s.matchLocked(q)
	total := len(matches)
	start, _ := strconv.Atoi(q.Get("startIndex"))
	start = min(max(start, 0), total)
	end := total
	if limit, err := strconv.Atoi(q.Get("limit")); err == nil && limit >= 0 {
		end = min(start+limit, total)
	}
	opts := optsFromQuery(q, userID)
	page := make([]map[string]any, 0, end-start)
	for _, it := range matches[start:end] {
		page = append(page, s.renderLocked(it, opts))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"Items":            page,
		"TotalRecordCount": total,
		"StartIndex":       start,
	})
}

func (s *Server) applyMutationsLocked() {
	var keep []mutation
	for _, m := range s.mutations {
		if s.itemPages >= m.afterPages {
			m.apply(s)
			continue
		}
		keep = append(keep, m)
	}
	s.mutations = keep
}

// matchLocked filters and sorts items (and collections, as BoxSet items)
// the way /Items does, by SortName then Id.
func (s *Server) matchLocked(q url.Values) []*Item {
	types := splitList(q.Get("includeItemTypes"))
	parentID := normaliseID(q.Get("parentId"))
	recursive := strings.EqualFold(q.Get("recursive"), "true")
	var out []*Item
	for _, it := range s.candidatesLocked() {
		if len(types) > 0 && !slices.Contains(types, strings.ToLower(it.Type)) {
			continue
		}
		if parentID != "" && !s.underLocked(it, parentID, recursive) {
			continue
		}
		out = append(out, it)
	}
	slices.SortFunc(out, func(a, b *Item) int {
		if c := strings.Compare(strings.ToLower(a.SortName), strings.ToLower(b.SortName)); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out
}

func (s *Server) candidatesLocked() []*Item {
	out := make([]*Item, 0, len(s.items)+len(s.collections))
	for _, it := range s.items {
		out = append(out, it)
	}
	for _, c := range s.collections {
		out = append(out, &Item{
			ID: c.ID, Name: c.Name, SortName: strings.ToLower(c.Name), Type: "BoxSet",
			IsFolder: true, LocationType: "FileSystem",
			Tags: []string{}, Genres: []string{}, ProviderIDs: map[string]string{},
		})
	}
	return out
}

func (s *Server) underLocked(it *Item, parentID string, recursive bool) bool {
	for _, c := range s.collections {
		if normaliseID(c.ID) == parentID && slices.Contains(c.ItemIDs, it.ID) {
			return true
		}
	}
	if !recursive {
		return normaliseID(it.ParentID) == parentID
	}
	for _, a := range it.ancestors() {
		if a != "" && normaliseID(a) == parentID {
			return true
		}
	}
	return false
}

func (s *Server) hasUserLocked(id string) bool {
	for _, u := range s.users {
		if normaliseID(u.ID) == normaliseID(id) {
			return true
		}
	}
	return false
}

func (s *Server) getItem(w http.ResponseWriter, r *http.Request) {
	if s.legacyOnly {
		notFound(w)
		return
	}
	s.serveItem(w, r.URL.Query().Get("userId"), r.PathValue("id"))
}

func (s *Server) getUserItem(w http.ResponseWriter, r *http.Request) {
	s.serveItem(w, r.PathValue("userId"), r.PathValue("id"))
}

func (s *Server) serveItem(w http.ResponseWriter, userID, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if userID != "" && !s.hasUserLocked(userID) {
		http.Error(w, "Error processing request.", http.StatusNotFound)
		return
	}
	opts := renderOpts{fields: allFields, userID: userID, userData: userID != "", images: true}
	if strings.Trim(id, "0-") == "" {
		// Jellyfin 12.1 answers the all-zero ID with the user's root folder.
		root := &Item{ID: "e9d5075a555c1cbc394eec4cef295274", Name: "Media Folders", Type: "UserRootFolder",
			IsFolder: true, Path: "/config/root/default", SortName: "media folders"}
		writeJSON(w, http.StatusOK, s.renderLocked(root, opts))
		return
	}
	for _, it := range s.items {
		if normaliseID(it.ID) == normaliseID(id) {
			writeJSON(w, http.StatusOK, s.renderLocked(it, opts))
			return
		}
	}
	notFound(w)
}

// renderLocked builds the item's JSON in Jellyfin's shape.
func (s *Server) renderLocked(it *Item, o renderOpts) map[string]any {
	m := map[string]any{
		"Name":            it.Name,
		"ServerId":        s.serverID,
		"Id":              it.ID,
		"ChannelId":       nil,
		"IsFolder":        it.IsFolder,
		"Type":            it.Type,
		"LocationType":    it.LocationType,
		"ImageBlurHashes": map[string]any{},
	}
	s.renderCore(it, m)
	s.renderFields(it, o, m)
	if o.images {
		tags := map[string]string{}
		if t := it.ImageTags["Primary"]; t != "" {
			tags["Primary"] = t
		}
		m["ImageTags"] = tags
		m["BackdropImageTags"] = []string{}
	}
	if o.userData {
		m["UserData"] = s.userDataJSONLocked(o.userID, it.ID)
	}
	return m
}

func (s *Server) renderCore(it *Item, m map[string]any) {
	if !it.IsFolder {
		m["MediaType"] = "Video"
		m["VideoType"] = it.VideoType
		m["Container"] = it.Container
		m["RunTimeTicks"] = it.RunTimeTicks
	}
	if it.ProductionYear > 0 {
		m["ProductionYear"] = it.ProductionYear
	}
	if it.Type == "Episode" {
		m["IndexNumber"] = it.IndexNumber
		m["ParentIndexNumber"] = it.ParentIndexNumber
		m["SeriesName"] = it.SeriesName
		m["SeriesId"] = it.SeriesID
		m["SeasonId"] = it.SeasonID
		m["SeasonName"] = it.SeasonName
	}
}

func (s *Server) renderFields(it *Item, o renderOpts, m map[string]any) {
	if o.fields["path"] && it.Path != "" {
		m["Path"] = it.Path
	}
	if o.fields["mediasources"] && !it.IsFolder {
		m["MediaSources"] = it.MediaSources
	}
	if o.fields["datecreated"] {
		m["DateCreated"] = jfTime(it.DateCreated)
	}
	if o.fields["tags"] {
		m["Tags"] = it.Tags
	}
	if o.fields["genres"] {
		m["Genres"] = it.Genres
		m["GenreItems"] = []any{}
	}
	if o.fields["providerids"] {
		m["ProviderIds"] = it.ProviderIDs
	}
	if o.fields["parentid"] && it.ParentID != "" {
		m["ParentId"] = it.ParentID
	}
	if o.fields["sortname"] {
		m["SortName"] = it.SortName
	}
}

func (s *Server) userDataJSONLocked(userID, itemID string) map[string]any {
	ud := s.userData[normaliseID(userID)][itemID]
	m := map[string]any{
		"PlaybackPositionTicks": ud.PlaybackPositionTicks,
		"PlayCount":             ud.PlayCount,
		"IsFavorite":            ud.IsFavorite,
		"Played":                ud.Played,
		"Key":                   itemID,
		"ItemId":                itemID,
	}
	if ud.LastPlayedDate != nil {
		m["LastPlayedDate"] = jfTime(*ud.LastPlayedDate)
	}
	return m
}

func normaliseID(id string) string {
	return strings.ToLower(strings.ReplaceAll(id, "-", ""))
}
