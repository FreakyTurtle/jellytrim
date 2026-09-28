-- Evaluations are written in batches while a run goes, instead of in one
-- transaction at the end, so evaluating a large library never holds every
-- result in memory. Each row records the run that last wrote it; a run that
-- finishes deletes the rows older runs left behind (items now pending, or no
-- longer managed). A run that stops part-way deletes nothing, so every item
-- keeps its latest evaluation until the next complete run.
CREATE TABLE evaluation_runs (
    id          INTEGER PRIMARY KEY,
    started_at  INTEGER NOT NULL,
    finished_at INTEGER                      -- NULL while running, or if the run stopped
);

-- Rows written before this migration belong to run 0, which every run
-- replaces.
ALTER TABLE evaluations ADD COLUMN run_id INTEGER NOT NULL DEFAULT 0;
-- Finishing a run finds the rows older runs left without reading the table.
CREATE INDEX evaluations_run ON evaluations(run_id);
