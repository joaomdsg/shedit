// Package core is the daemon's brain: ingest, the extract→rules→sort
// pipeline, every user action, and board snapshots pushed to subscribers.
// It has no I/O surface of its own; package rpc exposes it.
package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/joaomdsg/shedit/internal/blob"
	"github.com/joaomdsg/shedit/internal/extract"
	"github.com/joaomdsg/shedit/internal/model"
	"github.com/joaomdsg/shedit/internal/rules"
	"github.com/joaomdsg/shedit/internal/sorter"
	"github.com/joaomdsg/shedit/internal/store"
	"github.com/joaomdsg/shedit/internal/when"
)

var ErrNotFound = store.ErrNotFound

const errInterrupted = "interrupted before sorting finished; retry"

type Options struct {
	ExtractModel string
	SortModel    string
	// Now returns the current time in the user's zone. Prompts, rules, and
	// date parsing all take their location from it.
	Now    func() time.Time
	Logger *log.Logger
	// Sync runs the pipeline inline instead of in the background. Tests use
	// it; the daemon does not.
	Sync bool
	// Corrections is how many recent user moves the sorter sees.
	Corrections int
}

type Service struct {
	store *store.Store
	blob  *blob.Store
	model model.Runner
	opts  Options

	// ctx is cancelled by Close so in-flight model calls stop promptly.
	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	subs    map[chan Board]struct{}
	runs    map[string]*run
	wg      sync.WaitGroup
	costUSD float64
}

// run tracks one item's pipeline. A second request while running does not
// start a parallel pipeline; it asks the current one to go again, so the
// latest attachments always get the last word and versions never collide.
type run struct {
	again bool
}

func New(st *store.Store, bl *blob.Store, runner model.Runner, opts Options) *Service {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = log.New(io.Discard, "", 0)
	}
	if opts.Corrections == 0 {
		opts.Corrections = 20
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{store: st, blob: bl, model: runner, opts: opts, ctx: ctx, cancel: cancel,
		subs: map[chan Board]struct{}{}, runs: map[string]*run{}}
}

func (s *Service) now() time.Time           { return s.opts.Now() }
func (s *Service) Location() *time.Location { return s.now().Location() }
func (s *Service) CostUSD() float64         { s.mu.Lock(); defer s.mu.Unlock(); return s.costUSD }
func (s *Service) addCost(r model.Response) { s.mu.Lock(); s.costUSD += r.CostUSD; s.mu.Unlock() }
func (s *Service) isProcessing(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runs[id] != nil
}

func (s *Service) Wait() { s.wg.Wait() }

// Close stops background work: in-flight model calls are cancelled and the
// items they were sorting are marked interrupted so they show up as failed
// and retryable rather than quietly stuck.
func (s *Service) Close() {
	s.cancel()
	s.wg.Wait()
}

// Recover marks items the previous daemon left mid-pipeline. Call once at
// startup, before serving.
func (s *Service) Recover() error {
	items, err := s.store.ListItems(store.StatusOpen)
	if err != nil {
		return err
	}
	for _, it := range items {
		if it.Error != "" {
			continue
		}
		if _, err := s.store.LatestExtraction(it.ID); errors.Is(err, store.ErrNotFound) {
			if err := s.store.SetError(it.ID, errInterrupted); err != nil {
				return err
			}
		}
	}
	return nil
}

// Subscribe delivers a snapshot now and after every change. A slow reader
// only ever sees the latest one.
func (s *Service) Subscribe() (<-chan Board, func(), error) {
	b, err := s.Snapshot()
	if err != nil {
		return nil, nil, err
	}
	ch := make(chan Board, 1)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	ch <- b
	s.mu.Unlock()
	return ch, func() {
		s.mu.Lock()
		delete(s.subs, ch)
		s.mu.Unlock()
	}, nil
}

func (s *Service) publish() {
	b, err := s.Snapshot()
	if err != nil {
		s.opts.Logger.Printf("snapshot: %v", err)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.subs {
		select {
		case <-ch:
		default:
		}
		ch <- b
	}
}

type FileInput struct {
	Name   string
	Reader io.Reader
	Kind   string
}

type DumpInput struct {
	Text  string
	Files []FileInput
}

func isURL(s string) bool {
	if strings.ContainsAny(s, " \n\t") {
		return false
	}
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func kindOf(name, override string) string {
	if override != "" {
		return override
	}
	n := strings.ToLower(name)
	for _, ext := range []string{".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".heic"} {
		if strings.HasSuffix(n, ext) {
			return store.KindImage
		}
	}
	return store.KindFile
}

// Dump ingests one dump as exactly one item. Bytes reach disk before the
// item is visible; if any save fails, nothing is left behind.
func (s *Service) Dump(ctx context.Context, in DumpInput) (string, error) {
	if strings.TrimSpace(in.Text) == "" && len(in.Files) == 0 {
		return "", errors.New("empty dump")
	}
	it, err := s.store.CreateItem()
	if err != nil {
		return "", err
	}
	if err := s.attach(it.ID, in); err != nil {
		s.blob.RemoveItem(it.ID)
		s.store.DeleteItem(it.ID)
		return "", err
	}
	s.publish()
	s.schedule(it.ID)
	return it.ID, nil
}

func (s *Service) attach(itemID string, in DumpInput) error {
	if strings.TrimSpace(in.Text) != "" {
		kind, name := store.KindText, "text.txt"
		if isURL(strings.TrimSpace(in.Text)) {
			kind, name = store.KindURL, "link.txt"
		}
		if err := s.saveAttachment(itemID, kind, name, strings.NewReader(in.Text)); err != nil {
			return err
		}
	}
	for _, f := range in.Files {
		if err := s.saveAttachment(itemID, kindOf(f.Name, f.Kind), f.Name, f.Reader); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) saveAttachment(itemID, kind, name string, r io.Reader) error {
	saved, err := s.blob.Save(itemID, name, r)
	if err != nil {
		return err
	}
	_, err = s.store.AddAttachment(itemID, store.Attachment{Kind: kind, Name: name, Path: saved.Rel, SHA256: saved.SHA256, Size: saved.Size})
	return err
}

func (s *Service) schedule(id string) {
	s.mu.Lock()
	if r := s.runs[id]; r != nil {
		r.again = true
		s.mu.Unlock()
		return
	}
	s.runs[id] = &run{}
	s.mu.Unlock()

	if s.opts.Sync {
		s.runLoop(id)
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.runLoop(id)
	}()
}

func (s *Service) runLoop(id string) {
	for {
		s.process(id)
		s.mu.Lock()
		r := s.runs[id]
		if !r.again {
			delete(s.runs, id)
			s.mu.Unlock()
			s.publish()
			return
		}
		r.again = false
		s.mu.Unlock()
	}
}

func (s *Service) process(id string) {
	err := s.runPipeline(s.ctx, id)
	msg := ""
	switch {
	case err == nil:
	case s.ctx.Err() != nil:
		msg = errInterrupted
	default:
		s.opts.Logger.Printf("item %s: %v", id, err)
		msg = err.Error()
	}
	if err := s.store.SetError(id, msg); err != nil {
		s.opts.Logger.Printf("item %s: record error: %v", id, err)
	}
}

func (s *Service) runPipeline(ctx context.Context, id string) error {
	atts, err := s.store.ListAttachments(id)
	if err != nil {
		return err
	}
	if len(atts) == 0 {
		return errors.New("no attachments")
	}
	userText, eatts, err := s.extractorInputs(atts)
	if err != nil {
		return err
	}

	res, resp, err := extract.Run(ctx, s.model, s.opts.ExtractModel, eatts, s.now())
	s.addCost(resp)
	if err != nil {
		return fmt.Errorf("extract: %w", err)
	}
	if _, err := s.store.AddExtraction(id, store.Extraction{Model: resp.Model, JSON: string(resp.Output)}); err != nil {
		return err
	}
	s.publish()

	dec := rules.Apply(rules.Input{Text: userText, Dates: res.ParsedDates(s.Location())}, s.now())

	in, err := s.sorterInput(id, res, rules.StripHint(userText))
	if err != nil {
		return err
	}
	sr, sresp, err := sorter.Run(ctx, s.model, s.opts.SortModel, in, s.now())
	s.addCost(sresp)
	if err != nil {
		if !dec.Decided() {
			return fmt.Errorf("sort: %w", err)
		}
		// The rules already placed it; losing estimate and tags is not
		// worth failing the item over.
		s.opts.Logger.Printf("item %s: sorter: %v", id, err)
		return s.placeByRules(id, dec)
	}
	if err := s.store.SetSorting(id, store.Sorting{Estimate: sr.Estimate, Tags: sr.Tags, Priority: sr.Priority}); err != nil {
		return err
	}
	if sr.RelatedID != "" {
		if err := s.store.ProposeRelation(id, sr.RelatedID, sr.RelatedReason); err != nil {
			return err
		}
	}
	pile, reason := choosePile(sr, dec)
	return s.place(id, pile, reason, dec.Deadline, store.ActorSystem)
}

func (s *Service) placeByRules(id string, dec rules.Decision) error {
	it, err := s.store.GetItem(id)
	if err != nil {
		return err
	}
	deadline := it.Deadline
	if dec.Deadline != nil {
		deadline = &dec.Deadline.Time
	}
	if err := s.store.SetPriority(id, store.PriorityHeuristic(deadline, it.Estimate, s.now())); err != nil {
		return err
	}
	return s.place(id, dec.Pile, dec.Reason, dec.Deadline, store.ActorSystem)
}

func choosePile(sr sorter.Result, dec rules.Decision) (pile, reason string) {
	switch {
	case dec.Decided():
		return dec.Pile, dec.Reason
	case sr.Pile == store.PileDeadline && dec.Deadline == nil:
		// The sorter may not invent a deadline the extractor did not find.
		return store.PileUnsorted, "sorter wanted deadline but found no date: " + sr.Reason
	}
	return sr.Pile, sr.Reason
}

// place is the one place the system moves an item. It keeps the invariant
// "in deadline ⇒ has a date" and never overrides a pile the user chose:
// a re-extraction after the user has moved something only refreshes what
// the model knows, it does not re-sort.
func (s *Service) place(id, pile, reason string, deadline *when.When, actor string) error {
	it, err := s.store.GetItem(id)
	if err != nil {
		return err
	}
	if deadline != nil {
		if err := s.store.UpdateItem(id, store.ItemUpdate{Deadline: &deadline.Time, AllDay: &deadline.AllDay}); err != nil {
			return err
		}
	} else if it.Deadline == nil && pile == store.PileDeadline {
		pile, reason = store.PileUnsorted, "no date found: "+reason
	}
	if actor == store.ActorSystem && userPlaced(s.store, id) {
		return nil
	}
	if it.Pile == pile && it.Reason == reason {
		return nil
	}
	_, err = s.store.Move(id, pile, actor, reason)
	return err
}

func userPlaced(st *store.Store, id string) bool {
	moves, err := st.ListMoves(id)
	if err != nil || len(moves) == 0 {
		return false
	}
	return moves[len(moves)-1].Actor == store.ActorUser
}

func (s *Service) extractorInputs(atts []store.Attachment) (userText string, out []extract.Attachment, err error) {
	for _, a := range atts {
		ea := extract.Attachment{Kind: a.Kind, Name: a.Name, Path: s.blob.Abs(a.Path)}
		if a.Kind == store.KindText || a.Kind == store.KindURL {
			content, err := s.readBlob(a.Path)
			if err != nil {
				return "", nil, err
			}
			ea.Text = content
			if a.Kind == store.KindText {
				userText = content
				ea.Text = rules.StripHint(content)
			}
		}
		out = append(out, ea)
	}
	return userText, out, nil
}

func (s *Service) readBlob(rel string) (string, error) {
	f, err := s.blob.Open(rel)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	return string(b), err
}

func (s *Service) sorterInput(id string, res extract.Result, userText string) (sorter.Input, error) {
	in := sorter.Input{ItemID: id, Extraction: res, UserText: userText}
	items, err := s.store.ListItems(store.StatusOpen)
	if err != nil {
		return in, err
	}
	titles := map[string]string{}
	for _, it := range items {
		bi, err := s.buildItem(it)
		if err != nil {
			return in, err
		}
		titles[it.ID] = bi.Title
		if it.ID != id {
			in.Board = append(in.Board, sorter.BoardItem{ID: it.ID, Pile: it.Pile, Title: bi.Title, Deadline: bi.DeadlineLocal})
		}
	}
	moves, err := s.store.RecentUserMoves(s.opts.Corrections)
	if err != nil {
		return in, err
	}
	for _, m := range moves {
		title, ok := titles[m.ItemID]
		if !ok {
			title = s.titleOf(m.ItemID)
		}
		in.Corrections = append(in.Corrections, sorter.Correction{Title: title, From: m.From, To: m.To, Reason: m.Reason})
	}
	return in, nil
}

func (s *Service) titleOf(id string) string {
	it, err := s.store.GetItem(id)
	if err != nil {
		return ""
	}
	bi, err := s.buildItem(it)
	if err != nil {
		return ""
	}
	return bi.Title
}

// Move re-piles an item by hand and returns the move id so a reason can be
// attached afterwards. Leaving deadline drops the date, so the invariant
// holds in both directions for user moves.
func (s *Service) Move(id, pile, reason string) (string, error) {
	it, err := s.store.GetItem(id)
	if err != nil {
		return "", err
	}
	if pile == store.PileDeadline && it.Deadline == nil {
		return "", errors.New("set a date first: shedit deadline <id> <date>")
	}
	if pile != store.PileDeadline && it.Deadline != nil {
		if err := s.store.UpdateItem(id, store.ItemUpdate{ClearDeadline: true}); err != nil {
			return "", err
		}
	}
	m, err := s.store.Move(id, pile, store.ActorUser, reason)
	if err != nil {
		return "", err
	}
	s.publish()
	return m.ID, nil
}

func (s *Service) SetMoveReason(moveID, reason string) error {
	return s.then(s.store.SetMoveReason(moveID, reason))
}

func (s *Service) Done(id string) error { return s.then(s.store.SetStatus(id, store.StatusDone)) }
func (s *Service) Archive(id string) error {
	return s.then(s.store.SetStatus(id, store.StatusArchived))
}
func (s *Service) Reopen(id string) error { return s.then(s.store.SetStatus(id, store.StatusOpen)) }

func (s *Service) then(err error) error {
	if err != nil {
		return err
	}
	s.publish()
	return nil
}

type Edit struct {
	Title   *string
	Summary *string
}

func (s *Service) Edit(id string, e Edit) error {
	return s.then(s.store.UpdateItem(id, store.ItemUpdate{Title: e.Title, Summary: e.Summary}))
}

func (s *Service) SetDeadline(id string, d when.When) error {
	return s.then(s.place(id, store.PileDeadline, "you set a date", &d, store.ActorUser))
}

func (s *Service) ClearDeadline(id string) error {
	it, err := s.store.GetItem(id)
	if err != nil {
		return err
	}
	if err := s.store.UpdateItem(id, store.ItemUpdate{ClearDeadline: true}); err != nil {
		return err
	}
	if it.Pile == store.PileDeadline {
		if _, err := s.store.Move(id, store.PileEventually, store.ActorUser, "you removed the date"); err != nil {
			return err
		}
	}
	s.publish()
	return nil
}

// Attach adds raw inputs to an item and re-extracts. All files are saved
// before any is recorded, so a failure part-way leaves the item unchanged.
func (s *Service) Attach(ctx context.Context, id string, files []FileInput) error {
	if _, err := s.store.GetItem(id); err != nil {
		return err
	}
	if len(files) == 0 {
		return errors.New("nothing to attach")
	}
	type pending struct {
		saved blob.Saved
		kind  string
		name  string
	}
	var saved []pending
	for _, f := range files {
		sv, err := s.blob.Save(id, f.Name, f.Reader)
		if err != nil {
			for _, p := range saved {
				s.blob.Remove(p.saved.Rel)
			}
			return err
		}
		saved = append(saved, pending{sv, kindOf(f.Name, f.Kind), f.Name})
	}
	for _, p := range saved {
		if _, err := s.store.AddAttachment(id, store.Attachment{Kind: p.kind, Name: p.name, Path: p.saved.Rel, SHA256: p.saved.SHA256, Size: p.saved.Size}); err != nil {
			return err
		}
	}
	s.publish()
	s.schedule(id)
	return nil
}

// Detach removes one attachment and re-extracts. Refuses to leave an item
// empty. The record goes first: an orphan file is recoverable, an orphan
// record is a lie on the board.
func (s *Service) Detach(ctx context.Context, attID string) error {
	a, err := s.store.GetAttachment(attID)
	if err != nil {
		return err
	}
	atts, err := s.store.ListAttachments(a.ItemID)
	if err != nil {
		return err
	}
	if len(atts) <= 1 {
		return errors.New("an item needs at least one attachment; delete the item instead")
	}
	if err := s.store.RemoveAttachment(attID); err != nil {
		return err
	}
	if err := s.blob.Remove(a.Path); err != nil {
		s.opts.Logger.Printf("attachment %s: file left behind: %v", attID, err)
	}
	s.publish()
	s.schedule(a.ItemID)
	return nil
}

func (s *Service) Retry(ctx context.Context, id string) error {
	if _, err := s.store.GetItem(id); err != nil {
		return err
	}
	if err := s.store.SetError(id, ""); err != nil {
		return err
	}
	s.publish()
	s.schedule(id)
	return nil
}

// Delete removes an item and everything it holds. Only a human calls this.
func (s *Service) Delete(id string) error {
	if err := s.store.DeleteItem(id); err != nil {
		return err
	}
	if err := s.blob.RemoveItem(id); err != nil {
		return fmt.Errorf("item removed but its files were not: %w", err)
	}
	s.publish()
	return nil
}
