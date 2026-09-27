// Package units formats sizes, bitrates and durations for people, and parses
// the few values users type. Sizes use decimal units (1 GB = 10^9 bytes),
// matching how drives and most file managers report them.
package units

import (
	"fmt"
	"math"
	"strconv"
	"time"
)

// Bytes formats a size, for example "52.4 GB". Negative sizes format as "0 B".
func Bytes(n int64) string {
	if n <= 0 {
		return "0 B"
	}
	const unit = 1000
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	v := float64(n)
	for _, suffix := range []string{"kB", "MB", "GB", "TB", "PB"} {
		v /= unit
		if v < unit || suffix == "PB" {
			return trim(v) + " " + suffix
		}
	}
	return trim(v) + " PB"
}

// Bitrate formats bits per second, for example "48.2 Mbps".
func Bitrate(bps int64) string {
	switch {
	case bps <= 0:
		return "Unknown"
	case bps >= 1_000_000:
		return trim(float64(bps)/1_000_000) + " Mbps"
	case bps >= 1_000:
		return trim(float64(bps)/1_000) + " kbps"
	}
	return strconv.FormatInt(bps, 10) + " bps"
}

// trim formats with one decimal below 100 and none above, dropping ".0".
func trim(v float64) string {
	var s string
	if v >= 100 {
		s = strconv.FormatFloat(math.Round(v), 'f', 0, 64)
	} else {
		s = strconv.FormatFloat(math.Round(v*10)/10, 'f', 1, 64)
	}
	if len(s) > 2 && s[len(s)-2:] == ".0" {
		s = s[:len(s)-2]
	}
	return s
}

// DaysSince returns whole days between t and now (never negative).
func DaysSince(t, now time.Time) int {
	d := now.Sub(t)
	if d < 0 {
		return 0
	}
	return int(d.Hours() / 24)
}

// Ago formats a time relative to now: "just now", "12 min ago", "3 h ago", "143 days ago".
func Ago(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	}
	days := int(d.Hours() / 24)
	if days == 1 {
		return "1 day ago"
	}
	return fmt.Sprintf("%d days ago", days)
}

// Duration formats a length of time compactly: "1 h 52 min", "12 min", "40 s".
func Duration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d s", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if m == 0 {
		return fmt.Sprintf("%d h", h)
	}
	return fmt.Sprintf("%d h %d min", h, m)
}

// Percent formats a ratio (0.38) as "38%".
func Percent(ratio float64) string {
	return strconv.Itoa(int(math.Round(ratio*100))) + "%"
}
