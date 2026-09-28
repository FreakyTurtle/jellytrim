package timetable

import (
	"strings"
	"testing"
	"time"
)

func TestParseAndString(t *testing.T) {
	w, err := Parse("")
	if err != nil || !w.Full() {
		t.Fatal("empty should be always")
	}
	s := strings.Repeat("0", Slots)
	s = s[:3] + "1" + s[4:] // Monday 03:00
	w, err = Parse(s)
	if err != nil || !w[0][3] || w.ActiveHours() != 1 || w.String() != s {
		t.Fatalf("%v %v", w.ActiveHours(), err)
	}
	for _, bad := range []string{"01", strings.Repeat("2", Slots), strings.Repeat("1", Slots+1)} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestActiveUsesMondayFirstAndLocalHour(t *testing.T) {
	var w Week
	w[6][23] = true                                       // Sunday 23:00
	sun := time.Date(2026, 9, 27, 23, 30, 0, 0, time.UTC) // a Sunday
	if !w.Active(sun) || w.Active(sun.Add(time.Hour)) {
		t.Fatal("Sunday 23:00 block")
	}
	london, _ := time.LoadLocation("Europe/London")
	w = Week{}
	w[0][1] = true // Monday 01:00 local
	mon := time.Date(2026, 9, 28, 1, 15, 0, 0, london)
	if !w.Active(mon) || w.Active(mon.UTC()) {
		t.Fatal("hours are read in the time's own zone")
	}
}

func TestNextChange(t *testing.T) {
	w := Presets()[1].Week // nights 01 to 07
	at := time.Date(2026, 9, 28, 12, 20, 0, 0, time.UTC)
	next, ok := w.NextChange(at)
	if !ok || !next.Equal(time.Date(2026, 9, 29, 1, 0, 0, 0, time.UTC)) {
		t.Fatalf("next start %v", next)
	}
	next, ok = w.NextChange(time.Date(2026, 9, 29, 3, 0, 0, 0, time.UTC))
	if !ok || !next.Equal(time.Date(2026, 9, 29, 7, 0, 0, 0, time.UTC)) {
		t.Fatalf("next stop %v", next)
	}
	if _, ok := Always().NextChange(at); ok {
		t.Fatal("always never changes")
	}
	if _, ok := (Week{}).NextChange(at); ok {
		t.Fatal("empty never changes")
	}
}

func TestNextChangeAcrossDST(t *testing.T) {
	london, _ := time.LoadLocation("Europe/London")
	w := Presets()[1].Week
	// Clocks go back on Sunday 25 October 2026 at 02:00.
	at := time.Date(2026, 10, 24, 22, 0, 0, 0, london)
	next, ok := w.NextChange(at)
	if !ok || next.Hour() != 1 || next.Day() != 25 {
		t.Fatalf("next %v", next)
	}
	end, _ := w.NextChange(next)
	if end.Hour() != 7 || end.Day() != 25 {
		t.Fatalf("end %v", end)
	}
}

func TestFromWindow(t *testing.T) {
	if !FromWindow("", "").Full() || !FromWindow("bad", "07:00").Full() {
		t.Fatal("no window means always")
	}
	w := FromWindow("01:00", "06:30")
	if w[2][0] || !w[2][1] || !w[2][6] || w[2][7] || w.ActiveHours() != 6*7 {
		t.Fatalf("window: %d hours", w.ActiveHours())
	}
	wrap := FromWindow("22:00", "06:00")
	if !wrap[0][23] || !wrap[0][5] || wrap[0][6] || wrap[0][21] {
		t.Fatal("wrapping window")
	}
}

func TestDescribe(t *testing.T) {
	if Describe(Always()) != "any time" || Describe(Presets()[1].Week) != "nights (01:00 to 07:00)" {
		t.Fatal(Describe(Presets()[1].Week))
	}
	var w Week
	w[0][0] = true
	if Describe(w) != "1 hour a week" {
		t.Fatal(Describe(w))
	}
}
