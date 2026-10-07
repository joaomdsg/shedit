package rules

import (
	"testing"
	"time"

	"github.com/joaomdsg/shedit/internal/when"
)

var lisbon = mustLoad("Europe/Lisbon")
var now = time.Date(2026, 9, 4, 10, 0, 0, 0, lisbon)

func mustLoad(name string) *time.Location {
	l, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return l
}

func timed(t time.Time) When  { return When{Time: t, Kind: when.KindDue} }
func allDay(t time.Time) When { return When{Time: t, AllDay: true, Kind: when.KindDue} }
func day(d int) time.Time     { return time.Date(2026, 9, d, 0, 0, 0, 0, lisbon) }

func TestPileHint(t *testing.T) {
	cases := map[string]string{
		"pay rent #deadline":         "deadline",
		"#2min reply to Ana":         "2min",
		"read that book #eventually": "eventually",
		"#unsorted whatever":         "unsorted",
		"no hint here":               "",
		"hashtag #golang is fine":    "",
		"upper #2MIN works":          "2min",
		"https://x.com/#2min":        "",
		"#2minutes is not a hint":    "",
	}
	for in, want := range cases {
		if got := PileHint(in); got != want {
			t.Errorf("PileHint(%q)=%q want %q", in, got, want)
		}
	}
}

func TestStripHintKeepsEverythingElse(t *testing.T) {
	cases := map[string]string{
		"pay rent #deadline now":          "pay rent now",
		"#golang rocks":                   "#golang rocks",
		"line one\nline two #2min\n":      "line one\nline two\n",
		"#eventually\n  indented\n\nlist": "indented\n\nlist",
	}
	for in, want := range cases {
		if got := StripHint(in); got != want {
			t.Errorf("StripHint(%q)=%q want %q", in, got, want)
		}
	}
}

func TestHintWinsButDateIsKept(t *testing.T) {
	d := Apply(Input{Text: "call mom #eventually", Dates: []When{allDay(day(7))}}, now)
	if d.Pile != "eventually" || !d.Hint || d.Deadline == nil || d.Deadline.Time.Day() != 7 {
		t.Fatalf("%+v", d)
	}
}

func TestFutureDateMeansDeadline(t *testing.T) {
	future := now.AddDate(0, 0, 5)
	d := Apply(Input{Dates: []When{timed(future)}}, now)
	if d.Pile != "deadline" || d.Deadline == nil || !d.Deadline.Time.Equal(future) || d.Deadline.AllDay {
		t.Fatalf("%+v", d)
	}
	if d.Reason != "due Wed 9 Sep 10:00" {
		t.Fatalf("reason %q", d.Reason)
	}
}

func TestNearestFutureDateWins(t *testing.T) {
	d := Apply(Input{Dates: []When{allDay(day(13)), allDay(day(6)), allDay(day(1))}}, now)
	if d.Deadline.Time.Day() != 6 {
		t.Fatalf("%+v", d)
	}
}

func TestPastDateIsStillADeadlineMarkedOverdue(t *testing.T) {
	d := Apply(Input{Dates: []When{allDay(day(1)), allDay(day(3))}}, now)
	if d.Pile != "deadline" || d.Deadline.Time.Day() != 3 || d.Reason != "was due Thu 3 Sep" {
		t.Fatalf("%+v", d)
	}
}

func TestAllDayTodayIsDue(t *testing.T) {
	d := Apply(Input{Dates: []When{allDay(day(4))}}, now)
	if d.Pile != "deadline" || d.Reason != "due Fri 4 Sep" {
		t.Fatalf("%+v", d)
	}
	late := time.Date(2026, 9, 4, 23, 30, 0, 0, lisbon)
	if d := Apply(Input{Dates: []When{allDay(day(4))}}, late); d.Reason != "due Fri 4 Sep" {
		t.Fatalf("late evening: %+v", d)
	}
}

func TestNothingLeavesItToSorter(t *testing.T) {
	if d := Apply(Input{Text: "hmm"}, now); d.Decided() || d.Deadline != nil {
		t.Fatalf("%+v", d)
	}
}

func TestDatePileIsNotAHint(t *testing.T) {
	if d := Apply(Input{Dates: []When{allDay(day(7))}}, now); d.Pile != "deadline" || d.Hint {
		t.Fatalf("%+v", d)
	}
}

// A send date in the body is a fact about the email, not something owed.
func TestMentionedDateNeverBecomesDeadline(t *testing.T) {
	sent := When{Time: day(1), AllDay: true, Kind: when.KindMentioned}
	if d := Apply(Input{Dates: []When{sent}}, now); d.Decided() || d.Deadline != nil {
		t.Fatalf("%+v", d)
	}
}

func TestEventDateNeverBecomesDeadline(t *testing.T) {
	future := When{Time: day(9), AllDay: true, Kind: when.KindEvent}
	if d := Apply(Input{Dates: []When{future}}, now); d.Decided() || d.Deadline != nil {
		t.Fatalf("%+v", d)
	}
}

func TestDueDateWinsOverMentionedAndEvent(t *testing.T) {
	due := When{Time: day(11), AllDay: true, Kind: when.KindDue}
	mentioned := When{Time: day(5), AllDay: true, Kind: when.KindMentioned}
	event := When{Time: day(6), AllDay: true, Kind: when.KindEvent}
	d := Apply(Input{Dates: []When{mentioned, event, due}}, now)
	if d.Pile != "deadline" || d.Deadline == nil || d.Deadline.Time.Day() != 11 {
		t.Fatalf("%+v", d)
	}
}
