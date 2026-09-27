package jellyfin_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/freakyturtle/jellytrim/internal/jellyfin"
)

// TestIntegrationRealServer runs against a real Jellyfin, such as the dev
// stack. It is opt-in: set JELLYTRIM_JELLYFIN_IT=1, JELLYTRIM_IT_URL and
// JELLYTRIM_IT_KEY. Every other test uses jellyfintest.
func TestIntegrationRealServer(t *testing.T) {
	if os.Getenv("JELLYTRIM_JELLYFIN_IT") != "1" {
		t.Skip("set JELLYTRIM_JELLYFIN_IT=1 with JELLYTRIM_IT_URL and JELLYTRIM_IT_KEY to run against a real Jellyfin")
	}
	url, key := os.Getenv("JELLYTRIM_IT_URL"), os.Getenv("JELLYTRIM_IT_KEY")
	if url == "" || key == "" {
		t.Fatal("JELLYTRIM_IT_URL and JELLYTRIM_IT_KEY must both be set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	c, err := jellyfin.New(url, key)
	if err != nil {
		t.Fatal(err)
	}
	info, err := c.Ping(ctx)
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}
	t.Logf("server %q, Jellyfin %s", info.ServerName, info.Version)

	libs, err := c.Libraries(ctx)
	if err != nil {
		t.Fatalf("Libraries: %v", err)
	}
	users, err := c.Users(ctx)
	if err != nil {
		t.Fatalf("Users: %v", err)
	}
	userID := ""
	for _, u := range users {
		if !u.Policy.IsDisabled {
			userID = u.ID
			break
		}
	}
	total := 0
	counts := map[string]int{}
	firstID := ""
	for _, lib := range libs {
		n, err := c.AllItems(ctx, jellyfin.ItemQuery{UserID: userID, ParentID: lib.ItemID, Limit: 5},
			func(it jellyfin.Item) error {
				counts[it.Type]++
				if firstID == "" {
					firstID = it.ID
				}
				if it.Path == "" || len(it.MediaSources) == 0 {
					t.Errorf("%s has no path or media sources", it.ID)
				}
				return nil
			})
		if err != nil {
			t.Fatalf("AllItems(%s): %v", lib.Name, err)
		}
		t.Logf("library %q (%s) at %v: %d items", lib.Name, lib.CollectionType, lib.Locations, n)
		total += n
	}
	t.Logf("%d libraries, %d users, %d items by type %v", len(libs), len(users), total, counts)
	if len(libs) == 0 || total == 0 {
		t.Fatalf("expected at least one library with items")
	}
	it, err := c.Item(ctx, userID, firstID)
	if err != nil || it.ID != firstID || it.Path == "" {
		t.Errorf("Item(%s): %+v, %v", firstID, it, err)
	}
}
