package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/freakyturtle/jellytrim/internal/fileid"
	"github.com/freakyturtle/jellytrim/internal/pipeline"
	"github.com/freakyturtle/jellytrim/internal/queue"
	"github.com/freakyturtle/jellytrim/internal/store"
)

// postRestore posts the Restore form, with the "anyway" field when asked.
func postRestore(t *testing.T, e queueTestEnv, id int64, anyway bool) (*http.Response, string) {
	t.Helper()
	form := url.Values{}
	if anyway {
		form.Set("anyway", "1")
	}
	req := httptest.NewRequest("POST", "/history/"+strconv.FormatInt(id, 10)+"/restore", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	res := rec.Result()
	body, _ := io.ReadAll(res.Body)
	return res, string(body)
}

func TestHistoryRestoreAnywayWhenJellyfinCannotSay(t *testing.T) {
	e := newQueueTestEnv(t)
	t.Cleanup(func() { e.q.Stop(); e.lib.Wait() })
	ctx := context.Background()
	rows, _, err := e.st.LibraryList(ctx, store.LibraryFilter{Search: "Alpha", Limit: 1})
	libraryTestMust(t, err)
	alpha := rows[0].Item.ID

	// An optimised file in place, with its original kept as the backup.
	local := filepath.Join(t.TempDir(), "Alpha (2019).mkv")
	libraryTestMust(t, os.WriteFile(local, []byte("the optimised file"), 0o644))
	info, err := fileid.Stat(local)
	libraryTestMust(t, err)
	outID, _ := json.Marshal(store.FileIdentity{Dev: info.Dev, Inode: info.Inode, Size: info.Size, MtimeNs: info.MtimeNs})
	exp := time.Date(2027, 1, 3, 0, 0, 0, 0, time.UTC)
	id := e.seed(t, queueTestJob{item: alpha, name: "Alpha (2019)", status: store.JobComplete, local: local, backup: true,
		outcome: store.JobOutcome{OutputSize: &info.Size, OutputIdentity: string(outID), BackupExpiresAt: &exp}})
	libraryTestMust(t, os.WriteFile(pipeline.BackupPath(local, id), []byte("the original"), 0o644))
	unchanged := func() {
		t.Helper()
		if b, _ := os.ReadFile(local); string(b) != "the optimised file" {
			t.Fatal("the file was changed")
		}
	}

	// Jellyfin reports playback: refused, even when asked to go ahead.
	e.jf.SetPlaying(alpha, "alex", "Living Room TV", false)
	res, body := postRestore(t, e, id, true)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("playing: status %d", res.StatusCode)
	}
	queueExpect(t, body, "Not restored while someone is watching", "alex on Living Room TV")
	queueExpectNot(t, body, "Restore anyway")
	unchanged()
	e.jf.StopPlaying(alpha)

	// Jellyfin cannot answer: refused, and "Restore anyway" is offered.
	for range 3 { // every attempt the client makes
		e.jf.FailNext("/Sessions", http.StatusServiceUnavailable, 0)
	}
	res, body = postRestore(t, e, id, false)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("Jellyfin down: status %d", res.StatusCode)
	}
	queueExpect(t, body, "Not restored: Jellyfin did not answer", "you can restore it anyway",
		"Restore anyway", "Restore without checking?")
	queueExpectRaw(t, body, `href="#history-restore-anyway"`, `name="anyway" value="1"`)
	unchanged()

	// The second step restores without the check.
	for range 3 {
		e.jf.FailNext("/Sessions", http.StatusServiceUnavailable, 0)
	}
	res, _ = postRestore(t, e, id, true)
	if res.StatusCode != http.StatusSeeOther || !strings.HasSuffix(res.Header.Get("Location"), "?restored=1") {
		t.Fatalf("restore anyway: status %d, location %q", res.StatusCode, res.Header.Get("Location"))
	}
	if b, _ := os.ReadFile(local); string(b) != "the original" {
		t.Fatalf("the original was not put back: %q", b)
	}
}

func TestQueueTooLateToCancel(t *testing.T) {
	e := newQueueTestEnv(t)
	_, body := e.get(t, "/queue?notice=toolate", false)
	queueExpect(t, body, "Too late to cancel", "JellyTrim is replacing the file now. The original is kept as a backup.")

	// A job past its last check offers no Cancel.
	j := store.Job{ID: 9, ItemName: "Alpha (2019)", Status: store.JobReplacing}
	v := e.srv.queueRunningView(j, queue.Live{Status: store.JobReplacing, Committed: true})
	if !v.Committed || v.Readout != "Replacing the file" {
		t.Fatalf("view %+v", v)
	}
	if v = e.srv.queueRunningView(j, queue.Live{Status: store.JobReplacing}); v.Committed {
		t.Fatal("a job still waiting to replace must offer Cancel")
	}
}
