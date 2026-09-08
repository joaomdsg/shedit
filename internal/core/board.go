package core

import (
	"encoding/json"
	"sort"
	"time"

	"github.com/joaomdsg/shedit/internal/extract"
	"github.com/joaomdsg/shedit/internal/store"
	"github.com/joaomdsg/shedit/internal/when"
)

// Board is the full snapshot the UI renders. The daemon pushes a fresh one on
// every change; clients never patch.
type Board struct {
	At     time.Time         `json:"at"`
	Counts Counts            `json:"counts"`
	Piles  map[string][]Item `json:"piles"`
	Done   []Item            `json:"done"`
	Failed []Item            `json:"failed"`
}

type Counts struct {
	TwoMin   int `json:"2min"`
	Deadline int `json:"deadline"`
	Other    int `json:"other"`
}

type Item struct {
	ID       string     `json:"id"`
	Status   string     `json:"status"`
	Pile     string     `json:"pile"`
	Title    string     `json:"title"`
	Summary  string     `json:"summary"`
	Reason   string     `json:"reason"`
	Deadline *time.Time `json:"deadline,omitempty"`
	AllDay   bool       `json:"all_day,omitempty"`
	// DeadlineLocal is preformatted so the UI never does zone math.
	DeadlineLocal string       `json:"deadline_local,omitempty"`
	Estimate      string       `json:"estimate,omitempty"`
	Tags          []string     `json:"tags"`
	Priority      int          `json:"priority"`
	Error         string       `json:"error,omitempty"`
	Processing    bool         `json:"processing"`
	Attachments   []Attachment `json:"attachments"`
	Relations     []Relation   `json:"relations,omitempty"`
	LastMove      *Move        `json:"last_move,omitempty"`
	Actions       []string     `json:"actions,omitempty"`
	CreatedAt     time.Time    `json:"created_at"`
	UpdatedAt     time.Time    `json:"updated_at"`
}

type Attachment struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Name string `json:"name"`
	Path string `json:"path"`
	Size int64  `json:"size"`
}

type Relation struct {
	OtherID string `json:"other_id"`
	Reason  string `json:"reason"`
}

type Move struct {
	ID     string    `json:"id"`
	From   string    `json:"from"`
	To     string    `json:"to"`
	Actor  string    `json:"actor"`
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}

func (s *Service) buildItem(it store.Item) (Item, error) {
	out := Item{
		ID: it.ID, Status: it.Status, Pile: it.Pile, Reason: it.Reason,
		Deadline: it.Deadline, AllDay: it.AllDay, Estimate: it.Estimate, Tags: it.Tags, Priority: it.Priority, Error: it.Error,
		CreatedAt: it.CreatedAt, UpdatedAt: it.UpdatedAt,
		Attachments: []Attachment{},
	}
	if out.Tags == nil {
		out.Tags = []string{}
	}
	if it.Deadline != nil {
		d := *it.Deadline
		if !it.AllDay {
			d = d.In(s.Location())
		}
		out.DeadlineLocal = when.When{Time: d, AllDay: it.AllDay}.Local()
	}
	if ex, err := s.store.LatestExtraction(it.ID); err == nil {
		var r extract.Result
		if json.Unmarshal([]byte(ex.JSON), &r) == nil {
			out.Title, out.Summary, out.Actions = r.Title, r.Summary, r.Actions
		}
	}
	if it.TitleOverride != "" {
		out.Title = it.TitleOverride
	}
	if it.SummaryOverride != "" {
		out.Summary = it.SummaryOverride
	}
	atts, err := s.store.ListAttachments(it.ID)
	if err != nil {
		return out, err
	}
	for _, a := range atts {
		out.Attachments = append(out.Attachments, Attachment{ID: a.ID, Kind: a.Kind, Name: a.Name, Path: s.blob.Abs(a.Path), Size: a.Size})
	}
	if out.Title == "" {
		out.Title = fallbackTitle(atts)
	}
	rels, err := s.store.ListRelations(it.ID)
	if err != nil {
		return out, err
	}
	for _, r := range rels {
		out.Relations = append(out.Relations, Relation{OtherID: r.OtherID, Reason: r.Reason})
	}
	moves, err := s.store.ListMoves(it.ID)
	if err != nil {
		return out, err
	}
	if n := len(moves); n > 0 {
		m := moves[n-1]
		out.LastMove = &Move{ID: m.ID, From: m.From, To: m.To, Actor: m.Actor, Reason: m.Reason, At: m.CreatedAt}
	}
	out.Processing = s.isProcessing(it.ID)
	return out, nil
}

func fallbackTitle(atts []store.Attachment) string {
	for _, a := range atts {
		if a.Name != "" && a.Kind != store.KindText {
			return a.Name
		}
	}
	if len(atts) > 0 {
		return atts[0].Name
	}
	return "(empty)"
}

func (s *Service) Snapshot() (Board, error) {
	b := Board{At: s.now(), Piles: map[string][]Item{}, Done: []Item{}, Failed: []Item{}}
	for _, p := range store.Piles {
		b.Piles[p] = []Item{}
	}
	items, err := s.store.ListItems("")
	if err != nil {
		return b, err
	}
	for _, it := range items {
		if it.Status == store.StatusArchived {
			continue
		}
		bi, err := s.buildItem(it)
		if err != nil {
			return b, err
		}
		if it.Status == store.StatusDone {
			b.Done = append(b.Done, bi)
			continue
		}
		if bi.Error != "" {
			b.Failed = append(b.Failed, bi)
		}
		b.Piles[it.Pile] = append(b.Piles[it.Pile], bi)
		switch it.Pile {
		case store.Pile2Min:
			b.Counts.TwoMin++
		case store.PileDeadline:
			b.Counts.Deadline++
		default:
			b.Counts.Other++
		}
	}
	// Every pile leads with its highest-priority items; ties keep the pile's
	// own order below.
	for _, p := range store.Piles {
		if p == store.PileDeadline {
			continue
		}
		pile := b.Piles[p]
		sort.SliceStable(pile, func(i, j int) bool { return pile[i].Priority > pile[j].Priority })
	}
	dl := b.Piles[store.PileDeadline]
	sort.SliceStable(dl, func(i, j int) bool {
		oi := dl[i].Deadline != nil && dl[i].Deadline.Before(b.At)
		oj := dl[j].Deadline != nil && dl[j].Deadline.Before(b.At)
		if oi != oj {
			// Overdue outranks everything else regardless of priority.
			return oi
		}
		if dl[i].Priority != dl[j].Priority {
			return dl[i].Priority > dl[j].Priority
		}
		return dl[i].Deadline.Before(*dl[j].Deadline)
	})
	sort.SliceStable(b.Done, func(i, j int) bool { return b.Done[i].UpdatedAt.After(b.Done[j].UpdatedAt) })
	return b, nil
}
