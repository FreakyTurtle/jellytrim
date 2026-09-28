package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// ProbeSummary is a cached probe without its JSON: the file's identity and
// the parsed summary columns. Evaluating a large library reads these
// instead of decompressing and parsing every ffprobe document.
type ProbeSummary struct {
	ItemID    string
	LocalPath string
	FileIdentity
	Nlink     int
	IsSymlink bool
	Error     string
	// HasJSON reports whether the probe holds ffprobe output. It is false
	// when probing failed.
	HasJSON  bool
	ProbedAt time.Time

	VideoCodec   string
	Width        int
	Height       int
	Resolution   int
	HDR          string
	VideoBitrate int64
	DurationMs   int64
	Container    string
}

// probeSummaryColumns are the columns scanProbeSummary reads.
// octet_length reads the stored length without loading the value.
const probeSummaryColumns = `item_id, local_path, dev, inode, size, mtime_ns, nlink, is_symlink, error,
	octet_length(probe_json) > 0, probed_at, video_codec, width, height, resolution, hdr, video_bitrate, duration_ms, container`

func scanProbeSummary(r scanner) (ProbeSummary, error) {
	var p ProbeSummary
	var dev, inode, probed int64
	if err := r.Scan(&p.ItemID, &p.LocalPath, &dev, &inode, &p.Size, &p.MtimeNs, &p.Nlink, &p.IsSymlink, &p.Error,
		&p.HasJSON, &probed, &p.VideoCodec, &p.Width, &p.Height, &p.Resolution, &p.HDR, &p.VideoBitrate,
		&p.DurationMs, &p.Container); err != nil {
		return p, err
	}
	p.Dev, p.Inode = uint64(dev), uint64(inode)
	p.ProbedAt = time.Unix(probed, 0).UTC()
	return p, nil
}

// ProbeSummaries returns every cached probe's summary keyed by item ID,
// without reading the JSON.
func (s *Store) ProbeSummaries(ctx context.Context) (map[string]ProbeSummary, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+probeSummaryColumns+` FROM probes`)
	if err != nil {
		return nil, fmt.Errorf("reading probe summaries: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]ProbeSummary{}
	for rows.Next() {
		p, err := scanProbeSummary(rows)
		if err != nil {
			return nil, fmt.Errorf("reading probe summaries: %w", err)
		}
		out[p.ItemID] = p
	}
	return out, rows.Err()
}

// CompressLegacyProbes compresses up to limit probes that an earlier
// JellyTrim stored as plain JSON text, and returns how many it rewrote.
// SaveProbe compresses every new probe, so this is only for rows written
// before the upgrade whose files have not changed since. Call it in small
// batches while the queue is idle; each batch is one short transaction.
func (s *Store) CompressLegacyProbes(ctx context.Context, limit int) (int, error) {
	n := 0
	err := s.tx(ctx, func(tx *sql.Tx) error {
		todo, err := legacyProbes(ctx, tx, limit)
		if err != nil {
			return err
		}
		for _, l := range todo {
			probe, err := packJSON(l.probe)
			if err != nil {
				return err
			}
			frames, err := packJSON(l.frames)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE probes SET probe_json = ?, frame_json = ? WHERE item_id = ?`,
				probe, frames, l.id); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("compressing stored probes: %w", err)
	}
	return n, nil
}

type legacyProbe struct{ id, probe, frames string }

func legacyProbes(ctx context.Context, tx *sql.Tx, limit int) ([]legacyProbe, error) {
	rows, err := tx.QueryContext(ctx, `SELECT item_id, probe_json, frame_json FROM probes
		WHERE typeof(probe_json) = 'text' AND octet_length(probe_json) >= ? LIMIT ?`, compressMin, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []legacyProbe
	for rows.Next() {
		var l legacyProbe
		if err := rows.Scan(&l.id, &l.probe, &l.frames); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
