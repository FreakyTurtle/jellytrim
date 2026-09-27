package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// FileIdentity identifies a file's exact contents on disk well enough to
// notice any change: a new inode, a different size or a new mtime.
type FileIdentity struct {
	Dev     uint64 `json:"dev"`
	Inode   uint64 `json:"inode"`
	Size    int64  `json:"size"`
	MtimeNs int64  `json:"mtime_ns"`
}

// Probe is the cached ffprobe result for one item's file.
type Probe struct {
	ItemID    string
	LocalPath string
	FileIdentity
	Nlink     int
	IsSymlink bool
	ProbeJSON string
	FrameJSON string
	Error     string
	ProbedAt  time.Time

	// Parsed summary, for filtering.
	VideoCodec   string
	Width        int
	Height       int
	Resolution   int
	HDR          string
	VideoBitrate int64
	DurationMs   int64
	Container    string
}

// SaveProbe stores or replaces an item's probe.
func (s *Store) SaveProbe(ctx context.Context, p Probe) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO probes (item_id, local_path, dev, inode, size, mtime_ns, nlink, is_symlink,
		probe_json, frame_json, error, probed_at, video_codec, width, height, resolution, hdr, video_bitrate, duration_ms, container)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(item_id) DO UPDATE SET local_path = excluded.local_path, dev = excluded.dev, inode = excluded.inode,
		size = excluded.size, mtime_ns = excluded.mtime_ns, nlink = excluded.nlink, is_symlink = excluded.is_symlink,
		probe_json = excluded.probe_json, frame_json = excluded.frame_json, error = excluded.error,
		probed_at = excluded.probed_at, video_codec = excluded.video_codec, width = excluded.width,
		height = excluded.height, resolution = excluded.resolution, hdr = excluded.hdr,
		video_bitrate = excluded.video_bitrate, duration_ms = excluded.duration_ms, container = excluded.container`,
		p.ItemID, p.LocalPath, int64(p.Dev), int64(p.Inode), p.Size, p.MtimeNs, p.Nlink, p.IsSymlink,
		p.ProbeJSON, p.FrameJSON, p.Error, s.unix(), p.VideoCodec, p.Width, p.Height, p.Resolution, p.HDR,
		p.VideoBitrate, p.DurationMs, p.Container)
	if err != nil {
		return fmt.Errorf("saving probe for %s: %w", p.ItemID, err)
	}
	return nil
}

const probeColumns = `item_id, local_path, dev, inode, size, mtime_ns, nlink, is_symlink, probe_json, frame_json, error,
	probed_at, video_codec, width, height, resolution, hdr, video_bitrate, duration_ms, container`

func scanProbe(r scanner) (Probe, error) {
	var p Probe
	var dev, inode, probed int64
	err := r.Scan(&p.ItemID, &p.LocalPath, &dev, &inode, &p.Size, &p.MtimeNs, &p.Nlink, &p.IsSymlink, &p.ProbeJSON,
		&p.FrameJSON, &p.Error, &probed, &p.VideoCodec, &p.Width, &p.Height, &p.Resolution, &p.HDR, &p.VideoBitrate,
		&p.DurationMs, &p.Container)
	p.Dev, p.Inode = uint64(dev), uint64(inode)
	p.ProbedAt = time.Unix(probed, 0).UTC()
	return p, err
}

// Probe returns an item's cached probe, or ErrNotFound.
func (s *Store) Probe(ctx context.Context, itemID string) (Probe, error) {
	p, err := scanProbe(s.db.QueryRowContext(ctx, `SELECT `+probeColumns+` FROM probes WHERE item_id = ?`, itemID))
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

// Probes returns every cached probe keyed by item ID.
func (s *Store) Probes(ctx context.Context) (map[string]Probe, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+probeColumns+` FROM probes`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]Probe{}
	for rows.Next() {
		p, err := scanProbe(rows)
		if err != nil {
			return nil, err
		}
		out[p.ItemID] = p
	}
	return out, rows.Err()
}

// DeleteProbe removes an item's cached probe so it is probed again.
func (s *Store) DeleteProbe(ctx context.Context, itemID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM probes WHERE item_id = ?`, itemID)
	return err
}

// IsOptimised reports whether JellyTrim produced the file with this identity.
func (s *Store) IsOptimised(ctx context.Context, id FileIdentity) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM optimised WHERE dev = ? AND inode = ? AND size = ? AND mtime_ns = ?`,
		int64(id.Dev), int64(id.Inode), id.Size, id.MtimeNs).Scan(&n)
	return n > 0, err
}

// RecordOptimised remembers a file JellyTrim produced, so it is not encoded again.
func (s *Store) RecordOptimised(ctx context.Context, id FileIdentity, itemID string, jobID int64) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO optimised (dev, inode, size, mtime_ns, item_id, job_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, int64(id.Dev), int64(id.Inode), id.Size, id.MtimeNs, itemID, jobID, s.unix())
	return err
}
