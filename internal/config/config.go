// Package config reads JellyTrim's bootstrap settings from the environment.
// Everything else is configured in the web UI and stored in SQLite.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// Config holds the settings needed before the database is open.
type Config struct {
	Listen    string
	ConfigDir string
	FFmpeg    string
	FFprobe   string
	LogLevel  slog.Level
	LogJSON   bool

	// Optional preset Jellyfin connection. Applied on first start only.
	JellyfinURL    string
	JellyfinAPIKey string
}

// Defaults returns the configuration used when nothing is set.
func Defaults() Config {
	return Config{
		Listen:    ":8080",
		ConfigDir: "/config",
		FFmpeg:    "ffmpeg",
		FFprobe:   "ffprobe",
		LogLevel:  slog.LevelInfo,
	}
}

// FromEnv reads JELLYTRIM_* variables over the defaults. lookup is usually
// os.LookupEnv; readFile is usually os.ReadFile (used for _FILE variables).
func FromEnv(lookup func(string) (string, bool), readFile func(string) ([]byte, error)) (Config, error) {
	c := Defaults()
	str := func(name string, dst *string) {
		if v, ok := lookup(name); ok && strings.TrimSpace(v) != "" {
			*dst = strings.TrimSpace(v)
		}
	}
	str("JELLYTRIM_LISTEN", &c.Listen)
	str("JELLYTRIM_CONFIG_DIR", &c.ConfigDir)
	str("JELLYTRIM_FFMPEG", &c.FFmpeg)
	str("JELLYTRIM_FFPROBE", &c.FFprobe)
	str("JELLYTRIM_JELLYFIN_URL", &c.JellyfinURL)

	key, err := secret(lookup, readFile, "JELLYTRIM_JELLYFIN_API_KEY")
	if err != nil {
		return c, err
	}
	c.JellyfinAPIKey = key

	if v, ok := lookup("JELLYTRIM_LOG_LEVEL"); ok && v != "" {
		if err := c.LogLevel.UnmarshalText([]byte(v)); err != nil {
			return c, fmt.Errorf("JELLYTRIM_LOG_LEVEL: %w", err)
		}
	}
	if v, ok := lookup("JELLYTRIM_LOG_FORMAT"); ok {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "", "text":
		case "json":
			c.LogJSON = true
		default:
			return c, fmt.Errorf("JELLYTRIM_LOG_FORMAT must be text or json, got %q", v)
		}
	}
	return c, nil
}

// secret reads NAME, or the file named by NAME_FILE. Setting both is an error.
func secret(lookup func(string) (string, bool), readFile func(string) ([]byte, error), name string) (string, error) {
	direct, hasDirect := lookup(name)
	path, hasFile := lookup(name + "_FILE")
	hasDirect = hasDirect && direct != ""
	hasFile = hasFile && path != ""
	switch {
	case hasDirect && hasFile:
		return "", fmt.Errorf("set %s or %s_FILE, not both", name, name)
	case hasFile:
		b, err := readFile(path)
		if err != nil {
			return "", fmt.Errorf("reading %s_FILE: %w", name, err)
		}
		return strings.TrimSpace(string(b)), nil
	default:
		return strings.TrimSpace(direct), nil
	}
}

// Load reads the configuration from the process environment.
func Load() (Config, error) {
	return FromEnv(os.LookupEnv, os.ReadFile)
}

// ErrNoConfigDir is returned when the config directory cannot be used.
var ErrNoConfigDir = errors.New("config directory is not usable")

// EnsureConfigDir creates the config directory if needed and checks it is writable.
func (c Config) EnsureConfigDir() error {
	if err := os.MkdirAll(c.ConfigDir, 0o750); err != nil {
		return fmt.Errorf("%w: %s: %v", ErrNoConfigDir, c.ConfigDir, err)
	}
	f, err := os.CreateTemp(c.ConfigDir, ".write-test-*")
	if err != nil {
		return fmt.Errorf("%w: %s is not writable by this user: %v", ErrNoConfigDir, c.ConfigDir, err)
	}
	name := f.Name()
	_ = f.Close()
	return os.Remove(name)
}
