-- Items JellyTrim must leave alone, for example after the user restored the
-- original. Cleared only by the user.
CREATE TABLE exclusions (
    item_id    TEXT PRIMARY KEY,
    reason     TEXT NOT NULL,
    created_at INTEGER NOT NULL
);
