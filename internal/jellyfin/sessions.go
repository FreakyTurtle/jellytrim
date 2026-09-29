package jellyfin

import (
	"context"
	"net/url"
	"strconv"
)

// ActiveWithin is how recently a session must have been active for
// NowPlaying to count it, in seconds. Jellyfin's own dashboard uses the same
// value; it drops sessions a client abandoned without saying so.
const ActiveWithin = 960

// Playing is one session playing, or paused on, an item.
type Playing struct {
	// ItemID is the item being played.
	ItemID string
	// MediaSourceID is the version being played. For an item with one
	// file it equals ItemID; for an item with several versions it is the
	// ID of the version's own item.
	MediaSourceID string
	UserName      string
	DeviceName    string
	// Client is the app, for example "Jellyfin Web".
	Client string
	Paused bool
	// PositionTicks is the playback position in 100 ns ticks.
	PositionTicks int64
}

// Plays reports whether the session is playing itemID, as the item or as
// the media source. IDs are compared without dashes and ignoring case, since
// Jellyfin writes GUIDs both ways.
func (p Playing) Plays(itemID string) bool {
	id := normaliseID(itemID)
	return id != "" && (normaliseID(p.ItemID) == id || normaliseID(p.MediaSourceID) == id)
}

// session is the part of a /Sessions entry JellyTrim reads.
type session struct {
	UserName       string `json:"UserName"`
	DeviceName     string `json:"DeviceName"`
	Client         string `json:"Client"`
	NowPlayingItem *struct {
		ID string `json:"Id"`
	} `json:"NowPlayingItem"`
	PlayState struct {
		IsPaused      bool   `json:"IsPaused"`
		PositionTicks int64  `json:"PositionTicks"`
		MediaSourceID string `json:"MediaSourceId"`
	} `json:"PlayState"`
}

// NowPlaying lists what is playing on the server right now, paused sessions
// included. Sessions without a playing item (an open app on its home
// screen) are left out, and so are sessions idle for longer than
// ActiveWithin seconds. It needs an administrator's access, which an API key
// has.
func (c *Client) NowPlaying(ctx context.Context) ([]Playing, error) {
	var sessions []session
	q := url.Values{"activeWithinSeconds": {strconv.Itoa(ActiveWithin)}}
	if err := c.getJSON(ctx, "/Sessions", q, &sessions); err != nil {
		return nil, err
	}
	out := make([]Playing, 0, len(sessions))
	for _, s := range sessions {
		if s.NowPlayingItem == nil || s.NowPlayingItem.ID == "" {
			continue
		}
		out = append(out, Playing{
			ItemID:        s.NowPlayingItem.ID,
			MediaSourceID: s.PlayState.MediaSourceID,
			UserName:      s.UserName,
			DeviceName:    s.DeviceName,
			Client:        s.Client,
			Paused:        s.PlayState.IsPaused,
			PositionTicks: s.PlayState.PositionTicks,
		})
	}
	return out, nil
}
