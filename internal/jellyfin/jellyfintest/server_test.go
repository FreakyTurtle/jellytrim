package jellyfintest

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func header(token string) string {
	return `MediaBrowser Client="JellyTrim", Device="d", DeviceId="i", Version="v", Token="` + token + `"`
}

func get(t *testing.T, s *Server, path string, set func(*http.Request)) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, s.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if set != nil {
		set(req)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func TestAuthMatchesJellyfin12(t *testing.T) {
	s := New(t, WithFixtures())
	tests := []struct {
		name string
		path string
		set  func(*http.Request)
		want int
	}{
		{"quoted MediaBrowser header is accepted", "/System/Info", func(r *http.Request) { r.Header.Set("Authorization", header(Token)) }, 200},
		{"no header is refused", "/System/Info", nil, 401},
		{"wrong token is refused", "/System/Info", func(r *http.Request) { r.Header.Set("Authorization", header("nope")) }, 401},
		{"X-Emby-Token is refused", "/System/Info", func(r *http.Request) { r.Header.Set("X-Emby-Token", Token) }, 401},
		{"api_key query is refused", "/System/Info?api_key=" + Token, nil, 401},
		{"ApiKey query is refused even with a good header", "/System/Info?ApiKey=" + Token, func(r *http.Request) { r.Header.Set("Authorization", header(Token)) }, 401},
		{"unquoted values are refused", "/System/Info", func(r *http.Request) {
			r.Header.Set("Authorization", "MediaBrowser Client=JellyTrim, Device=d, DeviceId=i, Version=v, Token="+Token)
		}, 401},
		{"missing device is refused", "/System/Info", func(r *http.Request) {
			r.Header.Set("Authorization", `MediaBrowser Client="JellyTrim", Token="`+Token+`"`)
		}, 401},
		{"public info needs no key", "/System/Info/Public", nil, 200},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := get(t, s, tc.path, tc.set).StatusCode; got != tc.want {
				t.Errorf("status = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestAuthHeaderFields(t *testing.T) {
	f, ok := AuthHeaderFields(`MediaBrowser Client="A", Device="say \"hi\" \\ there", Token="t"`)
	if !ok || f["Client"] != "A" || f["Device"] != `say "hi" \ there` || f["Token"] != "t" {
		t.Fatalf("parsed %v %v", f, ok)
	}
	for _, bad := range []string{`Bearer x`, `MediaBrowser Token=t`, `MediaBrowser Token="t`, `MediaBrowser Token="t\`} {
		if _, ok := AuthHeaderFields(bad); ok {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestItemsPagingAndFieldGating(t *testing.T) {
	s := New(t, WithFixtures())
	auth := func(r *http.Request) { r.Header.Set("Authorization", header(Token)) }
	resp := get(t, s, "/Items?userId="+FixtureUserID+"&parentId="+FixtureTVLibraryID+
		"&recursive=true&includeItemTypes=Movie,Episode&startIndex=3&limit=10", auth)
	var page struct {
		Items            []map[string]any
		TotalRecordCount int
		StartIndex       int
	}
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if page.TotalRecordCount != 5 || page.StartIndex != 3 || len(page.Items) != 2 {
		t.Fatalf("paging: total %d start %d len %d", page.TotalRecordCount, page.StartIndex, len(page.Items))
	}
	it := page.Items[0]
	if _, ok := it["Path"]; ok {
		t.Errorf("Path sent without fields=Path")
	}
	if _, ok := it["UserData"]; !ok {
		t.Errorf("UserData missing with a userId")
	}
	if it["Type"] != "Episode" || it["SeriesName"] == "" {
		t.Errorf("episode shape: %v", it)
	}
}

func TestUnknownUserIs404Text(t *testing.T) {
	s := New(t, WithFixtures())
	resp := get(t, s, "/Items?userId=11111111111111111111111111111111", func(r *http.Request) {
		r.Header.Set("Authorization", header(Token))
	})
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 404 || string(b) != "Error processing request.\n" {
		t.Errorf("got %d %q", resp.StatusCode, b)
	}
}

func TestFixturesLoad(t *testing.T) {
	s := New(t, WithFixtures())
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.libraries) != 2 || len(s.users) != 1 || len(s.items) != 17 {
		t.Fatalf("libraries %d, users %d, items %d", len(s.libraries), len(s.users), len(s.items))
	}
	movies := 0
	for _, it := range s.items {
		if it.LibraryID == FixtureMoviesLibraryID {
			movies++
		}
	}
	if movies != 12 {
		t.Errorf("movies = %d, want 12", movies)
	}
	if !s.userData[FixtureUserID]["d15b890b81018d54a2b0f65fbee75563"].Played {
		t.Errorf("Alpha should be played by the fixture user")
	}
}

func TestSessionsShape(t *testing.T) {
	s := New(t, WithFixtures())
	if got := get(t, s, "/Sessions", nil).StatusCode; got != http.StatusUnauthorized {
		t.Fatalf("no key: status %d", got)
	}
	s.AddSession(Session{UserName: "dev", DeviceName: "Firefox"})
	s.SetPlaying("d15b890b81018d54a2b0f65fbee75563", "alex", "Living Room TV", true)
	s.AddSession(Session{UserName: "old", DeviceName: "Old TV", ItemID: "x", Stale: true})
	auth := func(r *http.Request) { r.Header.Set("Authorization", header(Token)) }
	var all []map[string]any
	if err := json.NewDecoder(get(t, s, "/Sessions", auth).Body).Decode(&all); err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("without activeWithinSeconds every session is listed, got %d", len(all))
	}
	var active []map[string]any
	if err := json.NewDecoder(get(t, s, "/Sessions?activeWithinSeconds=960", auth).Body).Decode(&active); err != nil {
		t.Fatal(err)
	}
	if len(active) != 2 {
		t.Fatalf("stale session listed: %d sessions", len(active))
	}
	if _, ok := active[0]["NowPlayingItem"]; ok {
		t.Error("an idle session has a NowPlayingItem")
	}
	if _, ok := active[0]["PlayState"].(map[string]any); !ok {
		t.Error("an idle session has no PlayState")
	}
	np, _ := active[1]["NowPlayingItem"].(map[string]any)
	ps, _ := active[1]["PlayState"].(map[string]any)
	if np["Name"] != "Alpha (2019)" || ps["IsPaused"] != true || ps["MediaSourceId"] != np["Id"] {
		t.Fatalf("playing session %+v", active[1])
	}
	s.StopPlaying("d15b890b-8101-8d54-a2b0-f65fbee75563")
	active = nil
	if err := json.NewDecoder(get(t, s, "/Sessions?activeWithinSeconds=960", auth).Body).Decode(&active); err != nil {
		t.Fatal(err)
	}
	if _, ok := active[1]["NowPlayingItem"]; ok {
		t.Error("StopPlaying left the item playing")
	}
}
