package jellyfin

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

// PublicInfo reads /System/Info/Public. Jellyfin answers it without a key,
// so it proves the URL is right but not that the key works; see Ping.
func (c *Client) PublicInfo(ctx context.Context) (SystemInfo, error) {
	var info SystemInfo
	err := c.getJSON(ctx, "/System/Info/Public", nil, &info)
	return info, err
}

// Ping reads /System/Info, which needs a valid key, so success proves both
// the URL and the key.
func (c *Client) Ping(ctx context.Context) (SystemInfo, error) {
	var info SystemInfo
	err := c.getJSON(ctx, "/System/Info", nil, &info)
	return info, err
}

// Libraries lists the server's libraries. It needs an administrator's
// access, which an API key has.
func (c *Client) Libraries(ctx context.Context) ([]Library, error) {
	var libs []Library
	err := c.getJSON(ctx, "/Library/VirtualFolders", nil, &libs)
	return libs, err
}

// Users lists every user, including disabled and hidden ones; callers
// decide which to skip.
func (c *Client) Users(ctx context.Context) ([]User, error) {
	var users []User
	err := c.getJSON(ctx, "/Users", nil, &users)
	return users, err
}

// mediaUpdate is one entry in a /Library/Media/Updated request.
type mediaUpdate struct {
	Path       string `json:"Path"`
	UpdateType string `json:"UpdateType"`
}

// NotifyMediaUpdated asks Jellyfin to rescan files it knows by these paths.
// The paths must already be mapped to Jellyfin's view of the filesystem.
// Jellyfin answers 204 and scans in the background.
func (c *Client) NotifyMediaUpdated(ctx context.Context, jellyfinPaths ...string) error {
	if len(jellyfinPaths) == 0 {
		return nil
	}
	body := struct {
		Updates []mediaUpdate `json:"Updates"`
	}{Updates: make([]mediaUpdate, 0, len(jellyfinPaths))}
	for _, p := range jellyfinPaths {
		body.Updates = append(body.Updates, mediaUpdate{Path: p, UpdateType: "Modified"})
	}
	return c.post(ctx, "/Library/Media/Updated", nil, body)
}

// RefreshItem asks Jellyfin to re-read one item's file and metadata, without
// replacing existing metadata or images. It is the fallback when
// NotifyMediaUpdated does not pick up a change.
func (c *Client) RefreshItem(ctx context.Context, itemID string) error {
	q := url.Values{
		"metadataRefreshMode": {"FullRefresh"},
		"imageRefreshMode":    {"None"},
		"replaceAllMetadata":  {"false"},
		"replaceAllImages":    {"false"},
	}
	return c.post(ctx, escape("/Items/%s/Refresh", itemID), q, nil)
}

// Image fetches an item's primary image, scaled to at most maxWidth pixels,
// for JellyTrim's artwork proxy. The caller closes the reader. An item with
// no artwork gives an error matching ErrNotFound.
func (c *Client) Image(ctx context.Context, itemID string, maxWidth int) (io.ReadCloser, string, error) {
	q := url.Values{"quality": {"85"}}
	if maxWidth > 0 {
		q.Set("maxWidth", strconv.Itoa(maxWidth))
	}
	resp, err := c.send(ctx, call{
		method: http.MethodGet,
		path:   escape("/Items/%s/Images/Primary", itemID),
		query:  q,
		image:  true,
	})
	if err != nil {
		return nil, "", err
	}
	return resp.Body, resp.Header.Get("Content-Type"), nil
}
