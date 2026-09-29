package queue

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/freakyturtle/jellytrim/internal/jellyfin"
	"github.com/freakyturtle/jellytrim/internal/jellyfin/jellyfintest"
	"github.com/freakyturtle/jellytrim/internal/library"
	"github.com/freakyturtle/jellytrim/internal/pipeline"
	"github.com/freakyturtle/jellytrim/internal/store"
)

// answer is one scripted reply to "is the item playing?".
type answer struct {
	playing bool
	err     error
}

var (
	free    = answer{}
	playing = answer{playing: true}
	down    = answer{err: &jellyfin.StatusError{Method: "GET", Path: "/Sessions", StatusCode: http.StatusServiceUnavailable}}
)

// script answers in order, repeating the last answer, and counts the asks.
type script struct {
	mu      sync.Mutex
	answers []answer
	asked   int
}

func (s *script) ask(context.Context, string) (*jellyfin.Playing, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.answers[min(s.asked, len(s.answers)-1)]
	s.asked++
	switch {
	case a.err != nil:
		return nil, a.err
	case a.playing:
		return &jellyfin.Playing{UserName: "alex", DeviceName: "Living Room TV"}, nil
	}
	return nil, nil
}

func (s *script) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.asked
}

func TestWaitUntilFreeRules(t *testing.T) {
	patient := PlaybackWait{Poll: time.Millisecond, Limit: time.Minute, Unreachable: time.Millisecond}
	short := PlaybackWait{Poll: time.Millisecond, Limit: 50 * time.Millisecond, Unreachable: time.Millisecond}
	unauthorised := &jellyfin.StatusError{Method: "GET", Path: "/Sessions", StatusCode: http.StatusUnauthorized}
	tests := []struct {
		name    string
		wait    PlaybackWait
		answers []answer
		asked   int // exact number of asks, when not zero
		seen    bool
		warning string
		err     string // prefix of the give-up error
	}{
		{name: "nothing playing goes ahead after one answer", wait: patient, answers: []answer{free}, asked: 1},
		{
			name: "after playback, one free answer is not enough", wait: patient,
			answers: []answer{playing, free, playing, free, free}, asked: 5, seen: true,
		},
		{
			name: "an error between free answers starts the count again", wait: patient,
			answers: []answer{playing, free, down, free, free}, asked: 5, seen: true,
		},
		{
			name: "Jellyfin down before anyone was seen goes ahead with a warning", wait: patient,
			answers: []answer{down}, warning: UnreachableWarning,
		},
		{
			name: "playing then unreachable never goes ahead unchecked", wait: short,
			answers: []answer{playing, down}, seen: true,
			err: "JellyTrim could not confirm with Jellyfin that nobody was watching this after",
		},
		{
			name: "a rejected key never goes ahead unchecked", wait: short,
			answers: []answer{{err: unauthorised}},
			err:     "JellyTrim could not confirm with Jellyfin that nobody was watching this after",
		},
		{
			name: "no Jellyfin connection never goes ahead unchecked", wait: short,
			answers: []answer{{err: library.ErrNotConfigured}},
			err:     "JellyTrim could not confirm with Jellyfin that nobody was watching this after",
		},
		{
			name: "still playing at the limit gives up", wait: short,
			answers: []answer{playing}, seen: true,
			err: "Someone was still watching this after",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q, _ := playbackQueue(t, tc.wait)
			s := &script{answers: tc.answers}
			q.playingFn = s.ask
			warning, seen, err := q.waitUntilFree(context.Background(), 1, fixtureAlpha)
			if warning != tc.warning || seen != tc.seen {
				t.Errorf("warning %q seen %v, want %q %v", warning, seen, tc.warning, tc.seen)
			}
			if (tc.err == "") != (err == nil) || (err != nil && !strings.HasPrefix(err.Error(), tc.err)) {
				t.Errorf("err %v, want %q", err, tc.err)
			}
			if tc.asked != 0 && s.count() != tc.asked {
				t.Errorf("asked %d times, want %d", s.count(), tc.asked)
			}
			if tc.err != "" && !q.isDeferred(fixtureAlpha) {
				t.Error("a job that gave up must hold the item back")
			}
		})
	}
}

func TestGiveUpsEndInSkipped(t *testing.T) {
	q, _ := playbackQueue(t, PlaybackWait{Poll: time.Millisecond, Limit: 10 * time.Millisecond})
	q.playingFn = (&script{answers: []answer{playing}}).ask
	var stop *pipeline.StopError
	for i := 1; i <= maxGiveUps; i++ {
		_, _, err := q.waitUntilFree(context.Background(), 7, fixtureAlpha)
		if i < maxGiveUps && (errors.As(err, &stop) || !strings.HasPrefix(err.Error(), "Someone was still watching")) {
			t.Fatalf("give-up %d: %v, want the job back in the queue", i, err)
		}
		if i == maxGiveUps && (!errors.As(err, &stop) || stop.Outcome != pipeline.Skipped || stop.Summary != errGaveUpForGood) {
			t.Fatalf("give-up %d: %v, want skipped", i, err)
		}
	}
	// Another job's count is its own.
	if _, _, err := q.waitUntilFree(context.Background(), 8, fixtureAlpha); errors.As(err, &stop) {
		t.Fatalf("job 8's first give-up: %v", err)
	}
	// A finished job's count is forgotten.
	q.finish(store.Job{ID: 7, ItemID: fixtureAlpha}, store.JobOutcome{Status: store.JobSkipped})
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, ok := q.giveUps[7]; ok {
		t.Fatal("the give-up count outlived the job")
	}
}

func TestCancelAndTheCommitPoint(t *testing.T) {
	q, _ := playbackQueue(t, PlaybackWait{})
	running := func() context.Context {
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		q.mu.Lock()
		q.running[1], q.live[1] = cancel, Live{Status: store.JobReplacing}
		delete(q.cancelled, 1)
		q.mu.Unlock()
		return ctx
	}
	t.Run("cancel before the commit point stops the job", func(t *testing.T) {
		ctx := running()
		must(t, q.Cancel(context.Background(), 1))
		if ctx.Err() == nil {
			t.Fatal("the job was not stopped")
		}
		if err := q.commit(ctx, 1); err == nil || q.Live()[1].Committed {
			t.Fatal("a cancelled job was committed")
		}
	})
	t.Run("cancel after the commit point is refused", func(t *testing.T) {
		ctx := running()
		must(t, q.commit(ctx, 1))
		if err := q.Cancel(context.Background(), 1); !errors.Is(err, ErrTooLate) {
			t.Fatalf("err %v, want ErrTooLate", err)
		}
		if ctx.Err() != nil || q.cancelled[1] {
			t.Fatal("a committed job was stopped")
		}
		want := "Too late to cancel: JellyTrim is replacing the file now. The original is kept as a backup."
		if ErrTooLate.Error() != want {
			t.Fatalf("message %q", ErrTooLate.Error())
		}
	})
	t.Run("cancel racing the commit point gets one clear answer", func(t *testing.T) {
		for range 200 {
			ctx := running()
			var commitErr, cancelErr error
			var wg sync.WaitGroup
			wg.Add(2)
			go func() { defer wg.Done(); commitErr = q.commit(ctx, 1) }()
			go func() { defer wg.Done(); cancelErr = q.Cancel(context.Background(), 1) }()
			wg.Wait()
			switch {
			case commitErr == nil && (!errors.Is(cancelErr, ErrTooLate) || ctx.Err() != nil):
				t.Fatalf("committed, but Cancel said %v and stopped=%v", cancelErr, ctx.Err() != nil)
			case commitErr != nil && (cancelErr != nil || !errors.Is(commitErr, context.Canceled)):
				t.Fatalf("not committed (%v), but Cancel said %v", commitErr, cancelErr)
			}
		}
	})
}

// --- the checks before replacing, with ffmpeg (Reassess probes the file) ---

// readyToReplace queues Alpha without starting the queue and builds the
// pipeline job as the worker would, so beforeReplace can be called directly.
func readyToReplace(t *testing.T, e env, answers ...answer) (store.Job, pipeline.Job) {
	t.Helper()
	ctx := context.Background()
	must(t, e.st.SetSetting(ctx, store.KeyDryRun, "false"))
	id := alphaID(t, e)
	jobID, _, err := e.q.Enqueue(ctx, id, "manual")
	must(t, err)
	j, err := e.st.Job(ctx, jobID)
	must(t, err)
	a, err := e.lib.Reassess(ctx, id)
	must(t, err)
	if a.Decision.Plan == nil {
		t.Fatalf("Alpha is not chosen: %s", a.Decision.Summary)
	}
	e.q.playback = PlaybackWait{Poll: time.Millisecond, Limit: time.Minute, Unreachable: time.Minute}.withDefaults()
	e.q.playingFn = (&script{answers: answers}).ask
	return j, pipeline.Job{ID: jobID, Path: a.Candidate.Item.LocalPath, Plan: *a.Decision.Plan, Identity: a.Identity}
}

func TestBeforeReplace(t *testing.T) {
	const stopped = "error" // a plain error: the job goes back to the queue
	tests := []struct {
		name    string
		answers []answer
		setup   func(t *testing.T, e env, j store.Job)
		want    pipeline.Outcome // "" goes ahead
		summary string           // prefix
		check   func(t *testing.T, e env, j store.Job)
	}{
		{
			name: "nothing changed goes ahead and is committed", answers: []answer{free},
			check: func(t *testing.T, e env, j store.Job) {
				if !e.q.Live()[j.ID].Committed {
					t.Fatal("not committed")
				}
			},
		},
		{
			name: "Dry Run turned on sends it back without replacing", answers: []answer{free},
			setup: func(t *testing.T, e env, _ store.Job) {
				must(t, e.st.SetSetting(context.Background(), store.KeyDryRun, "true"))
			},
			want: pipeline.Interrupted, summary: summaryDryRun,
		},
		{
			name: "watched during the wait and now a favourite is skipped", answers: []answer{playing, free, free},
			setup: func(_ *testing.T, e env, j store.Job) {
				e.jf.SetUserData(jellyfintest.FixtureUserID, j.ItemID, jellyfintest.UserData{IsFavorite: true})
			},
			want: pipeline.Skipped, summary: summaryWatchedNoLonger,
			check: func(t *testing.T, e env, j store.Job) {
				ud, err := e.st.ItemUserData(context.Background(), j.ItemID)
				must(t, err)
				if len(ud) != 1 || !ud[0].Favourite {
					t.Fatalf("the watch state was not re-read: %+v", ud)
				}
			},
		},
		{
			name: "watched during the wait with no fresh watch state gives up", answers: []answer{playing, free, free},
			setup: func(_ *testing.T, e env, j store.Job) {
				for range 5 { // every attempt the client makes
					e.jf.FailNext("/Items/"+j.ItemID, http.StatusServiceUnavailable, 0)
				}
			},
			want: stopped, summary: errNoWatchState.Error(),
			check: func(t *testing.T, e env, j store.Job) {
				if !e.q.isDeferred(j.ItemID) || e.q.giveUps[j.ID] != 1 {
					t.Fatal("a failed re-read must count as giving up")
				}
			},
		},
		{
			name: "favourited during the encode with nobody seen is still re-read and skipped", answers: []answer{free},
			setup: func(_ *testing.T, e env, j store.Job) {
				e.jf.SetUserData(jellyfintest.FixtureUserID, j.ItemID, jellyfintest.UserData{Played: true, IsFavorite: true})
			},
			want: pipeline.Skipped, summary: "Not replaced: the policies no longer choose this item (",
		},
		{
			name: "no fresh watch state with nobody seen uses the state from the start", answers: []answer{free},
			setup: func(_ *testing.T, e env, j store.Job) {
				for range 5 { // every attempt the client makes
					e.jf.FailNext("/Items/"+j.ItemID, http.StatusServiceUnavailable, 0)
				}
			},
			check: func(t *testing.T, e env, j store.Job) {
				if !e.q.Live()[j.ID].Committed {
					t.Fatal("not committed")
				}
			},
		},
		{
			name: "someone starting to watch during the last checks sends it back", answers: []answer{free, playing},
			want: stopped, summary: errStartedWatching.Error(),
			check: func(t *testing.T, e env, j store.Job) {
				if !e.q.isDeferred(j.ItemID) {
					t.Fatal("the item was not held back")
				}
			},
		},
		{
			name: "a policy turned off during the encode is skipped", answers: []answer{free},
			setup: func(t *testing.T, e env, _ store.Job) {
				ps, err := e.st.Policies(context.Background())
				must(t, err)
				for _, p := range ps {
					if p.Name == "Efficient encoding" {
						must(t, e.st.SetPolicyEnabled(context.Background(), p.ID, false))
					}
				}
			},
			want: pipeline.Skipped, summary: "Not replaced: the policies no longer choose this item (",
		},
		{
			name: "an original touched during the encode is skipped", answers: []answer{free},
			setup: func(t *testing.T, e env, _ store.Job) {
				later := time.Now().Add(time.Hour)
				must(t, os.Chtimes(e.alpha, later, later))
			},
			want: pipeline.Skipped, summary: summaryChanged,
		},
		{
			name: "cancelled just before the commit point is not committed", answers: []answer{free},
			setup: func(_ *testing.T, e env, j store.Job) {
				e.q.mu.Lock()
				e.q.cancelled[j.ID] = true
				e.q.mu.Unlock()
			},
			want: stopped, summary: context.Canceled.Error(),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := setup(t)
			j, pj := readyToReplace(t, e, tc.answers...)
			if tc.setup != nil {
				tc.setup(t, e, j)
			}
			_, err := e.q.beforeReplace(context.Background(), j, pj)
			var stop *pipeline.StopError
			switch tc.want {
			case "":
				must(t, err)
			case stopped:
				if err == nil || errors.As(err, &stop) || !strings.HasPrefix(err.Error(), tc.summary) {
					t.Fatalf("err %v, want %q", err, tc.summary)
				}
			default:
				if !errors.As(err, &stop) || stop.Outcome != tc.want || !strings.HasPrefix(stop.Summary, tc.summary) {
					t.Fatalf("err %v, want %s %q", err, tc.want, tc.summary)
				}
			}
			if tc.want != "" && e.q.Live()[j.ID].Committed {
				t.Fatal("a stopped job was committed")
			}
			if tc.check != nil {
				tc.check(t, e, j)
			}
		})
	}
}

func TestJobStartReadsWatchStateAgain(t *testing.T) {
	e := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); e.q.Stop(); e.lib.Wait() }()
	must(t, e.st.SetSetting(ctx, store.KeyDryRun, "false"))
	id := alphaID(t, e)
	jobID, _, err := e.q.Enqueue(ctx, id, "manual")
	must(t, err)
	// Marked a favourite after the last sync: the job must see it.
	e.jf.SetUserData(jellyfintest.FixtureUserID, id, jellyfintest.UserData{IsFavorite: true})
	e.q.Start(ctx)
	j := waitFor(t, e, jobID, store.JobComplete, store.JobFailed, store.JobSkipped)
	if j.Status != store.JobSkipped || !strings.HasPrefix(j.Summary, "Nothing to do after re-checking") {
		t.Fatalf("job %s: %s", j.Status, j.Summary)
	}
	assertUntouched(t, e)
}

// --- end to end: a real encode waits for a viewer --------------------------

// encodeThenWatch starts the queue on Alpha, and has alex start watching it
// while it encodes. It returns once the job waits for alex to finish.
func encodeThenWatch(t *testing.T, e env) (context.Context, int64) {
	t.Helper()
	e.q.playback = PlaybackWait{Poll: 20 * time.Millisecond}.withDefaults()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); e.q.Stop(); e.lib.Wait() })
	// A slow preset keeps the encode going while playback starts.
	must(t, e.st.SetSettings(ctx, map[string]string{store.KeyDryRun: "false", store.KeyX265Preset: "slower"}))
	e.q.Start(ctx)
	jobID, _, err := e.q.Enqueue(ctx, alphaID(t, e), "manual")
	must(t, err)
	waitFor(t, e, jobID, store.JobEncoding)
	e.jf.SetPlaying(fixtureAlpha, "alex", "Living Room TV", false)
	waitNote(t, e.q, "Waiting: alex is watching this on Living Room TV")
	return ctx, jobID
}

func assertNoLeftovers(t *testing.T, e env) {
	t.Helper()
	entries, _ := os.ReadDir(filepath.Dir(e.alpha))
	for _, en := range entries {
		if strings.Contains(en.Name(), ".jellytrim-") {
			t.Fatalf("left behind %s", en.Name())
		}
	}
}

func TestDryRunTurnedOnWhileWaiting(t *testing.T) {
	e := setup(t)
	ctx, jobID := encodeThenWatch(t, e)
	must(t, e.st.SetSetting(ctx, store.KeyDryRun, "true"))
	e.jf.StopPlaying(fixtureAlpha)

	deadline := time.Now().Add(2 * time.Minute)
	var j store.Job
	for time.Now().Before(deadline) {
		j, _ = e.st.Job(ctx, jobID)
		if j.Status == store.JobWaiting && j.StartedAt == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if j.Status != store.JobWaiting || j.Summary != summaryDryRun {
		t.Fatalf("job %s: %q", j.Status, j.Summary)
	}
	assertUntouched(t, e)
	assertNoLeftovers(t, e)
}

func TestWatchedWhileWaitingIsCheckedAgain(t *testing.T) {
	e := setup(t)
	_, jobID := encodeThenWatch(t, e)
	// alex likes it: the favourites policy now protects it.
	e.jf.SetUserData(jellyfintest.FixtureUserID, fixtureAlpha, jellyfintest.UserData{IsFavorite: true})
	e.jf.StopPlaying(fixtureAlpha)

	j := waitFor(t, e, jobID, store.JobComplete, store.JobFailed, store.JobSkipped, store.JobWaiting)
	if j.Status != store.JobSkipped || j.Summary != summaryWatchedNoLonger {
		t.Fatalf("job %s: %q\n%s", j.Status, j.Summary, j.Diagnostics)
	}
	if !strings.Contains(j.Diagnostics, "not replaced") {
		t.Fatalf("diagnostics: %s", j.Diagnostics)
	}
	assertUntouched(t, e)
	assertNoLeftovers(t, e)
}

func TestStartCheckHoldsBackWhenTheKeyIsRejected(t *testing.T) {
	q := &Service{}
	for _, tc := range []struct {
		name string
		a    answer
		want error
	}{
		{"a rejected key holds the job back", answer{err: &jellyfin.StatusError{Method: "GET", Path: "/Sessions",
			StatusCode: http.StatusUnauthorized}}, errKeyRejectedAtStart},
		{"no connection set up holds the job back", answer{err: library.ErrNotConfigured}, errKeyRejectedAtStart},
		{"Jellyfin down lets the job start", down, nil},
		{"nobody playing lets the job start", free, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q.playingFn = (&script{answers: []answer{tc.a}}).ask
			p, err := q.playingAtStart(context.Background(), "item")
			if p != nil || !errors.Is(err, tc.want) || (tc.want == nil) != (err == nil) {
				t.Fatalf("got %v %v, want %v", p, err, tc.want)
			}
		})
	}
}
