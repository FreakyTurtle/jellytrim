package pathmap

import (
	"errors"
	"reflect"
	"testing"
)

func mustNew(t *testing.T, ms ...Mapping) *Mapper {
	t.Helper()
	m, err := New(ms)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestToLocal(t *testing.T) {
	m := mustNew(t,
		Mapping{Jellyfin: "/media", Local: "/mnt/all"},
		Mapping{Jellyfin: "/media/movies", Local: "/mnt/media/movies"},
		Mapping{Jellyfin: "/media/tv/", Local: "/mnt/tv/"},
	)
	tests := []struct {
		name, in, want string
		err            error
	}{
		{"longest prefix wins", "/media/movies/A (2019)/A (2019).mkv", "/mnt/media/movies/A (2019)/A (2019).mkv", nil},
		{"trailing slash in mapping", "/media/tv/Show/S01/e1.mkv", "/mnt/tv/Show/S01/e1.mkv", nil},
		{"shorter prefix", "/media/music/x.flac", "/mnt/all/music/x.flac", nil},
		{"segment boundary", "/media/movies2/x.mkv", "/mnt/all/movies2/x.mkv", nil},
		{"exact prefix", "/media/movies", "/mnt/media/movies", nil},
		{"unmapped", "/data/x.mkv", "", ErrUnmapped},
		{"dotdot rejected", "/media/movies/../../etc/passwd", "", ErrUnsafe},
		{"relative rejected", "media/movies/x.mkv", "", ErrUnsafe},
		{"spaces and unicode kept", "/media/movies/Amélie (2001)/Amélie.mkv", "/mnt/media/movies/Amélie (2001)/Amélie.mkv", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := m.ToLocal(tt.in)
			if !errors.Is(err, tt.err) {
				t.Fatalf("err = %v, want %v", err, tt.err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSegmentBoundaryWithoutFallback(t *testing.T) {
	m := mustNew(t, Mapping{Jellyfin: "/media/movies", Local: "/mnt/movies"})
	if _, err := m.ToLocal("/media/movies2/x.mkv"); !errors.Is(err, ErrUnmapped) {
		t.Fatalf("prefix matched across a segment boundary: %v", err)
	}
}

func TestRoundTrip(t *testing.T) {
	m := mustNew(t,
		Mapping{Jellyfin: "/media/movies", Local: "/mnt/media/movies"},
		Mapping{Jellyfin: "/media/tv", Local: "/mnt/media/tv"},
	)
	for _, jf := range []string{"/media/movies/A/A.mkv", "/media/tv/S/Season 01/e.mkv"} {
		local, err := m.ToLocal(jf)
		if err != nil {
			t.Fatal(err)
		}
		back, err := m.ToJellyfin(local)
		if err != nil {
			t.Fatal(err)
		}
		if back != jf {
			t.Fatalf("round trip %q -> %q -> %q", jf, local, back)
		}
	}
}

func TestWindowsJellyfin(t *testing.T) {
	m := mustNew(t, Mapping{Jellyfin: `D:\Media\Movies`, Local: "/mnt/movies"})
	got, err := m.ToLocal(`d:\media\movies\A (2019)\A (2019).mkv`)
	if err != nil || got != "/mnt/movies/A (2019)/A (2019).mkv" {
		t.Fatalf("got %q, %v", got, err)
	}
	back, err := m.ToJellyfin("/mnt/movies/A (2019)/A (2019).mkv")
	if err != nil || back != `D:\Media\Movies\A (2019)\A (2019).mkv` {
		t.Fatalf("back %q, %v", back, err)
	}
}

func TestNewRejectsBadMappings(t *testing.T) {
	bad := [][]Mapping{
		{{Jellyfin: "", Local: "/mnt"}},
		{{Jellyfin: "/media", Local: "relative"}},
		{{Jellyfin: "/media/../x", Local: "/mnt"}},
		{{Jellyfin: "/media", Local: "/mnt/a"}, {Jellyfin: "/media/", Local: "/mnt/b"}},
	}
	for i, ms := range bad {
		if _, err := New(ms); !errors.Is(err, ErrInvalid) {
			t.Errorf("case %d: err = %v, want ErrInvalid", i, err)
		}
	}
}

func TestWithinAndRoots(t *testing.T) {
	m := mustNew(t,
		Mapping{Jellyfin: "/media/tv", Local: "/mnt/tv"},
		Mapping{Jellyfin: "/media/movies", Local: "/mnt/movies"},
	)
	roots := m.Roots()
	if !reflect.DeepEqual(roots, []string{"/mnt/movies", "/mnt/tv"}) {
		t.Fatalf("roots = %v", roots)
	}
	cases := map[string]bool{
		"/mnt/movies/a.mkv":       true,
		"/mnt/movies":             true,
		"/mnt/moviesx/a.mkv":      false,
		"/etc/passwd":             false,
		"/mnt/movies/../x/a.mkv":  false,
		"/mnt/tv/Show/S01/e1.mkv": true,
	}
	for p, want := range cases {
		if got := Within(roots, p); got != want {
			t.Errorf("Within(%q) = %v, want %v", p, got, want)
		}
	}
}
