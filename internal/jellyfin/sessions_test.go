package jellyfin_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/freakyturtle/jellytrim/internal/jellyfin"
	"github.com/freakyturtle/jellytrim/internal/jellyfin/jellyfintest"
)

const otherID = "0123456789abcdef0123456789abcdef"

func TestNowPlaying(t *testing.T) {
	tests := []struct {
		name  string
		setup func(s *jellyfintest.Server)
		want  []jellyfin.Playing
	}{
		{"no sessions", func(*jellyfintest.Server) {}, []jellyfin.Playing{}},
		{"an idle app is not playing", func(s *jellyfintest.Server) {
			s.AddSession(jellyfintest.Session{UserName: "dev", DeviceName: "Firefox"})
		}, []jellyfin.Playing{}},
		{"one session playing", func(s *jellyfintest.Server) {
			s.SetPlaying(alphaID, "alex", "Living Room TV", false)
		}, []jellyfin.Playing{{ItemID: alphaID, MediaSourceID: alphaID, UserName: "alex",
			DeviceName: "Living Room TV", Client: "Jellyfin Web", PositionTicks: 6_000_000_000}}},
		{"a paused session is listed as paused", func(s *jellyfintest.Server) {
			s.SetPlaying(alphaID, "alex", "Living Room TV", true)
		}, []jellyfin.Playing{{ItemID: alphaID, MediaSourceID: alphaID, UserName: "alex",
			DeviceName: "Living Room TV", Client: "Jellyfin Web", Paused: true, PositionTicks: 6_000_000_000}}},
		{"several sessions, idle ones left out", func(s *jellyfintest.Server) {
			s.AddSession(jellyfintest.Session{UserName: "dev", DeviceName: "Firefox"})
			s.SetPlaying(alphaID, "alex", "Living Room TV", false)
			s.AddSession(jellyfintest.Session{UserName: "sam", DeviceName: "Phone", Client: "Findroid",
				ItemID: otherID, MediaSourceID: "fedcba", Paused: true, PositionTicks: 42})
		}, []jellyfin.Playing{
			{ItemID: alphaID, MediaSourceID: alphaID, UserName: "alex", DeviceName: "Living Room TV",
				Client: "Jellyfin Web", PositionTicks: 6_000_000_000},
			{ItemID: otherID, MediaSourceID: "fedcba", UserName: "sam", DeviceName: "Phone",
				Client: "Findroid", Paused: true, PositionTicks: 42},
		}},
		{"a session idle for too long is left out", func(s *jellyfintest.Server) {
			s.AddSession(jellyfintest.Session{UserName: "alex", DeviceName: "Old TV", ItemID: alphaID, Stale: true})
		}, []jellyfin.Playing{}},
		{"stopped playback is not playing", func(s *jellyfintest.Server) {
			s.SetPlaying(alphaID, "alex", "Living Room TV", false)
			s.StopPlaying(alphaID)
		}, []jellyfin.Playing{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, fixtures())
			tc.setup(h.srv)
			got, err := h.c.NowPlaying(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d sessions, want %d: %+v", len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("session %d\n got: %+v\nwant: %+v", i, got[i], tc.want[i])
				}
			}
			reqs := h.srv.RequestsFor("/Sessions")
			if len(reqs) != 1 || reqs[0].Query.Get("activeWithinSeconds") != "960" {
				t.Fatalf("requests %+v", reqs)
			}
		})
	}
}

func TestNowPlayingErrors(t *testing.T) {
	tests := []struct {
		name  string
		fault jellyfintest.Fault
		times int
		want  error
	}{
		{"rejected key", jellyfintest.Fault{Status: http.StatusUnauthorized}, 1, jellyfin.ErrUnauthorized},
		{"starting up past every retry", jellyfintest.Fault{Status: http.StatusServiceUnavailable}, 3, jellyfin.ErrUnavailable},
		{"dropped connection past every retry", jellyfintest.Fault{DropConnection: true}, 3, jellyfin.ErrUnavailable},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, fixtures())
			h.srv.SetPlaying(alphaID, "alex", "Living Room TV", false)
			for range tc.times {
				h.srv.Inject("/Sessions", tc.fault)
			}
			got, err := h.c.NowPlaying(context.Background())
			if !errors.Is(err, tc.want) {
				t.Fatalf("err %v, want %v", err, tc.want)
			}
			if got != nil {
				t.Fatalf("sessions on error: %+v", got)
			}
		})
	}
}

func TestNowPlayingRetriesA503(t *testing.T) {
	h := newHarness(t, fixtures())
	h.srv.SetPlaying(alphaID, "alex", "Living Room TV", false)
	h.srv.FailNext("/Sessions", http.StatusServiceUnavailable, 0)
	got, err := h.c.NowPlaying(context.Background())
	if err != nil || len(got) != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestPlayingPlays(t *testing.T) {
	p := jellyfin.Playing{ItemID: "D15B890B-8101-8D54-A2B0-F65FBEE75563", MediaSourceID: "aaaa"}
	tests := []struct {
		id   string
		want bool
	}{
		{alphaID, true},
		{"aaaa", true},
		{otherID, false},
		{"", false},
	}
	for _, tc := range tests {
		if got := p.Plays(tc.id); got != tc.want {
			t.Errorf("Plays(%q) = %v, want %v", tc.id, got, tc.want)
		}
	}
	if (jellyfin.Playing{}).Plays("") {
		t.Error("an empty session plays nothing")
	}
}
