-- Parsed summary of each probe, so the Library can filter and sort in SQL
-- without parsing ffprobe JSON on every page load.
ALTER TABLE probes ADD COLUMN video_codec TEXT NOT NULL DEFAULT '';
ALTER TABLE probes ADD COLUMN width INTEGER NOT NULL DEFAULT 0;
ALTER TABLE probes ADD COLUMN height INTEGER NOT NULL DEFAULT 0;
ALTER TABLE probes ADD COLUMN resolution INTEGER NOT NULL DEFAULT 0; -- class: 480, 720, 1080, 2160
ALTER TABLE probes ADD COLUMN hdr TEXT NOT NULL DEFAULT '';
ALTER TABLE probes ADD COLUMN video_bitrate INTEGER NOT NULL DEFAULT 0;
ALTER TABLE probes ADD COLUMN duration_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE probes ADD COLUMN container TEXT NOT NULL DEFAULT '';
CREATE INDEX probes_codec ON probes(video_codec);
CREATE INDEX probes_resolution ON probes(resolution);
