package jellyfin

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// DefaultPageSize is the page size for item queries. Larger pages make
// Jellyfin slow on big libraries.
const DefaultPageSize = 200

// maxPasses bounds how often AllItems re-reads a list that changed under it.
const maxPasses = 3

// DefaultItemTypes are the item types JellyTrim manages.
var DefaultItemTypes = []string{"Movie", "Episode"}

// DefaultItemFields are the extra fields requested for a full item sync.
var DefaultItemFields = []string{
	"Path", "MediaSources", "DateCreated", "Tags", "Genres",
	"ProviderIds", "ParentId", "SortName",
}

// ItemQuery selects items from /Items.
type ItemQuery struct {
	// UserID selects whose watch state comes back in UserData. Without it,
	// items carry no user data.
	UserID string
	// ParentID limits the query to one library, series, season or
	// collection. Empty means the whole server.
	ParentID string
	// IncludeItemTypes defaults to DefaultItemTypes.
	IncludeItemTypes []string
	// Fields defaults to DefaultItemFields when nil. A non-nil empty slice
	// asks for no extra fields.
	Fields []string
	// StartIndex is the first item to return (for Items; AllItems starts
	// here too).
	StartIndex int
	// Limit is the page size, DefaultPageSize when zero.
	Limit int
	// NoImages leaves out image tags, for passes that only need user data.
	NoImages bool
	// NotRecursive returns only the parent's direct children.
	NotRecursive bool
}

func (q ItemQuery) values() url.Values {
	v := url.Values{}
	if q.UserID != "" {
		v.Set("userId", q.UserID)
	}
	if q.ParentID != "" {
		v.Set("parentId", q.ParentID)
	}
	v.Set("recursive", strconv.FormatBool(!q.NotRecursive))
	types := q.IncludeItemTypes
	if types == nil {
		types = DefaultItemTypes
	}
	if len(types) > 0 {
		v.Set("includeItemTypes", strings.Join(types, ","))
	}
	fields := q.Fields
	if fields == nil {
		fields = DefaultItemFields
	}
	if len(fields) > 0 {
		v.Set("fields", strings.Join(fields, ","))
	}
	v.Set("enableUserData", "true")
	if q.NoImages {
		v.Set("enableImages", "false")
	} else {
		v.Set("enableImageTypes", "Primary")
		v.Set("imageTypeLimit", "1")
	}
	v.Set("startIndex", strconv.Itoa(max(q.StartIndex, 0)))
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultPageSize
	}
	v.Set("limit", strconv.Itoa(limit))
	// A total order keeps pages stable between requests.
	v.Set("sortBy", "SortName,Id")
	v.Set("sortOrder", "Ascending")
	return v
}

// Items returns one page of items.
func (c *Client) Items(ctx context.Context, q ItemQuery) (ItemPage, error) {
	var page ItemPage
	if err := c.getJSON(ctx, "/Items", q.values(), &page); err != nil {
		return ItemPage{}, err
	}
	return page, nil
}

// AllItems pages through every item matching q and calls fn once per item
// ID. It returns how many distinct items it saw.
//
// Jellyfin's paging is by position, so a library scan that adds or removes
// items mid-pass shifts later pages. Additions repeat an item (removed by
// deduplicating on ID); removals can hide one. AllItems therefore checks the
// total record count on every page, and re-reads the list (without repeating
// items already passed to fn) if it changed. If it is still changing after
// maxPasses, it returns ErrIncompletePass. On any error the caller must treat
// the pass as incomplete and remove nothing.
func (c *Client) AllItems(ctx context.Context, q ItemQuery, fn func(Item) error) (int, error) {
	seen := make(map[string]struct{})
	for pass := 1; pass <= maxPasses; pass++ {
		stable, err := c.itemPass(ctx, q, seen, fn)
		if err != nil {
			return len(seen), err
		}
		if stable {
			return len(seen), nil
		}
		c.log.Info("jellyfin: item list changed while paging; reading it again",
			"parent", q.ParentID, "pass", pass+1)
	}
	return len(seen), fmt.Errorf("jellyfin: paging /Items under %q: %w", q.ParentID, ErrIncompletePass)
}

// itemPass reads every page once. It reports whether the total stayed the
// same throughout, which means no item can have been skipped.
func (c *Client) itemPass(ctx context.Context, q ItemQuery, seen map[string]struct{}, fn func(Item) error) (bool, error) {
	total := -1
	stable := true
	start := max(q.StartIndex, 0)
	for {
		pq := q
		pq.StartIndex = start
		page, err := c.Items(ctx, pq)
		if err != nil {
			return false, fmt.Errorf("jellyfin: reading items from %d: %w", start, err)
		}
		if total >= 0 && page.TotalRecordCount != total {
			stable = false
		}
		total = page.TotalRecordCount
		for _, it := range page.Items {
			if err := visit(it, seen, fn); err != nil {
				return false, err
			}
		}
		start += len(page.Items)
		if len(page.Items) == 0 || start >= page.TotalRecordCount {
			return stable, nil
		}
	}
}

func visit(it Item, seen map[string]struct{}, fn func(Item) error) error {
	if it.ID == "" {
		return nil
	}
	if _, dup := seen[it.ID]; dup {
		return nil
	}
	seen[it.ID] = struct{}{}
	return fn(it)
}

// UserData pages through one user's watch state for the movies and episodes
// under parentID, asking for no extra fields so the pass is cheap. Like
// AllItems, an error means the pass is incomplete.
func (c *Client) UserData(ctx context.Context, userID, parentID string, fn func(itemID string, ud UserData) error) error {
	if userID == "" {
		return errors.New("jellyfin: user data needs a user ID")
	}
	q := ItemQuery{UserID: userID, ParentID: parentID, Fields: []string{}, NoImages: true}
	_, err := c.AllItems(ctx, q, func(it Item) error { return fn(it.ID, it.UserData) })
	return err
}

// Collections lists the collections (box sets) visible to userID.
func (c *Client) Collections(ctx context.Context, userID string) ([]Collection, error) {
	q := ItemQuery{UserID: userID, IncludeItemTypes: []string{"BoxSet"}, Fields: []string{}, NoImages: true}
	var out []Collection
	_, err := c.AllItems(ctx, q, func(it Item) error {
		out = append(out, Collection{ID: it.ID, Name: it.Name})
		return nil
	})
	return out, err
}

// CollectionItemIDs lists the IDs of the items in one collection.
func (c *Client) CollectionItemIDs(ctx context.Context, userID, collectionID string) ([]string, error) {
	q := ItemQuery{
		UserID:           userID,
		ParentID:         collectionID,
		IncludeItemTypes: []string{},
		Fields:           []string{},
		NoImages:         true,
		NotRecursive:     true,
	}
	var ids []string
	_, err := c.AllItems(ctx, q, func(it Item) error {
		ids = append(ids, it.ID)
		return nil
	})
	return ids, err
}

// Item fetches one item with its path and media sources, as userID sees it.
// It uses /Items/{id} (10.9 and later) and falls back to the older
// /Users/{userId}/Items/{id} when that route is missing.
func (c *Client) Item(ctx context.Context, userID, itemID string) (Item, error) {
	q := url.Values{"fields": {"MediaSources,Path"}}
	if userID != "" {
		q.Set("userId", userID)
	}
	var it Item
	err := c.getJSON(ctx, escape("/Items/%s", itemID), q, &it)
	var se *StatusError
	if errors.As(err, &se) && (se.StatusCode == 404 || se.StatusCode == 405) && userID != "" {
		it = Item{}
		err = c.getJSON(ctx, escape("/Users/%s/Items/%s", userID, itemID), url.Values{"fields": {"MediaSources,Path"}}, &it)
	}
	if err != nil {
		return Item{}, err
	}
	// Jellyfin 12.1 answers an all-zero ID with the user's root folder
	// instead of 404, so check it returned the item that was asked for.
	if normaliseID(it.ID) != normaliseID(itemID) {
		return Item{}, fmt.Errorf("jellyfin: item %s: %w", itemID, ErrNotFound)
	}
	return it, nil
}

// normaliseID makes the dashed and undashed forms of a GUID compare equal.
func normaliseID(id string) string {
	return strings.ToLower(strings.ReplaceAll(id, "-", ""))
}
