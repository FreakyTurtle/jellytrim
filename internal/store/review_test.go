package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNewerSchemaIsRefused(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO schema_migrations (version, name, applied_at) VALUES (9999, '9999_future.sql', 0)`); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	_, err = Open(ctx, dir)
	if !errors.Is(err, ErrNewerSchema) || !strings.Contains(err.Error(), "9999") {
		t.Fatalf("opening a newer database: %v", err)
	}
}

func TestReplaceWithEmptyListKeepsRows(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	// A new install with nothing stored accepts an empty answer.
	if err := s.ReplaceLibraries(ctx, nil); err != nil {
		t.Fatalf("empty libraries on an empty store: %v", err)
	}
	if err := s.ReplaceUsers(ctx, nil); err != nil {
		t.Fatalf("empty users on an empty store: %v", err)
	}
	seed(t, s)
	if err := s.ReplaceLibraries(ctx, nil); !errors.Is(err, ErrNoLibraries) {
		t.Fatalf("empty libraries: %v", err)
	}
	if err := s.ReplaceUsers(ctx, []JellyfinUser{}); !errors.Is(err, ErrNoUsers) {
		t.Fatalf("empty users: %v", err)
	}
	if items, _ := s.ManagedItems(ctx); len(items) != 3 {
		t.Fatalf("an empty library list removed items: %d left", len(items))
	}
	if us, _ := s.Users(ctx); len(us) != 2 {
		t.Fatalf("an empty user list removed users: %d left", len(us))
	}
	// A shorter, non-empty list removes only the libraries missing from it.
	if err := s.ReplaceLibraries(ctx, []Library{{ID: "lm", Name: "Movies"}}); err != nil {
		t.Fatal(err)
	}
	libs, _ := s.Libraries(ctx)
	if len(libs) != 1 || libs[0].ID != "lm" || !libs[0].Managed {
		t.Fatalf("libraries %+v", libs)
	}
}

func TestCollectionsKeepSeriesAndSeasonMembers(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	seed(t, s)
	if err := s.ReplaceCollections(ctx, []Collection{
		{ID: "c1", Name: "Box", ItemIDs: []string{"s1", "season1", "a", "not-synced-yet"}},
	}); err != nil {
		t.Fatal(err)
	}
	cols, err := s.ItemCollections(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"s1", "season1", "a", "not-synced-yet"} {
		if len(cols[id]) != 1 || cols[id][0] != "c1" {
			t.Errorf("member %s: %v", id, cols[id])
		}
	}
}

func TestSeasonIDRoundTrips(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	id := seed(t, s)
	if err := s.UpsertItems(ctx, id, []Item{{ID: "e2", LibraryID: "lt", Type: "Episode", Name: "Two", SeriesID: "s1",
		SeasonID: "season1", JellyfinPath: "/media/tv/e2.mkv"}}); err != nil {
		t.Fatal(err)
	}
	it, err := s.Item(ctx, "e2")
	if err != nil || it.SeasonID != "season1" {
		t.Fatalf("item %+v (%v)", it, err)
	}
}

func TestLastCompleteSync(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if _, err := s.LastCompleteSync(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no syncs: %v", err)
	}
	first := seed(t, s)
	if _, err := s.FinishSync(ctx, first, true, 3, ""); err != nil {
		t.Fatal(err)
	}
	second, _ := s.StartSync(ctx)
	if _, err := s.FinishSync(ctx, second, false, 0, "Jellyfin is down"); err != nil {
		t.Fatal(err)
	}
	last, _ := s.LastSync(ctx)
	complete, err := s.LastCompleteSync(ctx)
	if err != nil || last.ID != second || complete.ID != first || complete.Status != "complete" {
		t.Fatalf("last %+v, complete %+v (%v)", last, complete, err)
	}
}

func TestProbeAndOptimisedIdentities(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	seed(t, s)
	id := FileIdentity{Dev: 1 << 63, Inode: 42, Size: 1000, MtimeNs: 7}
	if err := s.SaveProbe(ctx, Probe{ItemID: "a", LocalPath: "/mnt/movies/a.mkv", FileIdentity: id, Nlink: 2,
		IsSymlink: true, ProbeJSON: `{"big":"json"}`, Error: ""}); err != nil {
		t.Fatal(err)
	}
	ps, err := s.ProbeIdentities(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p := ps["a"]
	if p.FileIdentity != id || p.Nlink != 2 || !p.IsSymlink || p.LocalPath != "/mnt/movies/a.mkv" {
		t.Fatalf("probe identity %+v", p)
	}
	if err := s.RecordOptimised(ctx, id, "a", 1); err != nil {
		t.Fatal(err)
	}
	opt, err := s.OptimisedIdentities(ctx)
	if err != nil || !opt[id] || len(opt) != 1 {
		t.Fatalf("optimised %v (%v)", opt, err)
	}
}

func TestLibraryListIgnoresDisabledUsersAndEmptyProbeSize(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	seed(t, s)
	// u2 is selected but disabled: its watched and favourite flags must not show.
	if err := s.ReplaceUserData(ctx, "u2", []UserData{{ItemID: "a", Played: true, Favourite: true}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE items SET jellyfin_size = 5000 WHERE id = 'a'`); err != nil {
		t.Fatal(err)
	}
	// A failed probe stores size 0; the list falls back to Jellyfin's size.
	if err := s.SaveProbe(ctx, Probe{ItemID: "a", LocalPath: "/mnt/movies/a.mkv", Error: "missing"}); err != nil {
		t.Fatal(err)
	}
	rows, _, err := s.LibraryList(ctx, LibraryFilter{Search: "Alpha"})
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows %+v (%v)", rows, err)
	}
	if rows[0].Watched || rows[0].Favourite {
		t.Fatalf("disabled user's state counted: %+v", rows[0])
	}
	if rows[0].Size != 5000 {
		t.Fatalf("size %d, want Jellyfin's 5000", rows[0].Size)
	}
	if rows, _, _ := s.LibraryList(ctx, LibraryFilter{Watched: "yes"}); len(rows) != 0 {
		t.Fatalf("watched filter counted a disabled user: %d rows", len(rows))
	}
}

func TestCreateJobBlocksSameFileAndAttention(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	first, created, err := s.CreateJob(ctx, Job{ItemID: "a", ItemName: "A", Plan: "{}", LocalPath: "/m/shared.mkv"})
	if err != nil || !created {
		t.Fatalf("first job: %v %v", created, err)
	}
	// Another item with the same file is refused while the first is active.
	id, created, err := s.CreateJob(ctx, Job{ItemID: "b", ItemName: "B", Plan: "{}", LocalPath: "/m/shared.mkv"})
	if err != nil || created || id != first {
		t.Fatalf("same file: id %d created %v (%v)", id, created, err)
	}
	// A job that needs attention also blocks new jobs for its item.
	if err := s.FinishJob(ctx, first, JobOutcome{Status: JobAttention, Summary: "check", OutputIdentity: `{"dev":1}`}); err != nil {
		t.Fatal(err)
	}
	if _, created, _ := s.CreateJob(ctx, Job{ItemID: "a", ItemName: "A", Plan: "{}", LocalPath: "/m/other.mkv"}); created {
		t.Fatal("a job was queued for an item whose job needs attention")
	}
	att, err := s.JobsNeedingAttention(ctx)
	if err != nil || len(att) != 1 || att[0].OutputIdentity != `{"dev":1}` {
		t.Fatalf("attention %+v (%v)", att, err)
	}
	interrupted, _ := s.InterruptedJobs(ctx)
	if len(interrupted) != 1 || interrupted[0].ID != first {
		t.Fatalf("interrupted jobs must include attention: %+v", interrupted)
	}
	if n, _ := s.ActiveJobCount(ctx); n != 0 {
		t.Fatalf("attention counted as active: %d", n)
	}
	// Once resolved, both items can be queued again.
	if err := s.FinishJob(ctx, first, JobOutcome{Status: JobFailed}); err != nil {
		t.Fatal(err)
	}
	j, _ := s.Job(ctx, first)
	if j.OutputIdentity != `{"dev":1}` {
		t.Fatalf("an empty outcome identity cleared the stored one: %q", j.OutputIdentity)
	}
	if _, created, err := s.CreateJob(ctx, Job{ItemID: "b", ItemName: "B", Plan: "{}", LocalPath: "/m/shared.mkv"}); err != nil || !created {
		t.Fatalf("after resolving: %v %v", created, err)
	}
}

func TestJobPathsAndOutputIdentity(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	id, _, err := s.CreateJob(ctx, Job{ItemID: "a", ItemName: "A", Plan: "{}", LocalPath: "/old.mkv", JellyfinPath: "/jf/old.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateJobPaths(ctx, id, "/new.mkv", "/jf/new.mkv"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetJobOutputIdentity(ctx, id, `{"inode":9}`); err != nil {
		t.Fatal(err)
	}
	j, ok, err := s.ActiveJobForItem(ctx, "a")
	if err != nil || !ok || j.ID != id || j.LocalPath != "/new.mkv" || j.JellyfinPath != "/jf/new.mkv" || j.OutputIdentity != `{"inode":9}` {
		t.Fatalf("job %+v ok %v (%v)", j, ok, err)
	}
	if _, ok, err := s.ActiveJobForItem(ctx, "nothing"); ok || err != nil {
		t.Fatalf("no job: %v %v", ok, err)
	}
}

func TestActiveJobUniqueIndex(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if _, _, err := s.CreateJob(ctx, Job{ItemID: "a", ItemName: "A", Plan: "{}"}); err != nil {
		t.Fatal(err)
	}
	// A direct insert that bypasses CreateJob's check still cannot add a
	// second active job for the item.
	_, err := s.db.ExecContext(ctx, `INSERT INTO jobs (item_id, item_name, status, plan, local_path, jellyfin_path, created_at)
		VALUES ('a', 'A', 'waiting', '{}', '', '', 0)`)
	if !isUniqueViolation(err) {
		t.Fatalf("second active job: %v", err)
	}
}

// TestConcurrentWriters queues the same item from many goroutines while
// others write progress, as the queue and the web handlers do. Exactly one
// job must be created and no writer may see SQLITE_BUSY.
func TestConcurrentWriters(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	other, _, err := s.CreateJob(ctx, Job{ItemID: "running", ItemName: "R", Plan: "{}"})
	if err != nil {
		t.Fatal(err)
	}
	const writers = 24
	var wg sync.WaitGroup
	errs := make(chan error, writers*2)
	ids := make(chan int64, writers)
	start := make(chan struct{})
	for i := range writers {
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			id, created, err := s.CreateJob(ctx, Job{ItemID: "same", ItemName: "Same", Plan: "{}", LocalPath: fmt.Sprintf("/m/%d.mkv", i)})
			if err != nil {
				errs <- fmt.Errorf("create: %w", err)
				return
			}
			if created {
				ids <- id
			}
			// Each writer also queues its own items: read-then-write
			// transactions that overlap with every other writer.
			for k := range 5 {
				if _, _, err := s.CreateJob(ctx, Job{ItemID: fmt.Sprintf("own-%d-%d", i, k), ItemName: "Own", Plan: "{}"}); err != nil {
					errs <- fmt.Errorf("create own: %w", err)
					return
				}
			}
		}()
		go func() {
			defer wg.Done()
			<-start
			for k := range 5 {
				eta := int64(k)
				if err := s.SetJobProgress(ctx, other, float64(k)/5, 1, &eta); err != nil {
					errs <- fmt.Errorf("progress: %w", err)
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	close(ids)
	for err := range errs {
		t.Error(err)
	}
	if n := len(ids); n != 1 {
		t.Fatalf("%d jobs created for one item, want 1", n)
	}
	var active int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE item_id = 'same' AND status = 'waiting'`).Scan(&active); err != nil || active != 1 {
		t.Fatalf("active jobs for the item: %d (%v)", active, err)
	}
}

// TestMigration0004OnExistingDatabase upgrades a version 3 database that
// holds duplicate active jobs and collection members.
func TestMigration0004OnExistingDatabase(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	old := openAtVersion(t, dir, 3)
	for _, q := range []string{
		`INSERT INTO libraries (id, name, updated_at) VALUES ('lm', 'Movies', 0)`,
		`INSERT INTO items (id, library_id, type, name, jellyfin_path, seen_sync_id, updated_at) VALUES ('a', 'lm', 'Movie', 'A', '/a.mkv', 1, 0)`,
		`INSERT INTO collections (id, name) VALUES ('c1', 'Box')`,
		`INSERT INTO collection_items (collection_id, item_id) VALUES ('c1', 'a')`,
		`INSERT INTO jobs (id, item_id, item_name, status, plan, local_path, jellyfin_path, created_at) VALUES (1, 'a', 'A', 'waiting', '{}', '/a.mkv', '', 0)`,
		`INSERT INTO jobs (id, item_id, item_name, status, plan, local_path, jellyfin_path, created_at) VALUES (2, 'a', 'A', 'encoding', '{}', '/a.mkv', '', 0)`,
		`INSERT INTO jobs (id, item_id, item_name, status, plan, local_path, jellyfin_path, created_at) VALUES (3, 'a', 'A', 'waiting', '{}', '/a.mkv', '', 0)`,
		`INSERT INTO jobs (id, item_id, item_name, status, plan, local_path, jellyfin_path, created_at) VALUES (4, 'b', 'B', 'waiting', '{}', '/b.mkv', '', 0)`,
		`INSERT INTO jobs (id, item_id, item_name, status, plan, local_path, jellyfin_path, created_at) VALUES (5, 'b', 'B', 'waiting', '{}', '/b.mkv', '', 0)`,
	} {
		if _, err := old.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	_ = old.Close()

	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("upgrading: %v", err)
	}
	defer func() { _ = s.Close() }()
	want := map[int64]string{1: JobCancelled, 2: JobEncoding, 3: JobCancelled, 4: JobWaiting, 5: JobCancelled}
	for id, status := range want {
		j, err := s.Job(ctx, id)
		if err != nil || j.Status != status {
			t.Errorf("job %d: %q (%v), want %q", id, j.Status, err, status)
		}
	}
	cols, err := s.ItemCollections(ctx)
	if err != nil || len(cols["a"]) != 1 {
		t.Fatalf("collection members lost: %v (%v)", cols, err)
	}
	// Members that are not items (a series) can now be stored.
	if err := s.ReplaceCollections(ctx, []Collection{{ID: "c1", Name: "Box", ItemIDs: []string{"series-1"}}}); err != nil {
		t.Fatal(err)
	}
	var fk int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&fk); err != nil || fk != 0 {
		t.Fatalf("foreign key check: %d problems (%v)", fk, err)
	}
}

// openAtVersion creates a database with migrations up to version applied,
// as an older JellyTrim would have left it.
func openAtVersion(t *testing.T, dir string, version int) *sql.DB {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(dir, FileName)
	if err := ensureFile(path); err != nil {
		t.Fatal(err)
	}
	q := url.Values{}
	q.Add("_pragma", "foreign_keys(1)")
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	s := &Store{db: db, now: func() time.Time { return time.Unix(0, 0) }}
	if _, err := db.ExecContext(ctx, `CREATE TABLE schema_migrations (
		version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ms {
		if m.version > version {
			break
		}
		if err := s.apply(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestRecentJobsIgnoreJobsSkippedBeforeEncoding(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	seed(t, s)
	mk := func(item, status, diag string) {
		t.Helper()
		id, _, err := s.CreateJob(ctx, Job{ItemID: item, ItemName: item, Plan: "{}", LocalPath: "/mnt/" + item})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.FinishJob(ctx, id, JobOutcome{Status: status, Diagnostics: diag}); err != nil {
			t.Fatal(err)
		}
	}
	mk("a", JobSkipped, `{"step":"analysing"}`)
	mk("b", JobSkipped, `{"step":"validating"}`)
	mk("e1", JobFailed, `{"step":"encoding"}`)
	got, err := s.ItemsWithRecentJobs(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got["a"] || !got["b"] || !got["e1"] {
		t.Fatalf("recent %v", got)
	}
}
