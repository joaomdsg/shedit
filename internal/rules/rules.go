// Package rules holds the hard rules that run before the sorter and override
// it: an explicit pile hint from the user always wins; a confidently parsed
// date always means deadline.
package rules

import (
	"regexp"
	"strings"
	"time"

	"github.com/joaomdsg/shedit/internal/when"
)

type When = when.When

var hintRe = regexp.MustCompile(`(?i)(^|\s)#(2min|deadline|eventually|unsorted)\b`)

func PileHint(text string) string {
	m := hintRe.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	return strings.ToLower(m[2])
}

// StripHint removes the hint token and the whitespace that preceded it,
// leaving the rest of the text (including line breaks) untouched.
func StripHint(text string) string {
	loc := hintRe.FindStringIndex(text)
	if loc == nil {
		return text
	}
	return strings.TrimSpace(text[:loc[0]]+text[loc[1]:]) + trailingNewline(text)
}

func trailingNewline(s string) string {
	if strings.HasSuffix(s, "\n") {
		return "\n"
	}
	return ""
}

type Input struct {
	Text  string
	Dates []When
}

type Decision struct {
	Pile string
	// Deadline is set whenever a confident date exists, even when the pile
	// was decided by a hint or the date has passed. Overdue is still a fact.
	Deadline *When
	Reason   string
}

func (d Decision) Decided() bool { return d.Pile != "" }

// Apply evaluates the hard rules. The nearest future date wins; failing
// that, the most recent past one.
func Apply(in Input, now time.Time) Decision {
	var d Decision
	d.Deadline = pick(in.Dates, now)
	if hint := PileHint(in.Text); hint != "" {
		d.Pile, d.Reason = hint, "you said #"+hint
		return d
	}
	if d.Deadline != nil {
		d.Pile = "deadline"
		if d.Deadline.Future(now) {
			d.Reason = "due " + d.Deadline.Local()
		} else {
			d.Reason = "was due " + d.Deadline.Local()
		}
	}
	return d
}

func pick(dates []When, now time.Time) *When {
	var future, past *When
	for i := range dates {
		d := &dates[i]
		switch {
		case d.Future(now) && (future == nil || d.Before(*future)):
			future = d
		case !d.Future(now) && (past == nil || past.Before(*d)):
			past = d
		}
	}
	if future != nil {
		return future
	}
	return past
}
