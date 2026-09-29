package queue

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/freakyturtle/jellytrim/internal/jellyfin"
	"github.com/freakyturtle/jellytrim/internal/jellyfin/jellyfintest"
	"github.com/freakyturtle/jellytrim/internal/library"
	"github.com/freakyturtle/jellytrim/internal/pipeline"
	"github.com/freakyturtle/jellytrim/internal/store"
	"github.com/freakyturtle/jellytrim/internal/timetable"
)

// fixtureAlpha is "Alpha (2019)" in the fake Jellyfin's fixtures.
const fixtureAlpha = "d15b890b81018d54a2b0f65fbee75563"

// playbackQueue is a queue connected to a fake Jellyfin, with no encoder:
// enough to exercise the playback checks without ffmpeg. The client does
// not retry, so each check is one request.
func playbackQueue(t *testing.T, w PlaybackWait) (*Service, *jellyfintest.Server) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir())
	must(t, err)
	t.Cleanup(func() { _ = st.Close() })
	jf := jellyfintest.New(t, jellyfintest.WithFixtures())
	lib := library.New(library.Options{Store: st, NewClient: func(url, key string) (*jellyfin.Client, error) {
		return jellyfin.New(url, key, jellyfin.WithRetry(1, 0, 0))
	}})
	_, err = lib.SaveConnection(ctx, jf.URL, jf.APIKey())
	must(t, err)
	return New(Options{Store: st, Library: lib, Playback: w}), jf
}

func TestStillPlayingMessage(t *testing.T) {
	want := "Someone was still watching this after 6 hours, so JellyTrim will try again later."
	if got := errStillPlaying(DefaultPlaybackLimit).Error(); got != want {
		t.Fatalf("got %q", got)
	}
}

func TestWaitUntilFree(t *testing.T) {
	fast := PlaybackWait{Poll: 5 * time.Millisecond, Limit: time.Minute, Unreachable: time.Minute}
	tests := []struct {
		name    string
		wait    PlaybackWait
		setup   func(jf *jellyfintest.Server)
		during  func(t *testing.T, q *Service, jf *jellyfintest.Server) // runs while waiting
		warning string
		err     string
	}{
		{name: "nothing playing goes ahead at once", wait: fast, setup: func(*jellyfintest.Server) {}},
		{name: "another item playing does not hold it up", wait: fast, setup: func(jf *jellyfintest.Server) {
			jf.SetPlaying("0123456789abcdef0123456789abcdef", "alex", "Living Room TV", false)
		}},
		{
			name: "waits while the item plays, then goes ahead",
			wait: fast,
			setup: func(jf *jellyfintest.Server) {
				jf.AddSession(jellyfintest.Session{UserName: "dev", DeviceName: "Firefox"})
				jf.SetPlaying(fixtureAlpha, "alex", "Living Room TV", false)
			},
			during: func(t *testing.T, q *Service, jf *jellyfintest.Server) {
				waitNote(t, q, "Waiting: alex is watching this on Living Room TV")
				jf.StopPlaying(fixtureAlpha)
			},
		},
		{
			name: "a paused session counts as watching",
			wait: fast,
			setup: func(jf *jellyfintest.Server) {
				jf.SetPlaying(fixtureAlpha, "sam", "Phone", true)
			},
			during: func(t *testing.T, q *Service, jf *jellyfintest.Server) {
				waitNote(t, q, "Waiting: sam has this paused on Phone")
				jf.StopPlaying(fixtureAlpha)
			},
		},
		{
			name: "gives up when still playing at the limit",
			wait: PlaybackWait{Poll: 5 * time.Millisecond, Limit: 50 * time.Millisecond, Unreachable: time.Minute},
			setup: func(jf *jellyfintest.Server) {
				jf.SetPlaying(fixtureAlpha, "alex", "Living Room TV", true)
			},
			err: "Someone was still watching this after",
		},
		{
			name: "Jellyfin down past the grace period goes ahead with a warning",
			wait: PlaybackWait{Poll: 5 * time.Millisecond, Limit: time.Minute, Unreachable: 50 * time.Millisecond},
			setup: func(jf *jellyfintest.Server) {
				jf.SetPlaying(fixtureAlpha, "alex", "Living Room TV", false)
				for range 1000 {
					jf.FailNext("/Sessions", http.StatusServiceUnavailable, 0)
				}
			},
			warning: UnreachableWarning,
		},
		{
			name: "a rejected key never goes ahead unchecked",
			wait: PlaybackWait{Poll: 5 * time.Millisecond, Limit: 100 * time.Millisecond, Unreachable: time.Millisecond},
			setup: func(jf *jellyfintest.Server) {
				for range 1000 {
					jf.FailNext("/Sessions", http.StatusUnauthorized, 0)
				}
			},
			err: "JellyTrim could not confirm with Jellyfin that nobody was watching this after",
		},
		{
			name: "Jellyfin back within the grace period is asked again",
			wait: PlaybackWait{Poll: 5 * time.Millisecond, Limit: time.Minute, Unreachable: time.Minute},
			setup: func(jf *jellyfintest.Server) {
				jf.FailNext("/Sessions", http.StatusBadGateway, 0)
				jf.FailNext("/Sessions", http.StatusServiceUnavailable, 0)
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q, jf := playbackQueue(t, tc.wait)
			tc.setup(jf)
			q.setLive(1, func(l *Live) { l.Status = store.JobReplacing })
			done := make(chan struct{})
			var warning string
			var err error
			go func() {
				defer close(done)
				warning, _, err = q.waitUntilFree(context.Background(), 1, fixtureAlpha)
			}()
			if tc.during != nil {
				tc.during(t, q, jf)
			}
			select {
			case <-done:
			case <-time.After(30 * time.Second):
				t.Fatal("waitUntilFree did not return")
			}
			if warning != tc.warning {
				t.Errorf("warning %q, want %q", warning, tc.warning)
			}
			if (tc.err == "") != (err == nil) || (err != nil && !strings.HasPrefix(err.Error(), tc.err)) {
				t.Errorf("err %v, want %q", err, tc.err)
			}
		})
	}
}

func TestWaitUntilFreeStopsWhenCancelled(t *testing.T) {
	q, jf := playbackQueue(t, PlaybackWait{Poll: time.Hour})
	jf.SetPlaying(fixtureAlpha, "alex", "Living Room TV", false)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, _, err := q.waitUntilFree(ctx, 1, fixtureAlpha)
		done <- err
	}()
	waitNote(t, q, "Waiting: alex is watching this on Living Room TV")
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("did not stop when cancelled")
	}
}

func TestCheckNotPlaying(t *testing.T) {
	tests := []struct {
		name  string
		setup func(jf *jellyfintest.Server)
		want  error
		text  string
	}{
		{"nothing playing is allowed", func(*jellyfintest.Server) {}, nil, ""},
		{"playing is refused, naming who", func(jf *jellyfintest.Server) {
			jf.SetPlaying(fixtureAlpha, "alex", "Living Room TV", false)
		}, ErrInUse, "Someone is watching this right now (alex on Living Room TV). Try again when they have finished."},
		{"paused is refused", func(jf *jellyfintest.Server) {
			jf.SetPlaying(fixtureAlpha, "alex", "Living Room TV", true)
		}, ErrInUse, ""},
		{"Jellyfin unreachable is refused", func(jf *jellyfintest.Server) {
			jf.Close()
		}, ErrPlaybackUnknown, "JellyTrim could not check with Jellyfin whether this is playing. Try again in a moment."},
		{"a rejected key is refused", func(jf *jellyfintest.Server) {
			jf.FailNext("/Sessions", http.StatusUnauthorized, 0)
		}, ErrPlaybackUnknown, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q, jf := playbackQueue(t, PlaybackWait{})
			tc.setup(jf)
			err := q.checkNotPlaying(context.Background(), fixtureAlpha, false)
			if !errors.Is(err, tc.want) || (tc.want == nil) != (err == nil) {
				t.Fatalf("err %v, want %v", err, tc.want)
			}
			if tc.text != "" && err.Error() != tc.text {
				t.Fatalf("message %q", err.Error())
			}
		})
	}
}

func TestScheduleLeavesJobWaitingForPlaybackAlone(t *testing.T) {
	q, _ := playbackQueue(t, PlaybackWait{})
	replacing, stopReplacing := context.WithCancel(context.Background())
	encoding, stopEncoding := context.WithCancel(context.Background())
	defer stopReplacing()
	defer stopEncoding()
	q.running[1], q.live[1] = stopReplacing, Live{Status: store.JobReplacing, Note: "Waiting: alex is watching this"}
	q.running[2], q.live[2] = stopEncoding, Live{Status: store.JobEncoding}
	q.stopForSchedule()
	if replacing.Err() != nil || q.scheduleStopped[1] {
		t.Error("the schedule stopped a job waiting for playback")
	}
	if encoding.Err() == nil || !q.scheduleStopped[2] {
		t.Error("the schedule did not stop an encoding job")
	}
}

func waitNote(t *testing.T, q *Service, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		for _, l := range q.Live() {
			if l.Note == want {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no job has the note %q; live %+v", want, q.Live())
}

// --- end to end, with ffmpeg ------------------------------------------------

func assertUntouched(t *testing.T, e env) {
	t.Helper()
	b, _ := os.ReadFile(e.alpha)
	if sha256.Sum256(b) != e.hash {
		t.Fatal("original changed")
	}
}

func TestJobWaitsForPlaybackThenReplaces(t *testing.T) {
	e := setup(t)
	e.q.playback = PlaybackWait{Poll: 20 * time.Millisecond}.withDefaults()
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); e.q.Stop(); e.lib.Wait() }()
	// A slow preset keeps the encode going while playback starts.
	must(t, e.st.SetSettings(ctx, map[string]string{store.KeyDryRun: "false", store.KeyX265Preset: "slower"}))
	e.q.Start(ctx)
	jobID, _, err := e.q.Enqueue(ctx, alphaID(t, e), "manual")
	must(t, err)
	waitFor(t, e, jobID, store.JobEncoding)
	e.jf.SetPlaying(fixtureAlpha, "alex", "Living Room TV", false)

	waitNote(t, e.q, "Waiting: alex is watching this on Living Room TV")
	if j, _ := e.st.Job(ctx, jobID); j.Status != store.JobReplacing {
		t.Fatalf("status while waiting %s", j.Status)
	}
	assertUntouched(t, e)
	if _, err := os.Stat(pipeline.PartialPath(e.alpha, jobID)); err != nil {
		t.Fatalf("the validated new file should wait beside the original: %v", err)
	}
	// The schedule ending does not stop a job that has finished encoding.
	must(t, e.st.SetSetting(ctx, store.KeyProcessingSchedule, timetable.Week{}.String()))
	e.q.Wake()
	time.Sleep(200 * time.Millisecond)
	if l, ok := e.q.Live()[jobID]; !ok || l.Note == "" {
		t.Fatalf("the waiting job was stopped: %+v", e.q.Live())
	}

	e.jf.StopPlaying(fixtureAlpha)
	j := waitFor(t, e, jobID, store.JobComplete, store.JobFailed, store.JobSkipped, store.JobWaiting)
	if j.Status != store.JobComplete {
		t.Fatalf("job %s: %s\n%s", j.Status, j.Summary, j.Diagnostics)
	}
	e.lib.Wait()

	// Restore is refused while someone watches, or when Jellyfin cannot say.
	e.jf.SetPlaying(fixtureAlpha, "alex", "Living Room TV", true)
	if err := e.q.Restore(ctx, jobID, false); !errors.Is(err, ErrInUse) || !strings.Contains(err.Error(), "alex on Living Room TV") {
		t.Fatalf("restore while playing: %v", err)
	}
	e.jf.StopPlaying(fixtureAlpha)
	for range 3 { // every attempt the client makes
		e.jf.FailNext("/Sessions", http.StatusServiceUnavailable, 0)
	}
	if err := e.q.Restore(ctx, jobID, false); !errors.Is(err, ErrPlaybackUnknown) {
		t.Fatalf("restore with Jellyfin down: %v", err)
	}
	must(t, e.q.Restore(ctx, jobID, false))
	assertUntouched(t, e)
}

func TestJobGivesUpWhenStillWatched(t *testing.T) {
	e := setup(t)
	e.q.playback = PlaybackWait{Poll: 20 * time.Millisecond, Limit: 300 * time.Millisecond}.withDefaults()
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); e.q.Stop(); e.lib.Wait() }()
	must(t, e.st.SetSettings(ctx, map[string]string{store.KeyDryRun: "false", store.KeyX265Preset: "slower"}))
	e.q.Start(ctx)
	jobID, _, err := e.q.Enqueue(ctx, alphaID(t, e), "manual")
	must(t, err)
	waitFor(t, e, jobID, store.JobEncoding)
	e.jf.SetPlaying(fixtureAlpha, "alex", "Living Room TV", true)
	waitFor(t, e, jobID, store.JobReplacing)
	e.q.SetPaused(true) // so the requeued job stays waiting

	deadline := time.Now().Add(2 * time.Minute)
	var j store.Job
	for time.Now().Before(deadline) {
		j, _ = e.st.Job(ctx, jobID)
		if j.Status == store.JobWaiting && j.StartedAt == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	want := "Someone was still watching this after 0 s, so JellyTrim will try again later. The original is unchanged."
	if j.Status != store.JobWaiting || j.Summary != want {
		t.Fatalf("job %s: %q", j.Status, j.Summary)
	}
	assertUntouched(t, e)
	entries, _ := os.ReadDir(filepath.Dir(e.alpha))
	for _, en := range entries {
		if strings.Contains(en.Name(), ".jellytrim-") {
			t.Fatalf("left behind %s", en.Name())
		}
	}
	// Held back, so a client left paused does not cause an encode loop.
	if !e.q.isDeferred(fixtureAlpha) {
		t.Fatal("a job that gave up must be held back before it runs again")
	}
}

func TestPlayingAtStartIsNotEncoded(t *testing.T) {
	e := setup(t)
	e.q.playback = PlaybackWait{Poll: 20 * time.Millisecond}.withDefaults()
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); e.q.Stop(); e.lib.Wait() }()
	must(t, e.st.SetSetting(ctx, store.KeyDryRun, "false"))
	e.jf.SetPlaying(fixtureAlpha, "alex", "Living Room TV", false)
	e.q.Start(ctx)
	jobID, _, err := e.q.Enqueue(ctx, alphaID(t, e), "manual")
	must(t, err)

	deadline := time.Now().Add(30 * time.Second)
	var j store.Job
	for time.Now().Before(deadline) {
		j, _ = e.st.Job(ctx, jobID)
		if j.Status == store.JobWaiting && strings.HasPrefix(j.Summary, "Not started") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	want := "Not started: alex is watching this on Living Room TV. JellyTrim will try again in 30 min; other jobs go first."
	if j.Status != store.JobWaiting || j.Summary != want {
		t.Fatalf("job %s: %q", j.Status, j.Summary)
	}
	if !e.q.isDeferred(fixtureAlpha) {
		t.Fatal("the item should be held back")
	}
	// It is not picked up again while held back, even with nothing else to do.
	e.q.Wake()
	time.Sleep(300 * time.Millisecond)
	if j, _ := e.st.Job(ctx, jobID); j.Status != store.JobWaiting || j.StartedAt != nil {
		t.Fatalf("a held-back job started: %s", j.Status)
	}
	if _, err := os.Stat(pipeline.PartialPath(e.alpha, jobID)); !os.IsNotExist(err) {
		t.Fatal("nothing should have been encoded")
	}
	assertUntouched(t, e)
}

func TestJobGoesAheadWhenJellyfinCannotBeAsked(t *testing.T) {
	e := setup(t)
	e.q.playback = PlaybackWait{Poll: 20 * time.Millisecond, Unreachable: 200 * time.Millisecond}.withDefaults()
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); e.q.Stop(); e.lib.Wait() }()
	must(t, e.st.SetSetting(ctx, store.KeyDryRun, "false"))
	e.jf.SetPlaying(fixtureAlpha, "alex", "Living Room TV", false)
	for range 100 {
		e.jf.FailNext("/Sessions", http.StatusServiceUnavailable, 0)
	}
	e.q.Start(ctx)
	jobID, _, err := e.q.Enqueue(ctx, alphaID(t, e), "manual")
	must(t, err)
	j := waitFor(t, e, jobID, store.JobComplete, store.JobFailed, store.JobSkipped)
	if j.Status != store.JobComplete {
		t.Fatalf("job %s: %s", j.Status, j.Summary)
	}
	if !strings.Contains(j.Diagnostics, UnreachableWarning) {
		t.Fatalf("diagnostics lack the warning: %s", j.Diagnostics)
	}
}
