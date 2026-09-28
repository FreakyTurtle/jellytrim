package store

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

// outcomes returns every stored evaluation's outcome keyed by item ID.
func outcomes(t *testing.T, s *Store) map[string]string {
	t.Helper()
	rows, err := s.db.QueryContext(context.Background(), `SELECT item_id, outcome FROM evaluations`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var id, o string
		if err := rows.Scan(&id, &o); err != nil {
			t.Fatal(err)
		}
		out[id] = o
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func evs(outcome string, ids ...string) []Evaluation {
	out := make([]Evaluation, len(ids))
	for i, id := range ids {
		out[i] = Evaluation{ItemID: id, Outcome: outcome, Summary: outcome + " " + id}
	}
	return out
}

// run is one step of an evaluation-run scenario: a run that writes batches
// and then finishes, or stops before finishing.
type run struct {
	batches  [][]Evaluation
	finished bool
}

func TestEvaluationRuns(t *testing.T) {
	cases := []struct {
		name string
		runs []run
		want map[string]string
	}{
		{"a finished run keeps what it wrote",
			[]run{{[][]Evaluation{evs("optimise", "a", "b"), evs("skipped", "e1")}, true}},
			map[string]string{"a": "optimise", "b": "optimise", "e1": "skipped"}},
		{"a finished run deletes rows it did not write",
			[]run{{[][]Evaluation{evs("optimise", "a", "b", "e1")}, true}, {[][]Evaluation{evs("optimal", "a")}, true}},
			map[string]string{"a": "optimal"}},
		{"a run that stops keeps the previous rows it did not reach",
			[]run{{[][]Evaluation{evs("optimise", "a", "b", "e1")}, true}, {[][]Evaluation{evs("optimal", "a")}, false}},
			map[string]string{"a": "optimal", "b": "optimise", "e1": "optimise"}},
		{"the next finished run cleans up after a run that stopped",
			[]run{
				{[][]Evaluation{evs("optimise", "a", "b")}, true},
				{[][]Evaluation{evs("optimal", "a", "e1")}, false},
				{[][]Evaluation{evs("skipped", "a")}, true},
			},
			map[string]string{"a": "skipped"}},
		{"an evaluation for an item removed since is dropped",
			[]run{{[][]Evaluation{evs("optimise", "a", "gone")}, true}},
			map[string]string{"a": "optimise"}},
		{"a finished run with nothing written clears every row",
			[]run{{[][]Evaluation{evs("optimise", "a")}, true}, {nil, true}},
			map[string]string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := openTest(t)
			ctx := context.Background()
			seed(t, s)
			for _, r := range tc.runs {
				id, err := s.BeginEvaluations(ctx)
				if err != nil {
					t.Fatal(err)
				}
				for _, b := range r.batches {
					if err := s.WriteEvaluations(ctx, id, b); err != nil {
						t.Fatal(err)
					}
				}
				if r.finished {
					if _, err := s.FinishEvaluations(ctx, id); err != nil {
						t.Fatal(err)
					}
				}
			}
			if got := outcomes(t, s); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFinishingAnOlderRunKeepsANewerRunsRows(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	seed(t, s)
	older, err := s.BeginEvaluations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	newer, err := s.BeginEvaluations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteEvaluations(ctx, older, evs("optimise", "a")); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteEvaluations(ctx, newer, evs("optimal", "b")); err != nil {
		t.Fatal(err)
	}
	if n, err := s.FinishEvaluations(ctx, older); err != nil || n != 0 {
		t.Fatalf("deleted %d (%v)", n, err)
	}
	if got := outcomes(t, s); !reflect.DeepEqual(got, map[string]string{"a": "optimise", "b": "optimal"}) {
		t.Fatalf("got %v", got)
	}
}

func TestReplaceEvaluationsIsACompleteRun(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	seed(t, s)
	if err := s.ReplaceEvaluations(ctx, evs("optimise", "a", "b", "e1")); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceEvaluations(ctx, evs("skipped", "b")); err != nil {
		t.Fatal(err)
	}
	if got := outcomes(t, s); !reflect.DeepEqual(got, map[string]string{"b": "skipped"}) {
		t.Fatalf("got %v", got)
	}
	// Finished runs are forgotten; only the latest is kept.
	var runs int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM evaluation_runs`).Scan(&runs); err != nil || runs != 1 {
		t.Fatalf("%d runs kept (%v)", runs, err)
	}
}

// Rows written before migration 0006 belong to run 0 and are replaced by
// the first run that finishes.
func TestEvaluationsFromBeforeRunsAreReplaced(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	db := openAtVersion(t, dir, 5)
	if _, err := db.ExecContext(ctx, `INSERT INTO libraries (id, name, managed, updated_at) VALUES ('l', 'Movies', 1, 0);
		INSERT INTO items (id, library_id, type, name, jellyfin_path, seen_sync_id, updated_at)
		VALUES ('a', 'l', 'Movie', 'A', '/m/a.mkv', 1, 0), ('b', 'l', 'Movie', 'B', '/m/b.mkv', 1, 0);
		INSERT INTO evaluations (item_id, outcome, evaluated_at) VALUES ('a', 'optimise', 0), ('b', 'optimise', 0);`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if got := outcomes(t, s); len(got) != 2 {
		t.Fatalf("migration changed the evaluations: %v", got)
	}
	id, err := s.BeginEvaluations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteEvaluations(ctx, id, evs("optimal", "a")); err != nil {
		t.Fatal(err)
	}
	if n, err := s.FinishEvaluations(ctx, id); err != nil || n != 1 {
		t.Fatalf("deleted %d (%v)", n, err)
	}
	if got := outcomes(t, s); !reflect.DeepEqual(got, map[string]string{"a": "optimal"}) {
		t.Fatalf("got %v", got)
	}
}

// seedItems adds n managed movies with IDs item-0000 onwards and returns
// their IDs in order.
func seedItems(t *testing.T, s *Store, n int) []string {
	t.Helper()
	ctx := context.Background()
	seed(t, s)
	items := make([]Item, n)
	ids := make([]string, n)
	for i := range items {
		ids[i] = fmt.Sprintf("item-%04d", i)
		items[i] = Item{ID: ids[i], LibraryID: "lm", Type: "Movie", Name: ids[i], JellyfinPath: "/media/movies/" + ids[i] + ".mkv"}
	}
	if err := s.UpsertItems(ctx, 1, items); err != nil {
		t.Fatal(err)
	}
	return ids
}

// While a run replaces every item's evaluation batch by batch, readers
// always see one evaluation per item: none goes missing part-way.
func TestReadersSeeEveryEvaluationDuringARun(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	ids := seedItems(t, s, 400)
	want := len(ids) + 3 // and the three items seed adds
	all := append([]string{"a", "b", "e1"}, ids...)
	if err := s.ReplaceEvaluations(ctx, evs("optimise", all...)); err != nil {
		t.Fatal(err)
	}
	var done atomic.Bool
	var wg sync.WaitGroup
	var reads atomic.Int64
	errs := make(chan error, 3)
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !done.Load() {
				tot, err := s.Totals(ctx)
				if err == nil && tot.ByOutcome["pending"] != 0 {
					err = fmt.Errorf("%d items without an evaluation mid-run", tot.ByOutcome["pending"])
				}
				if err != nil {
					errs <- err
					return
				}
				reads.Add(1)
			}
		}()
	}
	for pass := range 3 {
		id, err := s.BeginEvaluations(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for start := 0; start < len(all); start += 50 {
			batch := evs(fmt.Sprintf("pass-%d", pass), all[start:min(start+50, len(all))]...)
			if err := s.WriteEvaluations(ctx, id, batch); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.FinishEvaluations(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	done.Store(true)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	got := outcomes(t, s)
	if len(got) != want || reads.Load() == 0 {
		t.Fatalf("%d evaluations after the runs, want %d; %d reads", len(got), want, reads.Load())
	}
	for id, o := range got {
		if o != "pass-2" {
			t.Fatalf("item %s kept %q", id, o)
		}
	}
}
