// Package when models a date as wall-clock time in the user's zone,
// optionally all-day. All-day dates compare and render by calendar date so
// they never shift across zones or DST.
package when

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Kind says what a date means to the user. Only KindDue makes a deadline.
type Kind string

const (
	KindDue       Kind = "due"
	KindEvent     Kind = "event"
	KindMentioned Kind = "mentioned"
)

type When struct {
	Time   time.Time
	AllDay bool
	Kind   Kind
}

func (w When) Local() string {
	if w.AllDay {
		return w.Time.Format("Mon 2 Jan")
	}
	return w.Time.Format("Mon 2 Jan 15:04")
}

// Future treats an all-day date by its captured calendar date, never by the
// instant of its midnight, so it stays "today" in any zone and through DST.
func (w When) Future(now time.Time) bool {
	if w.AllDay {
		y1, m1, d1 := w.Time.Date()
		y2, m2, d2 := now.Date()
		return y1 > y2 || (y1 == y2 && (m1 > m2 || (m1 == m2 && d1 >= d2)))
	}
	return w.Time.After(now)
}

func (w When) Before(o When) bool { return w.Time.Before(o.Time) }

var LocalLayouts = []string{"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04"}

// Parse reads an ISO date. A bare date is all-day. Zone-less values are in
// loc; an explicit offset is converted to loc.
func Parse(s string, loc *time.Location) (When, bool) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return When{Time: t.In(loc)}, true
	}
	if t, err := time.ParseInLocation("2006-01-02", s, loc); err == nil {
		return When{Time: t, AllDay: true}, true
	}
	for _, layout := range LocalLayouts {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return When{Time: t}, true
		}
	}
	return When{}, false
}

// Now formats the current moment for a prompt:
// "Friday 4 September 2026, 10:07 (Europe/Lisbon, UTC+01:00)".
func Now(now time.Time) string {
	return now.Format("Monday 2 January 2006, 15:04") + " (" + now.Location().String() + ", UTC" + now.Format("-07:00") + ")"
}

// UserLocation resolves the user's zone to a named location. time.Local
// prints as "Local", which tells the model nothing, so the name is read
// from TZ or the /etc/localtime symlink instead.
func UserLocation() *time.Location {
	if tz := os.Getenv("TZ"); tz != "" {
		if loc, err := time.LoadLocation(tz); err == nil {
			return loc
		}
	}
	if target, err := filepath.EvalSymlinks("/etc/localtime"); err == nil {
		if i := strings.Index(target, "zoneinfo/"); i >= 0 {
			if loc, err := time.LoadLocation(target[i+len("zoneinfo/"):]); err == nil {
				return loc
			}
		}
	}
	return time.Local
}
