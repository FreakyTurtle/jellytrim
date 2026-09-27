package units

import (
	"testing"
	"time"
)

func TestBytes(t *testing.T) {
	cases := map[int64]string{
		0: "0 B", 999: "999 B", 1000: "1 kB", 1500: "1.5 kB",
		620_000_000: "620 MB", 13_700_000_000: "13.7 GB", 48_200_000_000: "48.2 GB",
		4_800_000_000_000: "4.8 TB", 1_000_000_000: "1 GB", 150_400_000_000: "150 GB",
	}
	for in, want := range cases {
		if got := Bytes(in); got != want {
			t.Errorf("Bytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestBitrate(t *testing.T) {
	cases := map[int64]string{0: "Unknown", 128_000: "128 kbps", 48_200_000: "48.2 Mbps", 8_000_000: "8 Mbps"}
	for in, want := range cases {
		if got := Bitrate(in); got != want {
			t.Errorf("Bitrate(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestDaysAndAgo(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	if d := DaysSince(now.Add(-143*24*time.Hour-time.Hour), now); d != 143 {
		t.Errorf("DaysSince = %d", d)
	}
	if d := DaysSince(now.Add(time.Hour), now); d != 0 {
		t.Errorf("future DaysSince = %d", d)
	}
	cases := map[time.Duration]string{
		10 * time.Second: "just now", 12 * time.Minute: "12 min ago", 3 * time.Hour: "3 h ago",
		30 * time.Hour: "30 h ago", 24 * 143 * time.Hour: "143 days ago",
	}
	for d, want := range cases {
		if got := Ago(now.Add(-d), now); got != want {
			t.Errorf("Ago(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestDuration(t *testing.T) {
	cases := map[time.Duration]string{40 * time.Second: "40 s", 12 * time.Minute: "12 min", 112 * time.Minute: "1 h 52 min", 2 * time.Hour: "2 h"}
	for in, want := range cases {
		if got := Duration(in); got != want {
			t.Errorf("Duration(%v) = %q, want %q", in, got, want)
		}
	}
	if Percent(0.384) != "38%" {
		t.Error("Percent")
	}
}
