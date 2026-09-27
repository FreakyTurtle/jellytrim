package config

import (
	"errors"
	"log/slog"
	"testing"
)

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func noFile(string) ([]byte, error) { return nil, errors.New("no file") }

func TestFromEnvDefaults(t *testing.T) {
	c, err := FromEnv(env(nil), noFile)
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != ":8080" || c.ConfigDir != "/config" || c.FFmpeg != "ffmpeg" || c.LogLevel != slog.LevelInfo {
		t.Fatalf("unexpected defaults: %+v", c)
	}
}

func TestFromEnvOverrides(t *testing.T) {
	c, err := FromEnv(env(map[string]string{
		"JELLYTRIM_LISTEN":       "127.0.0.1:9000",
		"JELLYTRIM_LOG_LEVEL":    "debug",
		"JELLYTRIM_LOG_FORMAT":   "json",
		"JELLYTRIM_JELLYFIN_URL": " http://jellyfin:8096 ",
	}), noFile)
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != "127.0.0.1:9000" || c.LogLevel != slog.LevelDebug || !c.LogJSON || c.JellyfinURL != "http://jellyfin:8096" {
		t.Fatalf("overrides not applied: %+v", c)
	}
}

func TestAPIKeyFromFile(t *testing.T) {
	read := func(p string) ([]byte, error) {
		if p == "/run/secrets/key" {
			return []byte("deadbeefdeadbeefdeadbeefdeadbeef\n"), nil
		}
		return nil, errors.New("missing")
	}
	c, err := FromEnv(env(map[string]string{"JELLYTRIM_JELLYFIN_API_KEY_FILE": "/run/secrets/key"}), read)
	if err != nil {
		t.Fatal(err)
	}
	if c.JellyfinAPIKey != "deadbeefdeadbeefdeadbeefdeadbeef" {
		t.Fatalf("key not read from file: %q", c.JellyfinAPIKey)
	}
}

func TestAPIKeyBothSetIsError(t *testing.T) {
	_, err := FromEnv(env(map[string]string{
		"JELLYTRIM_JELLYFIN_API_KEY":      "a",
		"JELLYTRIM_JELLYFIN_API_KEY_FILE": "/x",
	}), noFile)
	if err == nil {
		t.Fatal("expected an error when both the key and the key file are set")
	}
}

func TestBadLogFormat(t *testing.T) {
	if _, err := FromEnv(env(map[string]string{"JELLYTRIM_LOG_FORMAT": "xml"}), noFile); err == nil {
		t.Fatal("expected error")
	}
}

func TestEnsureConfigDir(t *testing.T) {
	c := Defaults()
	c.ConfigDir = t.TempDir() + "/nested/config"
	if err := c.EnsureConfigDir(); err != nil {
		t.Fatal(err)
	}
}
