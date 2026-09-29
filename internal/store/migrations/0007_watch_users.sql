-- Whose watch state counts is now a rule: everyone (every enabled Jellyfin
-- user, including users added later) or only the ticked users, a share of
-- them that must have watched, and a filter for accounts nobody uses any
-- more. The filter reads each user's last activity from Jellyfin.
ALTER TABLE jellyfin_users ADD COLUMN last_activity_at INTEGER; -- Unix seconds UTC; NULL when never active
ALTER TABLE jellyfin_users ADD COLUMN hidden INTEGER NOT NULL DEFAULT 0; -- hidden from the sign-in screen; informational only

-- The old "any" and "all" modes become a share of users: any one (0) and
-- everyone (100). An install that never saved a mode keeps the default.
INSERT INTO settings (key, value)
SELECT 'watch_percent', CASE value WHEN 'all' THEN '100' ELSE '0' END
FROM settings
WHERE key = 'watch_mode'
  AND NOT EXISTS (SELECT 1 FROM settings WHERE key = 'watch_percent');

-- An install that ticked a deliberate subset of its users keeps counting
-- only them. Any other install (every user ticked, or none) counts
-- everyone, the new default.
INSERT INTO settings (key, value)
SELECT 'watch_users', 'selected'
WHERE NOT EXISTS (SELECT 1 FROM settings WHERE key = 'watch_users')
  AND EXISTS (SELECT 1 FROM jellyfin_users WHERE selected = 1)
  AND EXISTS (SELECT 1 FROM jellyfin_users WHERE selected = 0 AND disabled = 0);
