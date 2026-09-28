package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func bigJSON(n int) string {
	var b strings.Builder
	b.WriteString(`{"streams":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"index":%d,"codec_type":"subtitle","tags":{"language":"eng","title":"Subtitles %d"}}`, i, i)
	}
	b.WriteString(`]}`)
	return b.String()
}

func TestProbeJSONIsCompressedAndReadsBack(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	seed(t, s)
	cases := []struct {
		name, item, probe, frames string
		compressed                bool
	}{
		{"large JSON is compressed", "a", bigJSON(200), bigJSON(20), true},
		{"short JSON stays as text", "b", `{"streams":[]}`, `{"frames":[]}`, false},
		{"empty JSON stays empty", "e1", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := s.SaveProbe(ctx, Probe{ItemID: tc.item, LocalPath: "/m/" + tc.item, ProbeJSON: tc.probe, FrameJSON: tc.frames,
				VideoCodec: "h264", DurationMs: 7_200_000}); err != nil {
				t.Fatal(err)
			}
			var raw []byte
			var kind string
			if err := s.db.QueryRowContext(ctx, `SELECT probe_json, typeof(probe_json) FROM probes WHERE item_id = ?`, tc.item).Scan(&raw, &kind); err != nil {
				t.Fatal(err)
			}
			if got := isPacked(raw); got != tc.compressed || (kind == "blob") != tc.compressed {
				t.Fatalf("compressed %v (%s), want %v", got, kind, tc.compressed)
			}
			if tc.compressed && len(raw)*8 > len(tc.probe) {
				t.Fatalf("stored %d bytes for %d bytes of JSON", len(raw), len(tc.probe))
			}
			p, err := s.Probe(ctx, tc.item)
			if err != nil || p.ProbeJSON != tc.probe || p.FrameJSON != tc.frames {
				t.Fatalf("Probe read back %d/%d bytes (%v)", len(p.ProbeJSON), len(p.FrameJSON), err)
			}
			all, err := s.Probes(ctx)
			if err != nil || all[tc.item].ProbeJSON != tc.probe || all[tc.item].FrameJSON != tc.frames {
				t.Fatalf("Probes read back wrong JSON (%v)", err)
			}
		})
	}
}

func TestPrettyJSONIsStoredCompact(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	seed(t, s)
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, []byte(bigJSON(30)), "", "    "); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveProbe(ctx, Probe{ItemID: "a", LocalPath: "/m/a", ProbeJSON: pretty.String()}); err != nil {
		t.Fatal(err)
	}
	p, err := s.Probe(ctx, "a")
	if err != nil || p.ProbeJSON != bigJSON(30) {
		t.Fatalf("read back %q (%v)", p.ProbeJSON[:min(80, len(p.ProbeJSON))], err)
	}
}

// TestProbeDictionaryIsUnchanged guards the compression dictionary: stored
// probes cannot be read with a different one. Add a new version instead.
func TestProbeDictionaryIsUnchanged(t *testing.T) {
	sum := sha256.Sum256(probeDictV1)
	if got := hex.EncodeToString(sum[:]); got != "2abfe7ee9313b0ab05689488039f0307e2031455f73608c86cbfd7f116d1e818" {
		t.Fatalf("probedict_v1.txt changed (sha256 %s); stored probes would become unreadable", got)
	}
}

func TestUnpackRejectsUnknownOrDamagedData(t *testing.T) {
	packed, err := packJSON(bigJSON(20))
	if err != nil {
		t.Fatal(err)
	}
	good := packed.([]byte)
	otherDict := append([]byte{}, good...)
	binary.BigEndian.PutUint32(otherDict[2:6], 12345)
	damaged := append([]byte{}, good...)
	damaged[len(damaged)/2] ^= 0xff
	cases := []struct {
		name string
		in   []byte
	}{
		{"unknown dictionary", otherDict},
		{"damaged stream", damaged},
		{"truncated stream", good[:len(good)/2]},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if out, err := unpackJSON(tc.in); err == nil {
				t.Fatalf("no error, read %d bytes", len(out))
			}
		})
	}
	if out, err := unpackJSON(good); err != nil || out != bigJSON(20) {
		t.Fatalf("good data: %v", err)
	}
}

func TestLegacyTextProbesReadAndCompress(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	seed(t, s)
	probe, frames := bigJSON(100), bigJSON(10)
	// A row as an earlier JellyTrim wrote it: plain JSON text.
	if _, err := s.db.ExecContext(ctx, `INSERT INTO probes (item_id, local_path, dev, inode, size, mtime_ns, nlink,
		probe_json, frame_json, probed_at, video_codec) VALUES ('a', '/m/a', 1, 2, 3, 4, 1, ?, ?, 0, 'h264')`, probe, frames); err != nil {
		t.Fatal(err)
	}
	if p, err := s.Probe(ctx, "a"); err != nil || p.ProbeJSON != probe || p.FrameJSON != frames {
		t.Fatalf("legacy row read back wrong (%v)", err)
	}
	sums, err := s.ProbeSummaries(ctx)
	if err != nil || !sums["a"].HasJSON || sums["a"].VideoCodec != "h264" {
		t.Fatalf("summary of a legacy row %+v (%v)", sums["a"], err)
	}
	n, err := s.CompressLegacyProbes(ctx, 10)
	if err != nil || n != 1 {
		t.Fatalf("compressed %d rows (%v)", n, err)
	}
	if n, _ := s.CompressLegacyProbes(ctx, 10); n != 0 {
		t.Fatalf("compressed %d rows a second time", n)
	}
	var kind string
	if err := s.db.QueryRowContext(ctx, `SELECT typeof(probe_json) FROM probes WHERE item_id = 'a'`).Scan(&kind); err != nil || kind != "blob" {
		t.Fatalf("stored as %s (%v)", kind, err)
	}
	if p, err := s.Probe(ctx, "a"); err != nil || p.ProbeJSON != probe || p.FrameJSON != frames {
		t.Fatalf("compressed row read back wrong (%v)", err)
	}
}

func TestProbeSummaries(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	seed(t, s)
	id := FileIdentity{Dev: 1 << 63, Inode: 42, Size: 1000, MtimeNs: 7}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.SaveProbe(ctx, Probe{ItemID: "a", LocalPath: "/m/a.mkv", FileIdentity: id, Nlink: 2, IsSymlink: true,
		ProbeJSON: bigJSON(50), VideoCodec: "hevc", Width: 3840, Height: 2160, Resolution: 2160, HDR: "hdr10",
		VideoBitrate: 40_000_000, DurationMs: 7_200_000, Container: "matroska"}))
	must(s.SaveProbe(ctx, Probe{ItemID: "b", LocalPath: "/m/b.mkv", Error: "ffprobe failed"}))
	got, err := s.ProbeSummaries(ctx)
	must(err)
	a := got["a"]
	want := ProbeSummary{ItemID: "a", LocalPath: "/m/a.mkv", FileIdentity: id, Nlink: 2, IsSymlink: true, HasJSON: true,
		ProbedAt: a.ProbedAt, VideoCodec: "hevc", Width: 3840, Height: 2160, Resolution: 2160, HDR: "hdr10",
		VideoBitrate: 40_000_000, DurationMs: 7_200_000, Container: "matroska"}
	if a != want {
		t.Fatalf("summary\n got %+v\nwant %+v", a, want)
	}
	if b := got["b"]; b.HasJSON || b.Error != "ffprobe failed" {
		t.Fatalf("failed probe summary %+v", b)
	}
}

func i64(v int64) *int64 { return &v }

func TestCreateJobsRules(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	running, _, err := s.CreateJob(ctx, Job{ItemID: "running", ItemName: "R", Plan: "{}", LocalPath: "/m/running.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.StartJob(ctx, running, "", ""); err != nil || !ok {
		t.Fatal(err)
	}
	stuck, _, _ := s.CreateJob(ctx, Job{ItemID: "stuck", ItemName: "S", Plan: "{}", LocalPath: "/m/stuck.mkv"})
	if err := s.FinishJob(ctx, stuck, JobOutcome{Status: JobAttention}); err != nil {
		t.Fatal(err)
	}
	done, _, _ := s.CreateJob(ctx, Job{ItemID: "done", ItemName: "D", Plan: "{}", LocalPath: "/m/done.mkv"})
	if err := s.FinishJob(ctx, done, JobOutcome{Status: JobComplete}); err != nil {
		t.Fatal(err)
	}
	jobs := []Job{
		{ItemID: "running", LocalPath: "/m/running.mkv"},  // item already active
		{ItemID: "other", LocalPath: "/m/running.mkv"},    // same file as an active job
		{ItemID: "stuck", LocalPath: "/m/stuck-new.mkv"},  // item needs attention
		{ItemID: "stuck-file", LocalPath: "/m/stuck.mkv"}, // file needs attention
		{ItemID: "done", LocalPath: "/m/done.mkv"},        // finished jobs do not block
		{ItemID: "new", LocalPath: "/m/new.mkv"},          // created
		{ItemID: "new", LocalPath: "/m/new-again.mkv"},    // same item twice in the list
		{ItemID: "twin", LocalPath: "/m/new.mkv"},         // same file twice in the list
		{ItemID: "no-path"},                               // no local path: only the item counts
		{ItemID: "no-path-2"},                             // another item without a path
		{ItemID: "sized", LocalPath: "/m/sized.mkv", SourceSize: i64(1000), EstMax: i64(400), DurationMs: 5000},
	}
	for i := range jobs {
		jobs[i].ItemName, jobs[i].Plan = jobs[i].ItemID, "{}"
	}
	n, err := s.CreateJobs(ctx, jobs)
	if err != nil || n != 5 {
		t.Fatalf("created %d (%v), want 5", n, err)
	}
	active, _ := s.ActiveJobs(ctx)
	got := map[string]Job{}
	for _, j := range active {
		got[j.ItemID] = j
	}
	for _, id := range []string{"done", "new", "no-path", "no-path-2", "sized"} {
		if _, ok := got[id]; !ok {
			t.Errorf("no job for %s", id)
		}
	}
	for _, id := range []string{"other", "stuck-file", "twin"} {
		if _, ok := got[id]; ok {
			t.Errorf("a job was queued for %s", id)
		}
	}
	if j := got["sized"]; j.EstSaving != 600 || j.DurationMs != 5000 || j.Trigger != "auto" {
		t.Errorf("sized job saving %d, duration %d, trigger %q", j.EstSaving, j.DurationMs, j.Trigger)
	}
	// Running it again queues nothing new.
	if n, err := s.CreateJobs(ctx, jobs); err != nil || n != 0 {
		t.Fatalf("second run created %d (%v)", n, err)
	}
}

func TestCreateJobsAcrossBatches(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	var jobs []Job
	for i := 0; i < createBatch+10; i++ {
		jobs = append(jobs, Job{ItemID: fmt.Sprintf("i%d", i), ItemName: "x", Plan: "{}", LocalPath: fmt.Sprintf("/m/%d.mkv", i)})
	}
	// The last job shares a file with the first, in a different batch.
	jobs = append(jobs, Job{ItemID: "late", ItemName: "x", Plan: "{}", LocalPath: "/m/0.mkv"})
	n, err := s.CreateJobs(ctx, jobs)
	if err != nil || n != createBatch+10 {
		t.Fatalf("created %d (%v)", n, err)
	}
}

func TestWaitingOrder(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	add := func(item, trigger string, size, estMax int64) int64 {
		t.Helper()
		id, _, err := s.CreateJob(ctx, Job{ItemID: item, ItemName: item, Plan: "{}", Trigger: trigger, LocalPath: "/m/" + item,
			SourceSize: i64(size), EstMax: i64(estMax)})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	small := add("small", "auto", 1000, 900)      // saves 100
	big := add("big", "auto", 1000, 100)          // saves 900
	manualSmall := add("manual", "manual", 10, 9) // saves 1, but queued by hand
	unknown := add("unknown", "auto", 1000, 2000) // no saving
	mid := add("mid", "auto", 1000, 500)          // saves 500
	mid2 := add("mid-later", "auto", 1000, 500)   // same saving, newer
	running := add("running", "auto", 5000, 4000) // started below
	if ok, err := s.StartJob(ctx, running, "", ""); err != nil || !ok {
		t.Fatal(err)
	}
	want := []int64{manualSmall, big, mid, mid2, small, unknown}

	next, err := s.NextWaitingJobs(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := jobIDs(next); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("waiting order %v, want %v", got, want)
	}
	if j, err := s.NextWaitingJob(ctx); err != nil || j.ID != manualSmall {
		t.Fatalf("next %d (%v)", j.ID, err)
	}
	active, _ := s.ActiveJobs(ctx)
	if got := jobIDs(active); fmt.Sprint(got) != fmt.Sprint(append([]int64{running}, want...)) {
		t.Fatalf("active order %v", got)
	}
}

func jobIDs(jobs []Job) []int64 {
	var out []int64
	for _, j := range jobs {
		out = append(out, j.ID)
	}
	return out
}

// TestQueueQueriesUseIndexes guards the query plans that keep queueing fast
// with 100,000 jobs: no full scan of jobs and no sort for the next job.
func TestQueueQueriesUseIndexes(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	cases := []struct {
		name, query string
		args        []any
		want        []string
		never       []string
	}{
		{"blocking job by item and path", `SELECT MIN(id) FROM (
			SELECT id FROM jobs WHERE item_id = ? AND status IN ('waiting', 'analysing', 'encoding', 'validating', 'replacing', 'attention')
			UNION ALL
			SELECT id FROM jobs WHERE local_path = ? AND local_path != '' AND status IN ('waiting', 'analysing', 'encoding', 'validating', 'replacing', 'attention'))`,
			[]any{"a", "/m/a"}, []string{"jobs_item", "jobs_local_path"}, []string{"SCAN jobs"}},
		{"next waiting jobs", `SELECT id FROM jobs WHERE status = 'waiting' ORDER BY ` + waitingOrder + ` LIMIT 5`,
			nil, []string{"jobs_waiting_order"}, []string{"TEMP B-TREE"}},
		{"recent jobs", `SELECT item_id FROM jobs WHERE created_at >= ?`, []any{0}, []string{"jobs_created"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := explain(t, s, tc.query, tc.args...)
			for _, w := range tc.want {
				if !strings.Contains(plan, w) {
					t.Errorf("plan does not use %s:\n%s", w, plan)
				}
			}
			for _, n := range tc.never {
				if strings.Contains(plan, n) {
					t.Errorf("plan has %s:\n%s", n, plan)
				}
			}
		})
	}
	_ = ctx
}

func explain(t *testing.T, s *Store, q string, args ...any) string {
	t.Helper()
	rows, err := s.db.Query(`EXPLAIN QUERY PLAN `+q, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var b strings.Builder
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		b.WriteString(detail + "\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestMigration0005OnExistingDatabase(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	old := openAtVersion(t, dir, 4)
	probe := bigJSON(40)
	for _, q := range []string{
		`INSERT INTO libraries (id, name, updated_at) VALUES ('lm', 'Movies', 0)`,
		`INSERT INTO items (id, library_id, type, name, jellyfin_path, seen_sync_id, updated_at) VALUES ('a', 'lm', 'Movie', 'A', '/a.mkv', 1, 0)`,
		`INSERT INTO probes (item_id, local_path, dev, inode, size, mtime_ns, nlink, probe_json, probed_at, duration_ms) VALUES ('a', '/a.mkv', 1, 2, 3, 4, 1, '` + probe + `', 0, 60000)`,
		`INSERT INTO jobs (id, item_id, item_name, status, plan, local_path, jellyfin_path, source_size, est_max_bytes, created_at) VALUES (1, 'a', 'A', 'waiting', '{}', '/a.mkv', '', 1000, 300, 0)`,
		`INSERT INTO jobs (id, item_id, item_name, status, plan, local_path, jellyfin_path, source_size, est_max_bytes, created_at) VALUES (2, 'b', 'B', 'complete', '{}', '/b.mkv', '', 1000, 1200, 0)`,
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
	j1, _ := s.Job(ctx, 1)
	j2, _ := s.Job(ctx, 2)
	if j1.EstSaving != 700 || j1.DurationMs != 60000 || j2.EstSaving != 0 {
		t.Fatalf("backfill: job 1 saving %d duration %d, job 2 saving %d", j1.EstSaving, j1.DurationMs, j2.EstSaving)
	}
	if p, err := s.Probe(ctx, "a"); err != nil || p.ProbeJSON != probe {
		t.Fatalf("legacy probe after upgrade (%v)", err)
	}
}
