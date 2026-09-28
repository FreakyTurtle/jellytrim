package store

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// saveProbesRaw stores a probe for each ID in one transaction: compressed
// as SaveProbe writes it, or as the plain JSON text an older JellyTrim left.
func saveProbesRaw(t *testing.T, s *Store, ids []string, legacy func(i int) bool) map[string]string {
	t.Helper()
	ctx := context.Background()
	want := map[string]string{}
	err := s.tx(ctx, func(tx *sql.Tx) error {
		for i, id := range ids {
			doc := bigJSON(20 + i%7)
			var stored any = doc
			if !legacy(i) {
				var err error
				if stored, err = packJSON(doc); err != nil {
					return err
				}
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO probes (item_id, local_path, dev, inode, size, mtime_ns, nlink,
				probe_json, frame_json, probed_at, video_codec) VALUES (?, ?, 1, ?, 100, 5, 1, ?, '', 0, 'h264')`,
				id, "/m/"+id, i, stored); err != nil {
				return err
			}
			want[id] = doc
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return want
}

func TestProbesByIDs(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	ids := seedItems(t, s, 3*maxIDsPerQuery+7)
	probed := ids[:len(ids)-10] // the last ten have no probe
	want := saveProbesRaw(t, s, probed, func(i int) bool { return i%3 == 0 })
	var legacy int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM probes WHERE typeof(probe_json) = 'text'`).Scan(&legacy); err != nil || legacy == 0 {
		t.Fatalf("%d legacy rows (%v)", legacy, err)
	}
	cases := []struct {
		name string
		ask  []string
		want int
	}{
		{"none", nil, 0},
		{"one", ids[:1], 1},
		{"exactly one chunk", ids[:maxIDsPerQuery], maxIDsPerQuery},
		{"several chunks with some unprobed", ids, len(probed)},
		{"unknown and repeated IDs", []string{"nope", ids[3], ids[3], ids[maxIDsPerQuery+1]}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.ProbesByIDs(ctx, tc.ask)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != tc.want {
				t.Fatalf("%d probes, want %d", len(got), tc.want)
			}
			for id, p := range got {
				if p.ItemID != id || p.ProbeJSON != want[id] || p.LocalPath != "/m/"+id || p.VideoCodec != "h264" {
					t.Fatalf("probe %s read back wrong: %d bytes of JSON, path %q", id, len(p.ProbeJSON), p.LocalPath)
				}
				if one, err := s.Probe(ctx, id); err != nil || !reflect.DeepEqual(one, p) {
					t.Fatalf("probe %s differs from Probe (%v)", id, err)
				}
			}
		})
	}
}

// Walking the managed items a page at a time gives every managed item once,
// in ID order, and nothing from unmanaged libraries.
func TestManagedItemsAfterWalksTheLibrary(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	ids := seedItems(t, s, 25)
	if err := s.UpsertItems(ctx, 1, []Item{{ID: "item-0005x", LibraryID: "lt", Type: "Episode", Name: "x", JellyfinPath: "/media/tv/x.mkv"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetManagedLibraries(ctx, []string{"lm"}); err != nil {
		t.Fatal(err)
	}
	all, err := s.ManagedItems(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, it := range all {
		want = append(want, it.ID)
	}
	if len(want) != len(ids)+2 { // a and b are in lm too
		t.Fatalf("%d managed items", len(want))
	}
	for _, size := range []int{1, 4, 27, 100} {
		t.Run(fmt.Sprint("pages of ", size), func(t *testing.T) {
			var got []string
			after := ""
			for {
				page, err := s.ManagedItemsAfter(ctx, after, size)
				if err != nil {
					t.Fatal(err)
				}
				if len(page) == 0 {
					break
				}
				if len(page) > size {
					t.Fatalf("page of %d, limit %d", len(page), size)
				}
				for _, it := range page {
					got = append(got, it.ID)
				}
				after = page[len(page)-1].ID
			}
			sorted := slices.Sorted(slices.Values(want))
			if !reflect.DeepEqual(got, sorted) {
				t.Fatalf("got %v\nwant %v", got, sorted)
			}
		})
	}
}

// A page must come from the primary key in order, not from sorting the
// whole table, or walking a large library costs the square of its size.
func TestManagedItemsAfterDoesNotSort(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	seedItems(t, s, 10)
	rows, err := s.db.QueryContext(ctx, `EXPLAIN QUERY PLAN `+managedItemsAfterQuery, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(plan, "; ")
	if strings.Contains(joined, "TEMP B-TREE") || !strings.Contains(joined, "sqlite_autoindex_items_1") {
		t.Fatalf("query plan: %s", joined)
	}
}

func TestPerItemReadsMatchWholeLibraryReads(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	ids := seedItems(t, s, maxIDsPerQuery+20)
	saveProbesRaw(t, s, ids[10:], func(i int) bool { return i%2 == 0 })
	if err := s.ReplaceUserData(ctx, "u1", []UserData{{ItemID: ids[0], Played: true}, {ItemID: ids[maxIDsPerQuery+5], Favourite: true}, {ItemID: "a", PlayCount: 2}}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceUserData(ctx, "u2", []UserData{{ItemID: ids[0], Played: true}}); err != nil { // u2 is disabled
		t.Fatal(err)
	}
	ask := append([]string{"a", "missing"}, ids...)

	allUD, err := s.AllUserData(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ud, err := s.UserDataFor(ctx, ask)
	if err != nil || !reflect.DeepEqual(ud, allUD) {
		t.Fatalf("UserDataFor %v, AllUserData %v (%v)", ud, allUD, err)
	}
	if one, err := s.UserDataFor(ctx, ids[1:2]); err != nil || len(one) != 0 {
		t.Fatalf("watch state for an unwatched item: %v (%v)", one, err)
	}

	allSum, err := s.ProbeSummaries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := s.ProbeSummariesFor(ctx, ask)
	if err != nil || len(sum) != len(ids)-10 || !reflect.DeepEqual(sum, allSum) {
		t.Fatalf("ProbeSummariesFor gave %d, ProbeSummaries %d (%v)", len(sum), len(allSum), err)
	}

	items, err := s.ItemsByIDs(ctx, ask)
	if err != nil || len(items) != len(ids)+1 {
		t.Fatalf("ItemsByIDs gave %d (%v)", len(items), err)
	}
	if one, err := s.Item(ctx, "a"); err != nil || !reflect.DeepEqual(one, items["a"]) {
		t.Fatalf("ItemsByIDs %+v, Item %+v (%v)", items["a"], one, err)
	}
}
