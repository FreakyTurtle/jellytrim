package jellyfintest

import (
	"fmt"
	"net/http"
	"strings"
)

// Session is a client session in the fake server. A session with an empty
// ItemID is an idle app (open on its home screen), which Jellyfin lists
// without a NowPlayingItem.
type Session struct {
	UserName   string
	DeviceName string
	// Client is the app name. The default is "Jellyfin Web".
	Client string
	// ItemID is the item playing. MediaSourceID defaults to it.
	ItemID        string
	MediaSourceID string
	Paused        bool
	PositionTicks int64
	// Stale marks a session last active longer ago than the client's
	// activeWithinSeconds, so the server leaves it out when that parameter
	// is given, as Jellyfin does.
	Stale bool
}

// defaultPosition is the position SetPlaying reports: ten minutes in.
const defaultPosition = 10 * 60 * 10_000_000

// AddSession adds a session as it is.
func (s *Server) AddSession(sess Session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions = append(s.sessions, sess)
}

// SetPlaying starts (or updates) playback of itemID by user on device, ten
// minutes in. Paused sessions stay listed as playing, as in Jellyfin.
func (s *Server) SetPlaying(itemID, user, device string, paused bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.sessions {
		if s.sessions[i].UserName == user && s.sessions[i].DeviceName == device {
			s.sessions[i].ItemID, s.sessions[i].MediaSourceID = itemID, ""
			s.sessions[i].Paused = paused
			return
		}
	}
	s.sessions = append(s.sessions, Session{UserName: user, DeviceName: device, ItemID: itemID,
		Paused: paused, PositionTicks: defaultPosition})
}

// StopPlaying stops every session playing itemID. The sessions stay, idle,
// as they do in Jellyfin when playback stops.
func (s *Server) StopPlaying(itemID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.sessions {
		if normaliseID(s.sessions[i].ItemID) == normaliseID(itemID) {
			s.sessions[i].ItemID, s.sessions[i].MediaSourceID = "", ""
			s.sessions[i].Paused, s.sessions[i].PositionTicks = false, 0
		}
	}
}

// listSessions renders sessions in the shape Jellyfin 12.1 sends: every
// session has a PlayState; only a playing one has a NowPlayingItem.
func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	activeOnly := r.URL.Query().Get("activeWithinSeconds") != ""
	s.mu.Lock()
	out := make([]map[string]any, 0, len(s.sessions))
	for i, sess := range s.sessions {
		if activeOnly && sess.Stale {
			continue
		}
		out = append(out, s.renderSessionLocked(i, sess))
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) renderSessionLocked(i int, sess Session) map[string]any {
	client := sess.Client
	if client == "" {
		client = "Jellyfin Web"
	}
	play := map[string]any{
		"CanSeek": false, "IsPaused": false, "IsMuted": false,
		"RepeatMode": "RepeatNone", "PlaybackOrder": "Default",
	}
	m := map[string]any{
		"Id":                    fmt.Sprintf("%032x", i+1),
		"UserId":                s.userIDLocked(sess.UserName),
		"UserName":              sess.UserName,
		"Client":                client,
		"DeviceName":            sess.DeviceName,
		"DeviceId":              "device-" + strings.ReplaceAll(strings.ToLower(sess.DeviceName), " ", "-"),
		"ApplicationVersion":    s.version,
		"IsActive":              true,
		"SupportsMediaControl":  true,
		"SupportsRemoteControl": true,
		"NowPlayingQueue":       []any{},
		"AdditionalUsers":       []any{},
		"ServerId":              s.serverID,
		"PlayState":             play,
	}
	if sess.ItemID == "" {
		return m
	}
	source := sess.MediaSourceID
	if source == "" {
		source = sess.ItemID
	}
	play["CanSeek"], play["IsPaused"] = true, sess.Paused
	play["PositionTicks"], play["MediaSourceId"], play["PlayMethod"] = sess.PositionTicks, source, "DirectPlay"
	m["NowPlayingItem"] = s.nowPlayingItemLocked(sess.ItemID)
	return m
}

// nowPlayingItemLocked is the item as Jellyfin embeds it in a session; an
// item the fake does not hold is sent with its ID alone.
func (s *Server) nowPlayingItemLocked(id string) map[string]any {
	it, ok := s.items[id]
	if !ok {
		return map[string]any{"Id": id, "MediaType": "Video"}
	}
	return map[string]any{
		"Id": it.ID, "Name": it.Name, "Type": it.Type, "MediaType": "Video", "Path": it.Path,
		"RunTimeTicks": it.RunTimeTicks, "IsFolder": false, "ServerId": s.serverID,
	}
}

func (s *Server) userIDLocked(name string) string {
	for _, u := range s.users {
		if u.Name == name {
			return u.ID
		}
	}
	return ""
}
