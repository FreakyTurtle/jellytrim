-- JellyTrim initial schema. Times are Unix seconds (UTC) unless the column
-- name ends in _ns. JSON columns hold TEXT owned by a Go type.

CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE path_mappings (
    id              INTEGER PRIMARY KEY,
    jellyfin_prefix TEXT NOT NULL,
    local_prefix    TEXT NOT NULL,
    position        INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE libraries (
    id              TEXT PRIMARY KEY,          -- Jellyfin ItemId
    name            TEXT NOT NULL,
    collection_type TEXT NOT NULL DEFAULT '',  -- movies, tvshows, ...
    locations       TEXT NOT NULL DEFAULT '[]', -- JSON array of Jellyfin paths
    managed         INTEGER NOT NULL DEFAULT 0,
    updated_at      INTEGER NOT NULL
);

CREATE TABLE jellyfin_users (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    disabled   INTEGER NOT NULL DEFAULT 0,
    selected   INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL
);

CREATE TABLE sync_runs (
    id          INTEGER PRIMARY KEY,
    started_at  INTEGER NOT NULL,
    finished_at INTEGER,
    status      TEXT NOT NULL,                 -- running, complete, failed
    items_seen  INTEGER NOT NULL DEFAULT 0,
    error       TEXT NOT NULL DEFAULT ''
);

CREATE TABLE items (
    id               TEXT PRIMARY KEY,         -- Jellyfin item Id
    library_id       TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    type             TEXT NOT NULL,            -- Movie, Episode
    name             TEXT NOT NULL,
    sort_name        TEXT NOT NULL DEFAULT '',
    series_id        TEXT NOT NULL DEFAULT '',
    series_name      TEXT NOT NULL DEFAULT '',
    season_name      TEXT NOT NULL DEFAULT '',
    season_number    INTEGER,
    episode_number   INTEGER,
    year             INTEGER,
    jellyfin_path    TEXT NOT NULL,
    local_path       TEXT NOT NULL DEFAULT '', -- empty when no mapping applies
    date_added       INTEGER,
    runtime_ticks    INTEGER,
    jellyfin_size    INTEGER,
    jellyfin_codec   TEXT NOT NULL DEFAULT '',
    tags             TEXT NOT NULL DEFAULT '[]',
    genres           TEXT NOT NULL DEFAULT '[]',
    has_image        INTEGER NOT NULL DEFAULT 0,
    sync_skip_reason TEXT NOT NULL DEFAULT '', -- why the item cannot be managed at all
    seen_sync_id     INTEGER NOT NULL,
    updated_at       INTEGER NOT NULL
);
CREATE INDEX items_library ON items(library_id);
CREATE INDEX items_series ON items(series_id);
CREATE INDEX items_local_path ON items(local_path);

CREATE TABLE item_user_data (
    item_id        TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    user_id        TEXT NOT NULL,
    played         INTEGER NOT NULL DEFAULT 0,
    play_count     INTEGER NOT NULL DEFAULT 0,
    favorite       INTEGER NOT NULL DEFAULT 0,
    last_played_at INTEGER,
    PRIMARY KEY (item_id, user_id)
);

CREATE TABLE collections (
    id   TEXT PRIMARY KEY,
    name TEXT NOT NULL
);

CREATE TABLE collection_items (
    collection_id TEXT NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
    item_id       TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    PRIMARY KEY (collection_id, item_id)
);

CREATE TABLE probes (
    item_id    TEXT PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
    local_path TEXT NOT NULL,
    dev        INTEGER NOT NULL,
    inode      INTEGER NOT NULL,
    size       INTEGER NOT NULL,
    mtime_ns   INTEGER NOT NULL,
    nlink      INTEGER NOT NULL,
    is_symlink INTEGER NOT NULL DEFAULT 0,
    probe_json TEXT NOT NULL DEFAULT '',
    frame_json TEXT NOT NULL DEFAULT '',
    error      TEXT NOT NULL DEFAULT '',
    probed_at  INTEGER NOT NULL
);

CREATE TABLE policies (
    id         INTEGER PRIMARY KEY,
    name       TEXT NOT NULL,
    enabled    INTEGER NOT NULL DEFAULT 0,
    priority   INTEGER NOT NULL,
    scope      TEXT NOT NULL,
    conditions TEXT NOT NULL,
    action     TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE evaluations (
    item_id       TEXT PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
    outcome       TEXT NOT NULL,               -- optimise, optimal, protected, skipped, no_policy
    policy_id     INTEGER,
    summary       TEXT NOT NULL DEFAULT '',    -- one plain-English line
    explanation   TEXT NOT NULL DEFAULT '[]',  -- JSON
    reasons       TEXT NOT NULL DEFAULT '[]',  -- JSON
    plan          TEXT NOT NULL DEFAULT '',    -- JSON, when outcome is optimise
    est_min_bytes INTEGER,
    est_max_bytes INTEGER,
    evaluated_at  INTEGER NOT NULL
);
CREATE INDEX evaluations_outcome ON evaluations(outcome);

CREATE TABLE jobs (
    id                INTEGER PRIMARY KEY,
    item_id           TEXT NOT NULL,           -- no FK: history outlives items
    item_name         TEXT NOT NULL,
    library_name      TEXT NOT NULL DEFAULT '',
    policy_id         INTEGER,
    policy_name       TEXT NOT NULL DEFAULT '',
    status            TEXT NOT NULL,           -- waiting, analysing, encoding, validating, replacing, complete, skipped, failed
    trigger           TEXT NOT NULL DEFAULT 'auto', -- auto, manual
    plan              TEXT NOT NULL,           -- JSON
    local_path        TEXT NOT NULL,
    jellyfin_path     TEXT NOT NULL,
    source_identity   TEXT NOT NULL DEFAULT '', -- JSON: dev, inode, size, mtime_ns
    source_summary    TEXT NOT NULL DEFAULT '', -- "2160p H.264"
    target_summary    TEXT NOT NULL DEFAULT '', -- "1080p HEVC"
    source_size       INTEGER,
    output_size       INTEGER,
    est_min_bytes     INTEGER,
    est_max_bytes     INTEGER,
    progress          REAL NOT NULL DEFAULT 0, -- 0..1
    speed             REAL NOT NULL DEFAULT 0, -- multiple of real time
    eta_seconds       INTEGER,
    encoder           TEXT NOT NULL DEFAULT '',
    summary           TEXT NOT NULL DEFAULT '', -- plain-English result or failure
    diagnostics       TEXT NOT NULL DEFAULT '', -- JSON: args, stderr tail, versions, checks
    partial_path      TEXT NOT NULL DEFAULT '',
    backup_path       TEXT NOT NULL DEFAULT '',
    backup_expires_at INTEGER,
    restored_at       INTEGER,
    created_at        INTEGER NOT NULL,
    started_at        INTEGER,
    finished_at       INTEGER
);
CREATE INDEX jobs_status ON jobs(status);
CREATE INDEX jobs_item ON jobs(item_id);

CREATE TABLE journal (
    id           INTEGER PRIMARY KEY,
    job_id       INTEGER NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    step         TEXT NOT NULL,                -- partial_created, backup_linked, backup_renamed, replaced, backup_deleted, partial_deleted, restored
    path         TEXT NOT NULL,
    other_path   TEXT NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL,
    completed_at INTEGER
);
CREATE INDEX journal_job ON journal(job_id);

CREATE TABLE optimised (
    dev        INTEGER NOT NULL,
    inode      INTEGER NOT NULL,
    size       INTEGER NOT NULL,
    mtime_ns   INTEGER NOT NULL,
    item_id    TEXT NOT NULL,
    job_id     INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (dev, inode, size, mtime_ns)
);

CREATE TABLE capabilities (
    backend   TEXT PRIMARY KEY,
    available INTEGER NOT NULL,
    detail    TEXT NOT NULL DEFAULT '{}',
    tested_at INTEGER NOT NULL
);
