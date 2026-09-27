package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestOpenAppliesMigrationsOnce(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	v1, err := s.SchemaVersion(ctx)
	if err != nil || v1 < 1 {
		t.Fatalf("schema version %d, err %v", v1, err)
	}
	_ = s.Close()

	s2, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	defer func() { _ = s2.Close() }()
	v2, _ := s2.SchemaVersion(ctx)
	if v2 != v1 {
		t.Fatalf("reopen changed version %d -> %d", v1, v2)
	}
	var n int
	if err := s2.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&n); err != nil || n != v1 {
		t.Fatalf("expected %d migration rows, got %d (%v)", v1, n, err)
	}
}

func TestDatabaseFileIsOwnerOnly(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	fi, err := os.Stat(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("database mode %o, want 600", perm)
	}
}

func TestForeignKeysOn(t *testing.T) {
	s := openTest(t)
	var on int
	if err := s.db.QueryRow(`PRAGMA foreign_keys`).Scan(&on); err != nil || on != 1 {
		t.Fatalf("foreign_keys = %d (%v)", on, err)
	}
}

func TestSettingsDefaultsAndOverrides(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	st, err := s.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !st.DryRun || st.Concurrency != 1 || st.MinSavingPercent != 10 || st.BackupDays != 7 || st.HasAPIKey {
		t.Fatalf("unexpected defaults: %+v", st)
	}
	if err := s.SetSettings(ctx, map[string]string{
		KeyDryRun: "false", KeyConcurrency: "2", KeyJellyfinAPIKey: "deadbeefdeadbeefdeadbeefdeadbeef",
	}); err != nil {
		t.Fatal(err)
	}
	st, _ = s.Settings(ctx)
	if st.DryRun || st.Concurrency != 2 || !st.HasAPIKey {
		t.Fatalf("overrides not applied: %+v", st)
	}
	key, _ := s.JellyfinAPIKey(ctx)
	if key != "deadbeefdeadbeefdeadbeefdeadbeef" {
		t.Fatalf("key = %q", key)
	}
}

func TestBadIntegerSettingFallsBackToDefault(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.SetSetting(ctx, KeyConcurrency, "lots"); err != nil {
		t.Fatal(err)
	}
	st, _ := s.Settings(ctx)
	if st.Concurrency != 1 {
		t.Fatalf("concurrency = %d, want default 1", st.Concurrency)
	}
}
