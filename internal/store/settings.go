package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/freakyturtle/jellytrim/internal/timetable"
)

// Setting keys. Values are stored as text.
const (
	KeySetupComplete  = "setup_complete"
	KeyJellyfinURL    = "jellyfin_url"
	KeyJellyfinAPIKey = "jellyfin_api_key" // #nosec G101 -- a settings key name, not a credential
	KeyJellyfinServer = "jellyfin_server_name"
	KeyDeviceID       = "device_id"
	KeyDryRun         = "dry_run"
	// KeyWatchMode is the old any/all watch mode, replaced by
	// KeyWatchPercent. It is read only to derive the percentage for an
	// install that has not saved one.
	KeyWatchMode = "watch_mode" // any, all
	// KeyWatchUsers says whose watch state counts: WatchUsersEveryone or
	// WatchUsersSelected.
	KeyWatchUsers = "watch_users"
	// KeyWatchPercent is the share of counted users (0 to 100) that must
	// have watched an item; 0 means any one of them.
	KeyWatchPercent = "watch_percent"
	// KeyWatchInactiveDays leaves out users with no Jellyfin activity for
	// more than this many days; 0 turns the filter off.
	KeyWatchInactiveDays = "watch_inactive_days"
	KeySyncIntervalHours = "sync_interval_hours"
	KeySyncDailyAt       = "sync_daily_at" // HH:MM; empty means use the interval
	KeyDefaultCodec      = "default_codec"
	KeyAutoProcess       = "auto_process"
	KeyConcurrency       = "concurrency"
	KeyMinSavingPercent  = "min_saving_percent"
	KeyBackupDays        = "backup_retention_days"
	KeyWindowStart       = "window_start" // replaced by KeyProcessingSchedule; read once to migrate
	KeyWindowEnd         = "window_end"
	// KeyProcessingSchedule is the weekly hour grid (see internal/timetable):
	// 168 characters of 0 and 1, Monday 00:00 first. Empty means any time.
	KeyProcessingSchedule = "processing_schedule"
	KeyEncoderPreference  = "encoder_preference" // hardware, software
	KeyMatchBitDepth      = "match_source_bit_depth"
	KeyValidation         = "validation" // full, sampled
	KeyX265Preset         = "x265_preset"
	KeyQualityOverrides   = "quality_overrides" // JSON
	KeyDefaultQuality     = "default_quality"
	KeyQueuePaused        = "queue_paused"
)

// Values of KeyWatchUsers.
const (
	// WatchUsersEveryone counts every enabled Jellyfin user, including
	// users added later.
	WatchUsersEveryone = "everyone"
	// WatchUsersSelected counts only the users ticked in Settings.
	WatchUsersSelected = "selected"
)

// Settings is the typed view of the settings table with defaults applied.
type Settings struct {
	SetupComplete  bool
	JellyfinURL    string
	HasAPIKey      bool
	JellyfinServer string
	DeviceID       string
	DryRun         bool
	// WatchMode is the old any/all setting, kept readable for migration.
	// Use WatchPercent.
	WatchMode string
	// WatchUsers is WatchUsersEveryone or WatchUsersSelected.
	WatchUsers string
	// WatchPercent is 0 to 100. When it was never saved it is derived from
	// WatchMode: "all" gives 100, anything else 0.
	WatchPercent int
	// WatchInactiveDays is 0 (off) or more.
	WatchInactiveDays int
	SyncIntervalHours int
	SyncDailyAt       string
	DefaultCodec      string
	AutoProcess       bool
	Concurrency       int
	MinSavingPercent  int
	BackupDays        int
	WindowStart       string
	WindowEnd         string
	// ProcessingSchedule is the stored weekly grid. When it has never been
	// saved, it is derived from the old daily window so nothing changes.
	ProcessingSchedule string
	EncoderPreference  string
	MatchBitDepth      bool
	Validation         string
	X265Preset         string
	QualityOverrides   string
	DefaultQuality     string
}

var settingDefaults = map[string]string{
	KeyDryRun:            "true",
	KeyWatchMode:         "any",
	KeyWatchUsers:        WatchUsersEveryone,
	KeyWatchInactiveDays: "90",
	KeySyncIntervalHours: "6",
	KeyAutoProcess:       "true",
	KeyConcurrency:       "1",
	KeyMinSavingPercent:  "10",
	KeyBackupDays:        "7",
	KeyEncoderPreference: "hardware",
	KeyMatchBitDepth:     "false",
	KeyValidation:        "full",
	KeyX265Preset:        "slow",
	KeyQualityOverrides:  "{}",
	KeyDefaultQuality:    "high",
	KeyDefaultCodec:      "hevc",
}

// Setting returns a raw setting value, or its default, or "".
func (s *Store) Setting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return settingDefaults[key], nil
	}
	if err != nil {
		return "", fmt.Errorf("reading setting %s: %w", key, err)
	}
	return v, nil
}

// SetSetting stores a raw setting value.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	if err != nil {
		return fmt.Errorf("saving setting %s: %w", key, err)
	}
	return nil
}

// SetSettings stores several settings in one transaction.
func (s *Store) SetSettings(ctx context.Context, kv map[string]string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for k, v := range kv {
		if _, err := tx.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value`, k, v); err != nil {
			return fmt.Errorf("saving setting %s: %w", k, err)
		}
	}
	return tx.Commit()
}

// JellyfinAPIKey returns the stored key. Callers must never render or log it.
func (s *Store) JellyfinAPIKey(ctx context.Context) (string, error) {
	return s.Setting(ctx, KeyJellyfinAPIKey)
}

// Settings loads every setting with defaults applied. The API key itself is
// not included, only whether one is saved.
func (s *Store) Settings(ctx context.Context) (Settings, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return Settings{}, fmt.Errorf("reading settings: %w", err)
	}
	defer func() { _ = rows.Close() }()
	m := make(map[string]string, len(settingDefaults))
	for k, v := range settingDefaults {
		m[k] = v
	}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return Settings{}, err
		}
		m[k] = v
	}
	if err := rows.Err(); err != nil {
		return Settings{}, err
	}
	b := func(k string) bool { return m[k] == "true" }
	i := func(k string) int {
		n, err := strconv.Atoi(m[k])
		if err != nil {
			n, _ = strconv.Atoi(settingDefaults[k])
		}
		return n
	}
	return Settings{
		SetupComplete:      b(KeySetupComplete),
		JellyfinURL:        m[KeyJellyfinURL],
		HasAPIKey:          m[KeyJellyfinAPIKey] != "",
		JellyfinServer:     m[KeyJellyfinServer],
		DeviceID:           m[KeyDeviceID],
		DryRun:             b(KeyDryRun),
		WatchMode:          m[KeyWatchMode],
		WatchUsers:         watchUsers(m[KeyWatchUsers]),
		WatchPercent:       watchPercent(m),
		WatchInactiveDays:  max(i(KeyWatchInactiveDays), 0),
		SyncIntervalHours:  i(KeySyncIntervalHours),
		SyncDailyAt:        m[KeySyncDailyAt],
		DefaultCodec:       m[KeyDefaultCodec],
		AutoProcess:        b(KeyAutoProcess),
		Concurrency:        i(KeyConcurrency),
		MinSavingPercent:   i(KeyMinSavingPercent),
		BackupDays:         i(KeyBackupDays),
		WindowStart:        m[KeyWindowStart],
		WindowEnd:          m[KeyWindowEnd],
		ProcessingSchedule: schedule(m),
		EncoderPreference:  m[KeyEncoderPreference],
		MatchBitDepth:      b(KeyMatchBitDepth),
		Validation:         m[KeyValidation],
		X265Preset:         m[KeyX265Preset],
		QualityOverrides:   m[KeyQualityOverrides],
		DefaultQuality:     m[KeyDefaultQuality],
	}, nil
}

// FormatBool renders a bool as a setting value.
func FormatBool(v bool) string { return strconv.FormatBool(v) }

// watchUsers reads KeyWatchUsers, treating anything unknown as everyone.
func watchUsers(v string) string {
	if v == WatchUsersSelected {
		return v
	}
	return WatchUsersEveryone
}

// watchPercent reads KeyWatchPercent, or derives it from the old watch mode
// when it was never saved. KeyWatchPercent has no default in settingDefaults
// so that "never saved" can be told apart.
func watchPercent(m map[string]string) int {
	v, ok := m[KeyWatchPercent]
	if !ok {
		if m[KeyWatchMode] == "all" {
			return 100
		}
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0
	}
	return min(max(n, 0), 100)
}

// schedule returns the stored grid, or one converted from the old daily
// processing window when the grid has never been saved.
func schedule(m map[string]string) string {
	if s := m[KeyProcessingSchedule]; s != "" {
		return s
	}
	return timetable.FromWindow(m[KeyWindowStart], m[KeyWindowEnd]).String()
}
