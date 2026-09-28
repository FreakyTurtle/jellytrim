-- Collection membership as Jellyfin reports it. A box set can hold a series
-- or a season rather than its episodes, so collection_items.item_id may be a
-- series or season ID that is not a row in items. The table is recreated
-- without the foreign key to items (SQLite cannot drop a constraint). Nothing
-- references collection_items, so dropping it is safe with foreign keys on.
CREATE TABLE collection_items_new (
    collection_id TEXT NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
    item_id       TEXT NOT NULL, -- a movie, episode, series or season ID
    PRIMARY KEY (collection_id, item_id)
);
INSERT INTO collection_items_new (collection_id, item_id) SELECT collection_id, item_id FROM collection_items;
DROP TABLE collection_items;
ALTER TABLE collection_items_new RENAME TO collection_items;
CREATE INDEX collection_items_item ON collection_items(item_id);

-- The season an episode belongs to, so a box set holding a season protects
-- its episodes. Filled by the next sync.
ALTER TABLE items ADD COLUMN season_id TEXT NOT NULL DEFAULT '';

-- The identity (JSON FileIdentity) of the file a job wrote, so recovery can
-- tell its output from anything else at the path. Jobs may now also have the
-- status 'attention': the job stopped in a state that needs the user to look
-- at it. It is not active (it does not block the queue); recovery revisits
-- it on every start.
ALTER TABLE jobs ADD COLUMN output_identity TEXT NOT NULL DEFAULT '';

-- At most one active job per item. Two writers could both queue the same
-- item before; keep the job that has started (or the oldest) and cancel the
-- other waiting ones.
UPDATE jobs SET status = 'cancelled', summary = 'A duplicate of another queued job for the same item.', finished_at = CAST(strftime('%s', 'now') AS INTEGER)
WHERE status = 'waiting' AND EXISTS (
    SELECT 1 FROM jobs o
    WHERE o.item_id = jobs.item_id AND o.id != jobs.id
      AND o.status IN ('waiting', 'analysing', 'encoding', 'validating', 'replacing')
      AND (o.status != 'waiting' OR o.id < jobs.id)
);
CREATE UNIQUE INDEX jobs_one_active ON jobs(item_id)
WHERE status IN ('waiting', 'analysing', 'encoding', 'validating', 'replacing');
