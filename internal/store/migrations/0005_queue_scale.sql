-- Queue and probe cache changes for libraries of 100,000+ items.
--
-- probes.probe_json and probes.frame_json may now hold a gzip-compressed
-- BLOB instead of JSON text. The store tells them apart by the gzip magic
-- bytes (0x1f 0x8b), which never start JSON. Existing rows stay as text and
-- are compressed the next time the file is probed; nothing is rewritten here.

-- The conservative estimated saving (source size minus the largest
-- estimated output, never negative), so the automatic queue runs the biggest
-- savings first.
ALTER TABLE jobs ADD COLUMN est_saving INTEGER NOT NULL DEFAULT 0;
-- The source's duration in milliseconds from its probe, for the backlog
-- estimate. 0 when unknown.
ALTER TABLE jobs ADD COLUMN duration_ms INTEGER NOT NULL DEFAULT 0;

UPDATE jobs SET est_saving = MAX(COALESCE(source_size, 0) - COALESCE(est_max_bytes, source_size, 0), 0);
UPDATE jobs SET duration_ms = COALESCE((SELECT p.duration_ms FROM probes p WHERE p.item_id = jobs.item_id), 0)
WHERE status IN ('waiting', 'analysing', 'encoding', 'validating', 'replacing');

-- One active job per file: CreateJob looks jobs up by path as well as item.
CREATE INDEX jobs_local_path ON jobs(local_path);
-- The order the dispatcher takes waiting jobs in: manual first, then the
-- biggest saving, then the oldest. The ORDER BY in NextWaitingJobs must use
-- these exact expressions. It leads with status (rather than being a
-- partial index) so the planner prefers it to jobs_status.
CREATE INDEX jobs_waiting_order ON jobs(status, (trigger != 'manual'), est_saving DESC, id);
-- Automatic queueing skips items with a recent job.
CREATE INDEX jobs_created ON jobs(created_at);
-- Recent finished jobs by status, for the encode speed history.
CREATE INDEX jobs_status_finished ON jobs(status, finished_at);
-- Both indexes above lead with status, so this one only cost writes.
DROP INDEX jobs_status;
