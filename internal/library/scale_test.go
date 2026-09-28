package library_test

// A scale test for large libraries. It does not run by default:
//
//	JELLYTRIM_SCALE_ITEMS=100000 go test -run TestScale -v -timeout 60m ./internal/library/
//
// It builds a fake Jellyfin with N movies, sparse files on disk and
// realistic ffprobe output (many subtitle tracks and chapters, as real films
// have), then times each operation a large library stresses and reports
// peak memory and the database size. The fake prober is instant, so the
// first-scan time excludes ffprobe itself; the report estimates that.
//
// With JELLYTRIM_SCALE_PROFILE set to a directory, each operation also
// writes a CPU profile there, named after it. From 20,000 items up, the
// operations that grow with the library must stay within loose limits (see
// scaleLimits), so a regression fails the test.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/freakyturtle/jellytrim/internal/jellyfin/jellyfintest"
	"github.com/freakyturtle/jellytrim/internal/library"
	"github.com/freakyturtle/jellytrim/internal/media"
	"github.com/freakyturtle/jellytrim/internal/policy"
	"github.com/freakyturtle/jellytrim/internal/queue"
	"github.com/freakyturtle/jellytrim/internal/store"
)

type scaleEncoders struct{}

func (scaleEncoders) Select(media.Codec, bool, string) (string, bool, string) {
	return "x265", true, ""
}

// scaleProber answers every probe from a small set of realistic outputs,
// chosen by the file name's number.
type scaleProber struct {
	variants [][2][]byte
	calls    atomic.Int64
}

func (p *scaleProber) Probe(_ context.Context, path string) ([]byte, []byte, error) {
	p.calls.Add(1)
	n, _ := strconv.Atoi(filepath.Base(filepath.Dir(path))[len("Film "):])
	v := p.variants[n%len(p.variants)]
	return v[0], v[1], nil
}

// inflate adds subtitle tracks and chapters to a fixture so its JSON is the
// size of a real film's ffprobe output (about 30 KB).
func inflate(t testing.TB, name string) [2][]byte {
	t.Helper()
	dir := filepath.Join("..", "media", "testdata", "probe")
	raw, err := os.ReadFile(filepath.Join(dir, name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	frames, _ := os.ReadFile(filepath.Join(dir, name+".frames.json"))
	var d map[string]any
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	streams := d["streams"].([]any)
	langs := []string{"eng", "fre", "ger", "spa", "ita", "jpn", "por", "dut", "swe", "nor", "dan", "fin", "pol", "cze", "hun"}
	for i, lang := range langs {
		streams = append(streams, map[string]any{
			"index": len(streams), "codec_name": "subrip", "codec_type": "subtitle",
			"disposition": map[string]any{"default": 0, "forced": 0, "hearing_impaired": 0},
			"tags":        map[string]any{"language": lang, "title": fmt.Sprintf("Subtitles %d", i+1), "BPS": "80", "DURATION": "01:58:00.000000000", "NUMBER_OF_FRAMES": "1500", "NUMBER_OF_BYTES": "70000", "_STATISTICS_WRITING_APP": "mkvmerge v80.0", "_STATISTICS_TAGS": "BPS DURATION NUMBER_OF_FRAMES NUMBER_OF_BYTES"},
		})
	}
	d["streams"] = streams
	var chapters []any
	for i := 0; i < 30; i++ {
		chapters = append(chapters, map[string]any{
			"id": i, "time_base": "1/1000000000", "start": i * 240_000_000_000, "end": (i + 1) * 240_000_000_000,
			"start_time": fmt.Sprintf("%d.000000", i*240), "end_time": fmt.Sprintf("%d.000000", (i+1)*240),
			"tags": map[string]any{"title": fmt.Sprintf("Chapter %02d", i+1)},
		})
	}
	d["chapters"] = chapters
	format := d["format"].(map[string]any)
	format["duration"] = "7200.000000"
	format["size"] = "25000000000"
	if br, ok := format["bit_rate"]; ok && br != nil {
		format["bit_rate"] = "27000000"
	}
	out, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	return [2][]byte{out, frames}
}

type timing struct {
	name string
	took time.Duration
	peak uint64 // heap in use, bytes
	note string
}

// heapSampler records the peak heap in use since the last reset.
type heapSampler struct {
	peak atomic.Uint64
	stop chan struct{}
	done chan struct{}
}

func sampleHeap() *heapSampler {
	h := &heapSampler{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(h.done)
		var ms runtime.MemStats
		for {
			runtime.ReadMemStats(&ms)
			for {
				old := h.peak.Load()
				if ms.HeapInuse <= old || h.peak.CompareAndSwap(old, ms.HeapInuse) {
					break
				}
			}
			select {
			case <-h.stop:
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}()
	return h
}

// reset starts a new measurement from the heap in use now.
func (h *heapSampler) reset() {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	h.peak.Store(ms.HeapInuse)
}

func (h *heapSampler) close() {
	close(h.stop)
	<-h.done
}

// scaleLimit is the most an operation may take at n items: a fixed part
// plus a part per item, so the limit grows linearly with the library.
type scaleLimit struct {
	took    time.Duration // plus perItem × n
	perItem time.Duration
	heap    uint64 // bytes, plus heapPerItem × n
	heapPer uint64
}

// scaleLimits are about twice what was measured at 20,000 and 100,000
// items on a laptop (Apple M-series, 12 cores), fitted as a fixed part plus
// a part per item, so only a real regression fails. Before evaluation read
// probe summaries instead of parsing every probe, re-evaluating 20,000
// items took 2.9 s and peaked at over 800 MB, and a preview took 2.6 s;
// each would fail these. The heap is what the Go runtime holds in use,
// including the test's own fake Jellyfin and garbage not yet collected, so
// it runs at two to four times the live data. Operations not listed are
// reported but not checked.
var scaleLimits = map[string]scaleLimit{
	// Measured: 1.7 s and 8.3 s.
	opSecondSyncOwn: {took: 200 * time.Millisecond, perItem: 170 * time.Microsecond, heap: 100 << 20, heapPer: 16 << 10},
	// Measured: 0.64 s, 81 MB and 3.0 s, 274 MB, since evaluations are
	// saved a page at a time. Before, re-evaluating held the whole library
	// and every evaluation: 0.86 s, 176 MB and 4.3 s, 760 MB. Most of the
	// heap at 100,000 items is the fake Jellyfin's (about 115 MB live, which
	// the garbage collector lets grow to twice that); evaluation itself holds
	// about 25 MB.
	opReevaluate:   {took: 100 * time.Millisecond, perItem: 60 * time.Microsecond, heap: 50 << 20, heapPer: 5 << 10},
	opPolicyChange: {took: 100 * time.Millisecond, perItem: 60 * time.Microsecond, heap: 50 << 20, heapPer: 5 << 10},
	// Measured: 0.22 s, 73 MB and 0.85 s, 218 MB (0.17 s, 121 MB and
	// 0.64 s, 445 MB before the library was read a page at a time).
	opPreview:     {took: 100 * time.Millisecond, perItem: 12 * time.Microsecond, heap: 50 << 20, heapPer: 4 << 10},
	opPreviewWide: {took: 100 * time.Millisecond, perItem: 12 * time.Microsecond, heap: 50 << 20, heapPer: 4 << 10},
}

const (
	opSecondSync    = "second sync (nothing changed)"
	opFakeJellyfin  = "  of which the fake Jellyfin (its requests replayed)"
	opSecondSyncOwn = "  of which JellyTrim"
	opReevaluate    = "re-evaluate (after a policy or setting change)"
	opPolicyChange  = "re-evaluate after one policy change (save + evaluate)"
	opPreview       = "policy editor preview, last starter policy"
	opPreviewWide   = "policy editor preview, new policy deciding most items"
)

func checkScaleLimits(t *testing.T, n int, results []timing) {
	t.Helper()
	for _, r := range results {
		l, ok := scaleLimits[r.name]
		if !ok {
			continue
		}
		maxTook := l.took + time.Duration(n)*l.perItem
		maxHeap := l.heap + uint64(n)*l.heapPer
		if r.took > maxTook {
			t.Errorf("%s took %s, limit %s at %d items", r.name, r.took.Round(time.Millisecond), maxTook, n)
		}
		if r.peak > maxHeap {
			t.Errorf("%s peaked at %.0f MB of heap, limit %.0f MB at %d items", r.name, float64(r.peak)/1e6, float64(maxHeap)/1e6, n)
		}
	}
}

// replay sends the GET requests again, so their time shows how much of a
// sync is spent in the fake Jellyfin. The fake sorts every item for every page,
// which a real server does from an index, so this is test overhead to
// subtract from sync times.
func replay(t *testing.T, base string, reqs []jellyfintest.Request) int {
	t.Helper()
	n := 0
	for _, r := range reqs {
		if r.Method != http.MethodGet {
			continue
		}
		req, err := http.NewRequest(http.MethodGet, base+r.Path+"?"+r.Query.Encode(), nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", r.Authorization)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		n++
	}
	return n
}

// profileName turns an operation's name into a file name.
func profileName(op string) string {
	b := []byte(strings.ToLower(op))
	for i, c := range b {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			b[i] = '-'
		}
	}
	return string(b) + ".cpu"
}

func TestScale(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("JELLYTRIM_SCALE_ITEMS"))
	if n <= 0 {
		t.Skip("set JELLYTRIM_SCALE_ITEMS to run the scale test")
	}
	ctx := context.Background()
	configDir := t.TempDir()
	root := t.TempDir()
	st, err := store.Open(ctx, configDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	// Fake Jellyfin with one user, one library and N films.
	jf := jellyfintest.New(t)
	libID, userID := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	jf.AddLibrary(jellyfintest.Library{Name: "Movies", CollectionType: "movies", ItemID: libID, Locations: []string{"/media/movies"}})
	jf.AddUser(jellyfintest.User{ID: userID, Name: "alex", IsAdministrator: true})
	added := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	played := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	start := time.Now()
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("Film %d", i)
		jfPath := fmt.Sprintf("/media/movies/%s/%s.mkv", name, name)
		id := fmt.Sprintf("%032x", i+1)
		jf.AddItem(jellyfintest.Item{
			ID: id, Name: name, SortName: name, Type: "Movie", Path: jfPath, LibraryID: libID, ParentID: libID,
			DateCreated: added, LocationType: "FileSystem", VideoType: "VideoFile", ProductionYear: 2000 + i%25,
			MediaSources: []jellyfintest.MediaSource{{ID: id, Path: jfPath, Protocol: "File", Container: "mkv", Size: 25_000_000_000, VideoType: "VideoFile"}},
		})
		if i%3 == 0 {
			jf.SetUserData(userID, id, jellyfintest.UserData{Played: true, PlayCount: 1, LastPlayedDate: &played, IsFavorite: i%50 == 0})
		}
		local := filepath.Join(root, "movies", name, name+".mkv")
		if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
			t.Fatal(err)
		}
		f, err := os.Create(local)
		if err != nil {
			t.Fatal(err)
		}
		_ = f.Truncate(25_000_000_000) // sparse: no real disk use
		_ = f.Close()
	}
	t.Logf("setup: %d items and files in %s", n, time.Since(start).Round(time.Millisecond))

	prober := &scaleProber{}
	for _, fx := range []string{"h264-1080p", "hevc-1080p", "h264-2160p", "hevc-2160p-hdr10", "multi-audio-subs", "av1-1080p"} {
		prober.variants = append(prober.variants, inflate(t, fx))
	}
	lib := library.New(library.Options{Store: st, Prober: prober, Encoders: scaleEncoders{}})
	if _, err := lib.SaveConnection(ctx, jf.URL, jf.APIKey()); err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(lib.RefreshServerInfo(ctx))
	must(st.SetManagedLibraries(ctx, []string{libID}))
	must(st.SetSelectedUsers(ctx, []string{userID}))
	must(st.ReplacePathMappings(ctx, []store.PathMapping{{JellyfinPrefix: "/media/movies", LocalPrefix: filepath.ToSlash(filepath.Join(root, "movies"))}}))
	must(lib.CreateStarterPolicies(ctx, nil))
	rows, _ := st.Policies(ctx)
	for _, p := range rows {
		must(st.SetPolicyEnabled(ctx, p.ID, true))
	}

	heap := sampleHeap()
	profileDir := os.Getenv("JELLYTRIM_SCALE_PROFILE")
	var results []timing
	measure := func(name string, fn func() string) {
		t.Helper()
		runtime.GC()
		heap.reset()
		if profileDir != "" {
			f, err := os.Create(filepath.Join(profileDir, profileName(name)))
			must(err)
			must(pprof.StartCPUProfile(f))
			defer func() { pprof.StopCPUProfile(); _ = f.Close() }()
		}
		s := time.Now()
		note := fn()
		results = append(results, timing{name, time.Since(s), heap.peak.Load(), note})
	}

	measure("first sync (sync + probe + evaluate)", func() string {
		res, err := lib.Run(ctx)
		must(err)
		return fmt.Sprintf("%d items, %d probed", res.Items, res.Probed)
	})
	before := len(jf.Requests())
	measure(opSecondSync, func() string {
		res, err := lib.Run(ctx)
		must(err)
		return fmt.Sprintf("%d probed", res.Probed)
	})
	syncRequests := jf.Requests()[before:]
	measure(opFakeJellyfin, func() string {
		return fmt.Sprintf("%d requests; test overhead", replay(t, jf.URL, syncRequests))
	})
	// JellyTrim's own share of the second sync, which the limits check: the
	// fake Jellyfin's time grows with the square of the library.
	own := results[len(results)-2]
	own.name, own.took = opSecondSyncOwn, own.took-results[len(results)-1].took
	own.note = "second sync minus the fake Jellyfin's time"
	results = append(results, own)
	measure("inspect changed files (none) + evaluate", func() string {
		probed, _, err := lib.Probe(ctx)
		must(err)
		return fmt.Sprintf("%d probed", probed)
	})
	measure(opReevaluate, func() string {
		k, err := lib.Evaluate(ctx)
		must(err)
		return fmt.Sprintf("%d evaluations", k)
	})
	ps, _ := lib.Policies(ctx)
	measure(opPolicyChange, func() string {
		archive := ps[1] // Archive watched 4K
		archive.Conditions.All[1].Number = 60
		_, err := lib.SavePolicy(ctx, archive)
		must(err)
		k, err := lib.Evaluate(ctx)
		must(err)
		return fmt.Sprintf("%d evaluations", k)
	})
	previewNote := func(pv library.Preview) string {
		note := fmt.Sprintf("%d matches, %d wins, %d optimise", pv.Matches, pv.Wins, pv.Optimise)
		if pv.Sampled {
			note += fmt.Sprintf(" (estimated from %d)", pv.SampleSize)
		}
		return note
	}
	measure(opPreview, func() string {
		pv, err := lib.Preview(ctx, ps[len(ps)-1])
		must(err)
		return previewNote(pv)
	})
	measure(opPreviewWide, func() string {
		pv, err := lib.Preview(ctx, policy.Policy{Name: "Everything to HEVC", Priority: 1,
			Action: policy.Action{Kind: policy.KindOptimise, MaxResolution: "1080p", Codec: "hevc"}})
		must(err)
		return previewNote(pv)
	})
	measure("Dry Run summary (dashboard)", func() string {
		sum, err := lib.DryRun(ctx)
		must(err)
		return fmt.Sprintf("%d would be optimised", sum.Optimise)
	})
	measure("Library page, default", func() string {
		_, total, err := st.LibraryList(ctx, store.LibraryFilter{Limit: 100})
		must(err)
		return fmt.Sprintf("%d total", total)
	})
	measure("Library page, filtered (needs optimisation, H.264)", func() string {
		_, total, err := st.LibraryList(ctx, store.LibraryFilter{Outcome: "optimise", Codec: "h264", Limit: 100})
		must(err)
		return fmt.Sprintf("%d total", total)
	})
	measure("Library page, search", func() string {
		_, total, err := st.LibraryList(ctx, store.LibraryFilter{Search: "Film 123", Limit: 100})
		must(err)
		return fmt.Sprintf("%d total", total)
	})
	measure("Library page, sorted by saving, page 50", func() string {
		_, total, err := st.LibraryList(ctx, store.LibraryFilter{Sort: "saving", Limit: 100, Offset: 4900})
		must(err)
		return fmt.Sprintf("%d total", total)
	})
	measure("dashboard totals", func() string {
		tot, err := st.Totals(ctx)
		must(err)
		return fmt.Sprintf("%d items", tot.Items)
	})
	q := queue.New(queue.Options{Store: st, Library: lib})
	must(st.SetSetting(ctx, store.KeyDryRun, "false"))
	measure("queue everything the policies chose", func() string {
		k, err := q.EnqueueMatching(ctx)
		must(err)
		return fmt.Sprintf("%d jobs", k)
	})
	measure("queue page (active jobs)", func() string {
		jobs, err := st.ActiveJobs(ctx)
		must(err)
		return fmt.Sprintf("%d jobs", len(jobs))
	})
	heap.close()

	size := int64(0)
	for _, f := range []string{store.FileName, store.FileName + "-wal"} {
		if fi, err := os.Stat(filepath.Join(configDir, f)); err == nil {
			size += fi.Size()
		}
	}
	t.Logf("\nScale test: %d items\n", n)
	var peak uint64
	t.Logf("  %-56s %10s %10s", "operation", "time", "peak heap")
	for _, r := range results {
		t.Logf("  %-56s %10s %7.0f MB   %s", r.name, r.took.Round(time.Millisecond), float64(r.peak)/1e6, r.note)
		peak = max(peak, r.peak)
	}
	t.Logf("  %-56s %10.1f MB", "database size", float64(size)/1e6)
	t.Logf("  %-56s %10.1f MB", "peak heap in use", float64(peak)/1e6)
	t.Logf("  %-56s %10.0f KB", "ffprobe output per item (average)", float64(len(prober.variants[4][0]))/1e3)
	t.Logf("  %-56s %10s", "first scan with real ffprobe (0.5 s per file, 2 at a time)", (time.Duration(n) * 250 * time.Millisecond).Round(time.Minute))
	if n >= 20000 {
		checkScaleLimits(t, n, results)
	}
}
