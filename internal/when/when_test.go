package when

import (
	"testing"
	"time"
)

func TestNowFormat(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Lisbon")
	got := Now(time.Date(2026, 9, 4, 10, 7, 0, 0, loc))
	if got != "Friday 4 September 2026, 10:07 (Europe/Lisbon, UTC+01:00)" {
		t.Fatalf("%q", got)
	}
}

func TestUserLocationFromTZ(t *testing.T) {
	t.Setenv("TZ", "America/New_York")
	if got := UserLocation().String(); got != "America/New_York" {
		t.Fatalf("%q", got)
	}
	t.Setenv("TZ", "Not/AZone")
	if got := UserLocation().String(); got == "Not/AZone" {
		t.Fatal("bad TZ accepted")
	}
}

func TestParseForms(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Lisbon")
	w, ok := Parse("2026-09-10", loc)
	if !ok || !w.AllDay || w.Time.Location() != loc {
		t.Fatalf("%+v %v", w, ok)
	}
	w, ok = Parse("2026-09-10 15:00", loc)
	if !ok || w.AllDay || w.Time.Hour() != 15 {
		t.Fatalf("%+v %v", w, ok)
	}
	if _, ok := Parse("next week", loc); ok {
		t.Fatal("garbage parsed")
	}
}

func TestFutureAcrossDST(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Lisbon")
	// Clocks go back 25 Oct 2026. An all-day deadline on the 25th is still
	// future at 23:59 on the 24th and on the 25th itself, past on the 26th.
	d := When{Time: time.Date(2026, 10, 25, 0, 0, 0, 0, loc), AllDay: true}
	if !d.Future(time.Date(2026, 10, 24, 23, 59, 0, 0, loc)) || !d.Future(time.Date(2026, 10, 25, 23, 59, 0, 0, loc)) {
		t.Fatal("should be future")
	}
	if d.Future(time.Date(2026, 10, 26, 0, 1, 0, 0, loc)) {
		t.Fatal("should be past")
	}
}

func TestAllDayKeepsDateInOtherZone(t *testing.T) {
	lisbon, _ := time.LoadLocation("Europe/Lisbon")
	ny, _ := time.LoadLocation("America/New_York")
	d := When{Time: time.Date(2026, 9, 10, 0, 0, 0, 0, lisbon), AllDay: true}
	// Viewed from New York late on the 10th, it is still due "today".
	if !d.Future(time.Date(2026, 9, 10, 22, 0, 0, 0, ny)) {
		t.Fatal("all-day date shifted across zones")
	}
}
