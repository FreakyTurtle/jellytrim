package pipeline

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
)

// sentence is an error written as a sentence for people, like the queue's.
type sentence string

func (s sentence) Error() string { return string(s) }

// errStillWatching stands in for the queue's give-up error.
var errStillWatching error = sentence("Someone was still watching this after 6 hours, so JellyTrim will try again later.")

func TestSafetyBeforeReplace(t *testing.T) {
	cases := []struct {
		name string
		// wait is the hook; r is the rig, so a case can change the file.
		wait        func(ctx context.Context, r *rig) (string, error)
		cancelFirst bool
		want        Outcome
		summary     string
	}{
		{
			name:    "free after waiting replaces the file",
			wait:    func(context.Context, *rig) (string, error) { return "", nil },
			want:    Complete,
			summary: "Optimised.",
		},
		{
			name: "still playing at the limit discards the new file",
			wait: func(context.Context, *rig) (string, error) { return "", errStillWatching },
			want: Interrupted,
			summary: "Someone was still watching this after 6 hours, so JellyTrim will try again later. " +
				"The original is unchanged.",
		},
		{
			name: "cancelled while waiting stops",
			wait: func(ctx context.Context, _ *rig) (string, error) {
				<-ctx.Done()
				return "", ctx.Err()
			},
			cancelFirst: true,
			want:        Interrupted,
			summary:     "Stopped before finishing. The original is unchanged.",
		},
		{
			name: "the queue ends the job as skipped without replacing",
			wait: func(context.Context, *rig) (string, error) {
				return "", &StopError{Outcome: Skipped, Summary: "Not replaced: the policies no longer choose it."}
			},
			want:    Skipped,
			summary: "Not replaced: the policies no longer choose it.",
		},
		{
			name: "the queue sends the job back without replacing",
			wait: func(context.Context, *rig) (string, error) {
				return "", &StopError{Outcome: Interrupted, Summary: "Not replaced: Dry Run was turned on."}
			},
			want:    Interrupted,
			summary: "Not replaced: Dry Run was turned on.",
		},
		{
			name: "a new file changed during the wait is not used",
			wait: func(_ context.Context, r *rig) (string, error) {
				f, err := os.OpenFile(PartialPath(r.path, r.job.ID), os.O_WRONLY|os.O_APPEND, 0)
				if err != nil {
					return "", err
				}
				_, err = f.WriteString("appended after validation")
				return "", errors.Join(err, f.Close())
			},
			want:    Failed,
			summary: "The new file changed after JellyTrim checked it",
		},
		{
			name: "a new file swapped for another during the wait is not used",
			wait: func(_ context.Context, r *rig) (string, error) {
				partial := PartialPath(r.path, r.job.ID)
				b, err := os.ReadFile(partial)
				if err != nil {
					return "", err
				}
				// Same bytes and size, but a different file (new inode).
				if err := os.Remove(partial); err != nil {
					return "", err
				}
				return "", os.WriteFile(partial, b, 0o644)
			},
			want:    Failed,
			summary: "The new file changed after JellyTrim checked it",
		},
		{
			name: "a file changed during the wait is caught by the re-check",
			wait: func(_ context.Context, r *rig) (string, error) {
				changed := []byte("a newer download")
				if err := os.WriteFile(r.path, changed, 0o644); err != nil {
					return "", err
				}
				r.hash = sha256.Sum256(changed)
				return "", nil
			},
			want:    Skipped,
			summary: "The original changed while JellyTrim was working on it",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var calledWith []string
			r.job.BeforeReplace = func(ctx context.Context) (string, error) {
				// The new file is validated and nothing is backed up yet.
				if _, err := os.Stat(PartialPath(r.path, r.job.ID)); err != nil {
					t.Errorf("partial missing while waiting: %v", err)
				}
				if _, err := os.Stat(BackupPath(r.path, r.job.ID)); err == nil {
					t.Error("backup made before the wait")
				}
				if tc.cancelFirst {
					cancel()
				}
				return tc.wait(ctx, r)
			}
			h := Hooks{Note: func(n string) { calledWith = append(calledWith, n) }}
			res := r.pipeline().Execute(ctx, r.job, h)
			if res.Outcome != tc.want || !strings.HasPrefix(res.Summary, tc.summary) {
				t.Fatalf("got %s %q, want %s %q", res.Outcome, res.Summary, tc.want, tc.summary)
			}
			if !slices.Equal(calledWith, []string{""}) {
				t.Errorf("notes %q: the wait's note must be cleared once", calledWith)
			}
			if tc.want == Complete {
				return
			}
			r.assertOriginalIntact(res)
		})
	}
}

// TestSafetyPartialNotRemovedFailsTheJob covers a give-up or discard whose
// partial file cannot be deleted: the job fails, naming the file, instead of
// going back to the queue and forgetting it.
func TestSafetyPartialNotRemovedFailsTheJob(t *testing.T) {
	for _, stop := range []error{
		errStillWatching,
		&StopError{Outcome: Skipped, Summary: "Not replaced: the policies no longer choose it."},
	} {
		t.Run(stop.Error(), func(t *testing.T) {
			r := newRig(t)
			r.fs.op = "remove"
			r.job.BeforeReplace = func(context.Context) (string, error) { return "", stop }
			res := r.pipeline().Execute(context.Background(), r.job, Hooks{})
			partial := PartialPath(r.path, r.job.ID)
			want := "The new file was not used, but JellyTrim could not delete it. Delete " + partial +
				" yourself. The original is unchanged."
			if res.Outcome != Failed || res.Summary != want {
				t.Fatalf("got %s %q", res.Outcome, res.Summary)
			}
			if _, err := os.Stat(partial); err != nil {
				t.Fatalf("the partial should still be there for the user to delete: %v", err)
			}
			if _, err := os.Stat(BackupPath(r.path, r.job.ID)); !os.IsNotExist(err) {
				t.Fatal("nothing may be backed up after the hook said stop")
			}
			b, _ := os.ReadFile(r.path)
			if sha256.Sum256(b) != r.hash {
				t.Fatal("original changed")
			}
		})
	}
}

func TestSafetyWaitWarningIsKept(t *testing.T) {
	r := newRig(t)
	warning := "Jellyfin could not be asked whether the file was playing; it was replaced anyway."
	r.job.BeforeReplace = func(context.Context) (string, error) { return warning, nil }
	res := r.pipeline().Execute(context.Background(), r.job, Hooks{})
	if res.Outcome != Complete {
		t.Fatalf("%s: %s", res.Outcome, res.Summary)
	}
	if !slices.Contains(res.Diagnostics.Warnings, warning) {
		t.Fatalf("warnings %q", res.Diagnostics.Warnings)
	}
}

func TestSafetyNoWaitHookReplacesAsBefore(t *testing.T) {
	r := newRig(t)
	notes := 0
	res := r.pipeline().Execute(context.Background(), r.job, Hooks{Note: func(string) { notes++ }})
	if res.Outcome != Complete || notes != 0 {
		t.Fatalf("%s (%s), %d notes", res.Outcome, res.Summary, notes)
	}
}
