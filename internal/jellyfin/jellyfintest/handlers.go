package jellyfintest

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// handler wraps the routes in the behaviour every Jellyfin request gets:
// recording, delay, injected faults, then authentication.
func (s *Server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /System/Info/Public", s.publicInfo)
	mux.HandleFunc("GET /System/Info", s.auth(s.systemInfo))
	mux.HandleFunc("GET /Library/VirtualFolders", s.auth(s.virtualFolders))
	mux.HandleFunc("GET /Users", s.auth(s.listUsers))
	mux.HandleFunc("GET /Items", s.auth(s.listItems))
	mux.HandleFunc("GET /Items/{id}", s.auth(s.getItem))
	mux.HandleFunc("GET /Users/{userId}/Items/{id}", s.auth(s.getUserItem))
	mux.HandleFunc("GET /Items/{id}/Images/{type}", s.auth(s.getImage))
	mux.HandleFunc("POST /Items/{id}/Refresh", s.auth(s.refresh))
	mux.HandleFunc("POST /Library/Media/Updated", s.auth(s.mediaUpdated))
	mux.HandleFunc("GET /Sessions", s.auth(s.listSessions))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fault, hasFault, delay := s.record(r)
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				return
			}
		}
		if hasFault {
			s.writeFault(w, fault)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// record logs the request and takes the next queued fault for its path.
func (s *Server) record(r *http.Request) (Fault, bool, time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, Request{
		Method:        r.Method,
		Path:          r.URL.Path,
		Query:         r.URL.Query(),
		Authorization: r.Header.Get("Authorization"),
		Header:        r.Header.Clone(),
	})
	q := s.faults[r.URL.Path]
	if len(q) == 0 {
		return Fault{}, false, s.delay
	}
	s.faults[r.URL.Path] = q[1:]
	return q[0], true, s.delay
}

func (s *Server) writeFault(w http.ResponseWriter, f Fault) {
	if f.DropConnection {
		hj, ok := w.(http.Hijacker)
		if !ok {
			s.t.Errorf("jellyfintest: cannot drop the connection: no hijacker")
			return
		}
		conn, _, err := hj.Hijack()
		if err == nil {
			_ = conn.Close()
		}
		return
	}
	status := f.Status
	if status == 0 {
		status = http.StatusInternalServerError
	}
	if f.RetryAfter > 0 {
		secs := int((f.RetryAfter + time.Second - 1) / time.Second)
		w.Header().Set("Retry-After", strconv.Itoa(secs))
	}
	w.WriteHeader(status)
	_, _ = io.WriteString(w, f.Body)
}

// auth enforces Jellyfin 12's rules: only the MediaBrowser Authorization
// header counts, with every field quoted; the legacy X-Emby-Token header and
// api_key query parameter are refused. Failures get an empty 401, as
// Jellyfin sends.
func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authorised(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (s *Server) authorised(r *http.Request) bool {
	if r.Header.Get("X-Emby-Token") != "" || r.Header.Get("X-MediaBrowser-Token") != "" {
		return false
	}
	for k := range r.URL.Query() {
		switch strings.ToLower(k) {
		case "api_key", "apikey":
			return false
		}
	}
	fields, ok := AuthHeaderFields(r.Header.Get("Authorization"))
	if !ok || fields["Token"] != s.token || fields["Client"] == "" {
		return false
	}
	for _, k := range []string{"Device", "DeviceId", "Version"} {
		if fields[k] == "" {
			return false
		}
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// notFound mimics ASP.NET's problem-details 404.
func notFound(w http.ResponseWriter) {
	writeJSON(w, http.StatusNotFound, map[string]any{
		"type":   "https://tools.ietf.org/html/rfc9110#section-15.5.5",
		"title":  "Not Found",
		"status": 404,
	})
}

func (s *Server) publicInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"LocalAddress":           s.URL,
		"ServerName":             s.serverName,
		"Version":                s.version,
		"ProductName":            "Jellyfin Server",
		"OperatingSystem":        "",
		"Id":                     s.serverID,
		"StartupWizardCompleted": true,
	})
}

func (s *Server) systemInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"OperatingSystemDisplayName": "",
		"HasPendingRestart":          false,
		"IsShuttingDown":             false,
		"SupportsLibraryMonitor":     true,
		"WebSocketPortNumber":        8096,
		"CompletedInstallations":     []any{},
		"CanSelfRestart":             true,
		"ProgramDataPath":            "/config",
		"CachePath":                  "/cache",
		"LogPath":                    "/config/log",
		"HasUpdateAvailable":         false,
		"EncoderLocation":            "System",
		"SystemArchitecture":         "X64",
		"LocalAddress":               s.URL,
		"ServerName":                 s.serverName,
		"Version":                    s.version,
		"ProductName":                "Jellyfin Server",
		"OperatingSystem":            "",
		"Id":                         s.serverID,
		"StartupWizardCompleted":     true,
	})
}

func (s *Server) virtualFolders(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	out := make([]map[string]any, 0, len(s.libraries))
	for _, l := range s.libraries {
		infos := make([]map[string]string, 0, len(l.Locations))
		for _, loc := range l.Locations {
			infos = append(infos, map[string]string{"Path": loc})
		}
		out = append(out, map[string]any{
			"Name":           l.Name,
			"Locations":      l.Locations,
			"CollectionType": l.CollectionType,
			"LibraryOptions": map[string]any{
				"Enabled":               true,
				"EnableRealtimeMonitor": true,
				"PathInfos":             infos,
			},
			"ItemId":        l.ItemID,
			"RefreshStatus": "Idle",
		})
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) listUsers(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	out := make([]map[string]any, 0, len(s.users))
	for _, u := range s.users {
		m := map[string]any{
			"Name":                      u.Name,
			"ServerId":                  s.serverID,
			"Id":                        u.ID,
			"HasPassword":               true,
			"HasConfiguredPassword":     true,
			"HasConfiguredEasyPassword": false,
			"EnableAutoLogin":           false,
			"Configuration":             map[string]any{"PlayDefaultAudioTrack": true},
			"Policy": map[string]any{
				"IsAdministrator":  u.IsAdministrator,
				"IsHidden":         u.IsHidden,
				"IsDisabled":       u.IsDisabled,
				"EnableAllFolders": true,
			},
		}
		if u.LastActivityDate != nil {
			m["LastActivityDate"] = jfTime(*u.LastActivityDate)
			m["LastLoginDate"] = jfTime(*u.LastActivityDate)
		}
		out = append(out, m)
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getImage(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	it, ok := s.items[r.PathValue("id")]
	var img []byte
	if ok && r.PathValue("type") == "Primary" {
		img = it.Image
	}
	name := ""
	if ok {
		name = it.Name
	}
	s.mu.Unlock()
	switch {
	case !ok:
		notFound(w)
	case img == nil:
		// Jellyfin 12.1 sends a JSON string, not problem details, here.
		writeJSON(w, http.StatusNotFound, name+" does not have an image of type "+r.PathValue("type"))
	default:
		w.Header().Set("Content-Type", http.DetectContentType(img))
		_, _ = w.Write(img)
	}
}

func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[r.PathValue("id")]; !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	s.refreshes = append(s.refreshes, Refresh{ItemID: r.PathValue("id"), Query: r.URL.Query()})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) mediaUpdated(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		w.WriteHeader(http.StatusUnsupportedMediaType)
		return
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var body struct {
		Updates []MediaUpdate `json:"Updates"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.updates = append(s.updates, body.Updates...)
	s.updateRaw = append(s.updateRaw, raw)
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// jfTime formats a time the way Jellyfin does: UTC, seven fractional
// digits, a Z.
func jfTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.0000000Z")
}
