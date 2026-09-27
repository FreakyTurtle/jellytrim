package jellyfin

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func readJSON(t *testing.T, name string, v any) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("decoding %s: %v", name, err)
	}
}

// TestDecodesRealJellyfin121Responses checks the client's types against JSON
// captured from a real Jellyfin 12.1 server, not the fake's rendering of it.
func TestDecodesRealJellyfin121Responses(t *testing.T) {
	var page ItemPage
	readJSON(t, "items.json", &page)
	if page.TotalRecordCount != 17 || len(page.Items) != 17 {
		t.Fatalf("items: total %d, len %d", page.TotalRecordCount, len(page.Items))
	}
	byName := map[string]Item{}
	for _, it := range page.Items {
		byName[it.Name] = it
		if it.DateCreated.IsZero() || it.Path == "" || len(it.MediaSources) != 1 {
			t.Errorf("%s: missing basics: %+v", it.Name, it)
		}
	}
	alpha := byName["Alpha (2019)"]
	wantPlayed := time.Date(2026, 9, 22, 18, 17, 3, 0, time.UTC)
	if alpha.UserData.LastPlayedDate == nil || !alpha.UserData.LastPlayedDate.Equal(wantPlayed) || alpha.ProductionYear != 2019 {
		t.Errorf("Alpha: %+v", alpha.UserData)
	}
	if byName["Echo (2022)"].UserData.LastPlayedDate != nil {
		t.Errorf("an unplayed item has a last-played date")
	}
	delta := byName["Delta (2022)"].MediaSources[0]
	if v := delta.MediaStreams[0]; v.Codec != "hevc" || v.Width != 3840 || v.VideoRangeType != "HDR10" {
		t.Errorf("Delta video: %+v", v)
	}
	if delta.Size <= 0 || delta.Bitrate <= 0 {
		t.Errorf("Delta size and bitrate: %+v", delta)
	}
	golf := byName["Golf (2018)"].MediaSources[0].MediaStreams
	if len(golf) != 7 || golf[3].Language != "fra" || golf[4].Type != "Subtitle" {
		t.Errorf("Golf streams: %+v", golf)
	}
	ep := byName["Mike Show - S01E02"]
	if ep.Type != "Episode" || ep.SeriesName != "Mike Show" || ep.SeasonID == "" || ep.IndexNumber != 2 || ep.ParentIndexNumber != 1 {
		t.Errorf("episode: %+v", ep)
	}
	if ep.Runtime() != 2021*time.Millisecond {
		t.Errorf("runtime = %v", ep.Runtime())
	}

	var light ItemPage
	readJSON(t, "items_userdata.json", &light)
	if len(light.Items) != 2 || light.Items[0].Path != "" || !light.Items[0].UserData.Played || !light.Items[1].UserData.IsFavorite {
		t.Errorf("light pass: %+v", light.Items)
	}

	var one Item
	readJSON(t, "item.json", &one)
	if one.ID != "d15b890b81018d54a2b0f65fbee75563" || len(one.MediaSources) != 1 {
		t.Errorf("single item: %+v", one)
	}
}

func TestDecodesRealSystemLibrariesAndUsers(t *testing.T) {
	var info SystemInfo
	readJSON(t, "system_info.json", &info)
	if info.ServerName != "jellytrim-dev" || info.Version != "12.1.0" || info.ID == "" {
		t.Errorf("system info: %+v", info)
	}
	var libs []Library
	readJSON(t, "virtual_folders.json", &libs)
	if len(libs) != 2 || libs[0].ItemID == "" || len(libs[0].Locations) != 1 {
		t.Errorf("libraries: %+v", libs)
	}
	var users []User
	readJSON(t, "users.json", &users)
	if len(users) != 1 || users[0].LastActivityDate == nil || !users[0].Policy.IsAdministrator || !users[0].Policy.IsHidden {
		t.Errorf("users: %+v", users)
	}
}

func TestFlexTime(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want time.Time
		err  bool
	}{
		{"seven fractional digits with Z", `"2026-09-22T18:17:03.1234567Z"`, time.Date(2026, 9, 22, 18, 17, 3, 123456700, time.UTC), false},
		{"offset is converted to UTC", `"2026-09-22T19:17:03+01:00"`, time.Date(2026, 9, 22, 18, 17, 3, 0, time.UTC), false},
		{"no zone is read as UTC", `"2026-09-22T18:17:03.0000000"`, time.Date(2026, 9, 22, 18, 17, 3, 0, time.UTC), false},
		{"null is never", `null`, time.Time{}, false},
		{"empty is never", `""`, time.Time{}, false},
		{".NET minimum date is never", `"0001-01-01T00:00:00.0000000Z"`, time.Time{}, false},
		{"garbage is an error", `"yesterday"`, time.Time{}, true},
		{"a number is an error", `12`, time.Time{}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var f flexTime
			err := json.Unmarshal([]byte(tc.in), &f)
			if (err != nil) != tc.err {
				t.Fatalf("err = %v", err)
			}
			if !f.t.Equal(tc.want) || (!f.t.IsZero() && f.t.Location() != time.UTC) {
				t.Errorf("got %v, want %v", f.t, tc.want)
			}
		})
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"5", 5 * time.Second, true},
		{" 0 ", 0, true},
		{"-1", 0, false},
		{"", 0, false},
		{"soon", 0, false},
		{"Sun, 27 Sep 2026 12:00:30 GMT", 30 * time.Second, true},
		{"Sun, 27 Sep 2026 11:00:00 GMT", 0, true},
	}
	for _, tc := range tests {
		got, ok := parseRetryAfter(tc.in, now)
		if got != tc.want || ok != tc.ok {
			t.Errorf("parseRetryAfter(%q) = %v, %v; want %v, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestQuoteEscapes(t *testing.T) {
	tests := map[string]string{
		`plain`:       `"plain"`,
		`say "hi"`:    `"say \"hi\""`,
		`back\slash`:  `"back\\slash"`,
		"new\r\nline": `"newline"`,
		"":            `""`,
	}
	for in, want := range tests {
		if got := quote(in); got != want {
			t.Errorf("quote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestSleepContextStopsOnCancel(t *testing.T) {
	if err := sleepContext(context.Background(), 0); err != nil {
		t.Errorf("zero wait: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepContext(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled wait: %v", err)
	}
}
