package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joaomdsg/shedit/internal/blob"
	"github.com/joaomdsg/shedit/internal/extract"
	"github.com/joaomdsg/shedit/internal/model"
	"github.com/joaomdsg/shedit/internal/sorter"
	"github.com/joaomdsg/shedit/internal/store"
	"github.com/joaomdsg/shedit/internal/when"
)

var lisbon = func() *time.Location { l, _ := time.LoadLocation("Europe/Lisbon"); return l }()
var now = time.Date(2026, 9, 4, 10, 0, 0, 0, lisbon)

type fixture struct {
	svc  *Service
	fake *model.Fake
	st   *store.Store
	bl   *blob.Store
	root string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "shedit.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	fake := &model.Fake{}
	bl := blob.New(root)
	svc := New(st, bl, fake, Options{Now: func() time.Time { return now }, Sync: true})
	return &fixture{svc: svc, fake: fake, st: st, bl: bl, root: root}
}

func ext(title string, dates ...extract.Date) extract.Result {
	return extract.Result{Title: title, Summary: "sum " + title, Dates: dates, SizeGuess: "small", Confidence: 0.9}
}

func srt(pile, reason string) sorter.Result {
	return sorter.Result{Pile: pile, Estimate: "10m", Tags: []string{"t"}, Reason: reason}
}

func TestDumpTextRunsBothStagesAndPlacesItem(t *testing.T) {
	f := newFixture(t)
	f.fake.Queue(ext("Call the plumber")).Queue(srt("2min", "quick call"))
	id, err := f.svc.Dump(context.Background(), DumpInput{Text: "call plumber about the leak\n"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := f.svc.Snapshot()
	if len(b.Piles["2min"]) != 1 || b.Counts.TwoMin != 1 {
		t.Fatalf("%+v", b.Counts)
	}
	it := b.Piles["2min"][0]
	if it.ID != id || it.Title != "Call the plumber" || it.Reason != "quick call" || it.Estimate != "10m" || it.Processing {
		t.Fatalf("%+v", it)
	}
	if it.LastMove == nil || it.LastMove.Actor != "system" {
		t.Fatalf("move not recorded: %+v", it.LastMove)
	}
	if len(f.fake.Requests) != 2 {
		t.Fatalf("want 2 model calls, got %d", len(f.fake.Requests))
	}
	if !strings.Contains(f.fake.Requests[0].Prompt, "call plumber about the leak") {
		t.Fatal("extractor did not get the text")
	}
	if !strings.Contains(f.fake.Requests[1].Prompt, "call plumber about the leak") {
		t.Fatal("sorter did not get the user's text")
	}
	if f.svc.CostUSD() <= 0 {
		t.Fatal("cost not tracked")
	}
	// Raw text is on disk verbatim.
	data, _ := os.ReadFile(it.Attachments[0].Path)
	if string(data) != "call plumber about the leak\n" || it.Attachments[0].Kind != "text" {
		t.Fatalf("raw not kept verbatim: %q %+v", data, it.Attachments[0])
	}
}

func TestDumpEmptyFails(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.Dump(context.Background(), DumpInput{Text: "   "}); err == nil {
		t.Fatal("want error")
	}
}

func TestDumpFilesBecomeOneItemWithAttachments(t *testing.T) {
	f := newFixture(t)
	f.fake.Queue(ext("Trip docs")).Queue(srt("eventually", "no date"))
	id, err := f.svc.Dump(context.Background(), DumpInput{
		Text:  "trip stuff",
		Files: []FileInput{{Name: "ticket.pdf", Reader: strings.NewReader("pdf")}, {Name: "map.png", Reader: strings.NewReader("png")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	atts, _ := f.st.ListAttachments(id)
	if len(atts) != 3 || atts[1].Kind != "file" || atts[2].Kind != "image" {
		t.Fatalf("%+v", atts)
	}
	req := f.fake.Requests[0]
	if len(req.Files) != 2 {
		t.Fatalf("extractor should see the two files, got %v", req.Files)
	}
	if len(f.fake.Requests[1].Files) != 0 {
		t.Fatal("sorter must never see files")
	}
}

func TestDumpURLAllowsWeb(t *testing.T) {
	f := newFixture(t)
	f.fake.Queue(ext("Article")).Queue(srt("eventually", "reading"))
	id, _ := f.svc.Dump(context.Background(), DumpInput{Text: "https://example.com/post"})
	atts, _ := f.st.ListAttachments(id)
	if atts[0].Kind != "url" {
		t.Fatalf("%+v", atts[0])
	}
	if !f.fake.Requests[0].AllowWeb {
		t.Fatal("extractor should be allowed to fetch")
	}
}

func TestPileHintWinsOverSorter(t *testing.T) {
	f := newFixture(t)
	f.fake.Queue(ext("Read book")).Queue(srt("2min", "sorter says now"))
	id, _ := f.svc.Dump(context.Background(), DumpInput{Text: "read the book #eventually"})
	it, _ := f.st.GetItem(id)
	if it.Pile != "eventually" || !strings.Contains(it.Reason, "#eventually") {
		t.Fatalf("%+v", it)
	}
	if it.Estimate != "10m" {
		t.Fatal("sorter estimate should still be kept")
	}
	for i, req := range f.fake.Requests {
		if strings.Contains(req.Prompt, "#eventually") {
			t.Fatalf("hint leaked into model call %d", i)
		}
	}
}

func TestConfidentFutureDateMeansDeadline(t *testing.T) {
	f := newFixture(t)
	f.fake.Queue(ext("Dentist", extract.Date{Date: "2026-09-10", Confident: true})).Queue(srt("eventually", "meh"))
	id, _ := f.svc.Dump(context.Background(), DumpInput{Text: "dentist on the 10th"})
	it, _ := f.st.GetItem(id)
	if it.Pile != "deadline" || it.Deadline == nil || it.Deadline.Day() != 10 || !it.AllDay {
		t.Fatalf("%+v", it)
	}
	b, _ := f.svc.Snapshot()
	if b.Counts.Deadline != 1 || b.Piles["deadline"][0].DeadlineLocal != "Thu 10 Sep" || b.Piles["deadline"][0].Reason != "due Thu 10 Sep" {
		t.Fatalf("%+v", b.Piles["deadline"][0])
	}
}

func TestTimedDateIsLocalAndDueTodayCounts(t *testing.T) {
	f := newFixture(t)
	f.fake.Queue(ext("Call", extract.Date{Date: "2026-09-04T15:00", Confident: true})).Queue(srt("2min", "meh"))
	id, _ := f.svc.Dump(context.Background(), DumpInput{Text: "call at 3"})
	it, _ := f.st.GetItem(id)
	if it.Pile != "deadline" || it.Deadline == nil || it.Deadline.In(lisbon).Hour() != 15 || it.AllDay {
		t.Fatalf("%+v", it)
	}
	b, _ := f.svc.Snapshot()
	if b.Piles["deadline"][0].DeadlineLocal != "Fri 4 Sep 15:00" {
		t.Fatalf("%q", b.Piles["deadline"][0].DeadlineLocal)
	}
	// An all-day date for today is still a deadline at 10:00.
	f.fake.Queue(ext("Today", extract.Date{Date: "2026-09-04", Confident: true})).Queue(srt("2min", "meh"))
	id, _ = f.svc.Dump(context.Background(), DumpInput{Text: "today"})
	it, _ = f.st.GetItem(id)
	if it.Pile != "deadline" {
		t.Fatalf("due today not a deadline: %+v", it)
	}
	// The sorter's board view shows local dates.
	if !strings.Contains(f.fake.Requests[3].Prompt, "| Fri 4 Sep 15:00") {
		t.Fatalf("board view lacks local deadline:\n%s", f.fake.Requests[3].Prompt)
	}
}

func TestUnconfidentDateLeftToSorter(t *testing.T) {
	f := newFixture(t)
	f.fake.Queue(ext("Old article", extract.Date{Date: "2026-09-10", Confident: false})).Queue(srt("eventually", "reading"))
	id, _ := f.svc.Dump(context.Background(), DumpInput{Text: "x"})
	it, _ := f.st.GetItem(id)
	if it.Pile != "eventually" || it.Deadline != nil {
		t.Fatalf("%+v", it)
	}
}

func TestExtractorFailureIsVisibleAndRetryable(t *testing.T) {
	f := newFixture(t)
	f.fake.QueueError(errors.New("model down"))
	id, err := f.svc.Dump(context.Background(), DumpInput{Text: "hello"})
	if err != nil {
		t.Fatal("dump itself must succeed; raw is safe")
	}
	b, _ := f.svc.Snapshot()
	if len(b.Failed) != 1 || b.Failed[0].ID != id || !strings.Contains(b.Failed[0].Error, "model down") {
		t.Fatalf("%+v", b.Failed)
	}
	if len(b.Piles["unsorted"]) != 1 {
		t.Fatal("failed item should still sit in unsorted")
	}
	f.fake.Queue(ext("Hello")).Queue(srt("2min", "ok"))
	if err := f.svc.Retry(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	b, _ = f.svc.Snapshot()
	if len(b.Failed) != 0 || len(b.Piles["2min"]) != 1 {
		t.Fatalf("retry failed: %+v", b)
	}
}

func TestSorterFailureWithRulesDecisionStillPlaces(t *testing.T) {
	f := newFixture(t)
	f.fake.Queue(ext("X")).QueueError(errors.New("sorter down"))
	id, _ := f.svc.Dump(context.Background(), DumpInput{Text: "x #2min"})
	it, _ := f.st.GetItem(id)
	if it.Pile != "2min" || it.Error != "" {
		t.Fatalf("%+v", it)
	}
}

func TestSorterFailureWithoutRulesIsAnError(t *testing.T) {
	f := newFixture(t)
	f.fake.Queue(ext("X")).QueueError(errors.New("sorter down"))
	id, _ := f.svc.Dump(context.Background(), DumpInput{Text: "x"})
	it, _ := f.st.GetItem(id)
	if it.Pile != "unsorted" || !strings.Contains(it.Error, "sorter down") {
		t.Fatalf("%+v", it)
	}
	if _, err := f.st.LatestExtraction(id); err != nil {
		t.Fatal("extraction should have been kept despite sorter failure")
	}
}

func TestUserMoveIsCorrectionSeenBySorter(t *testing.T) {
	f := newFixture(t)
	f.fake.Queue(ext("Gym")).Queue(srt("2min", "now"))
	id, _ := f.svc.Dump(context.Background(), DumpInput{Text: "gym"})
	mid, err := f.svc.Move(id, "eventually", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SetMoveReason(mid, "not today"); err != nil {
		t.Fatal(err)
	}
	f.fake.Queue(ext("Run")).Queue(srt("eventually", "like gym"))
	f.svc.Dump(context.Background(), DumpInput{Text: "run"})
	p := f.fake.Requests[3].Prompt
	if !strings.Contains(p, `"Gym" moved 2min -> eventually because: not today`) {
		t.Fatalf("correction missing from sorter prompt:\n%s", p)
	}
	if !strings.Contains(p, id+" | eventually | Gym") {
		t.Fatalf("board view missing:\n%s", p)
	}
}

func TestRelationProposalIsRecordedNotActedOn(t *testing.T) {
	f := newFixture(t)
	f.fake.Queue(ext("Flight")).Queue(srt("deadline", "x"))
	a, _ := f.svc.Dump(context.Background(), DumpInput{Text: "flight"})
	f.fake.Queue(ext("Hotel")).Queue(sorter.Result{Pile: "eventually", Reason: "y", RelatedID: a, RelatedReason: "same trip"})
	b, _ := f.svc.Dump(context.Background(), DumpInput{Text: "hotel"})
	snap, _ := f.svc.Snapshot()
	var hotel Item
	for _, it := range snap.Piles["eventually"] {
		if it.ID == b {
			hotel = it
		}
	}
	if len(hotel.Relations) != 1 || hotel.Relations[0].OtherID != a || hotel.Relations[0].Reason != "same trip" {
		t.Fatalf("%+v", hotel)
	}
	atts, _ := f.st.ListAttachments(a)
	if len(atts) != 1 {
		t.Fatal("must not auto-merge")
	}
}

func TestDoneArchiveReopen(t *testing.T) {
	f := newFixture(t)
	f.fake.Queue(ext("A")).Queue(srt("2min", "x"))
	id, _ := f.svc.Dump(context.Background(), DumpInput{Text: "a"})
	f.svc.Done(id)
	b, _ := f.svc.Snapshot()
	if len(b.Done) != 1 || b.Counts.TwoMin != 0 {
		t.Fatalf("%+v", b)
	}
	f.svc.Archive(id)
	b, _ = f.svc.Snapshot()
	if len(b.Done) != 0 {
		t.Fatal("archived should be hidden")
	}
	f.svc.Reopen(id)
	b, _ = f.svc.Snapshot()
	if b.Counts.TwoMin != 1 {
		t.Fatal("reopen failed")
	}
	if err := f.svc.Done("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("%v", err)
	}
}

func TestEditOverridesWithoutNewExtraction(t *testing.T) {
	f := newFixture(t)
	f.fake.Queue(ext("Model title")).Queue(srt("2min", "x"))
	id, _ := f.svc.Dump(context.Background(), DumpInput{Text: "a"})
	title := "My title"
	if err := f.svc.Edit(id, Edit{Title: &title}); err != nil {
		t.Fatal(err)
	}
	b, _ := f.svc.Snapshot()
	if b.Piles["2min"][0].Title != "My title" || b.Piles["2min"][0].Summary != "sum Model title" {
		t.Fatalf("%+v", b.Piles["2min"][0])
	}
	if exs, _ := f.st.ListExtractions(id); len(exs) != 1 {
		t.Fatal("edit must not re-extract")
	}
}

func TestSetAndClearDeadlineMovePiles(t *testing.T) {
	f := newFixture(t)
	f.fake.Queue(ext("A")).Queue(srt("eventually", "x"))
	id, _ := f.svc.Dump(context.Background(), DumpInput{Text: "a"})
	d := when.When{Time: time.Date(2026, 9, 11, 0, 0, 0, 0, lisbon), AllDay: true}
	if err := f.svc.SetDeadline(id, d); err != nil {
		t.Fatal(err)
	}
	it, _ := f.st.GetItem(id)
	if it.Pile != "deadline" || it.Deadline == nil || !it.AllDay {
		t.Fatalf("%+v", it)
	}
	moves, _ := f.st.ListMoves(id)
	if moves[len(moves)-1].Actor != "user" {
		t.Fatal("deadline set should be a user move")
	}
	if err := f.svc.ClearDeadline(id); err != nil {
		t.Fatal(err)
	}
	it, _ = f.st.GetItem(id)
	if it.Pile != "eventually" || it.Deadline != nil {
		t.Fatalf("%+v", it)
	}
}

func TestDeadlinePileOrderedByDate(t *testing.T) {
	f := newFixture(t)
	f.fake.Queue(ext("Late", extract.Date{Date: "2026-09-20", Confident: true})).Queue(srt("deadline", "x"))
	f.svc.Dump(context.Background(), DumpInput{Text: "late"})
	f.fake.Queue(ext("Soon", extract.Date{Date: "2026-09-06", Confident: true})).Queue(srt("deadline", "x"))
	f.svc.Dump(context.Background(), DumpInput{Text: "soon"})
	b, _ := f.svc.Snapshot()
	if b.Piles["deadline"][0].Title != "Soon" {
		t.Fatalf("%+v", b.Piles["deadline"])
	}
}

func TestAttachAndDetachReExtract(t *testing.T) {
	f := newFixture(t)
	f.fake.Queue(ext("A")).Queue(srt("eventually", "x"))
	id, _ := f.svc.Dump(context.Background(), DumpInput{Text: "a"})
	f.fake.Queue(ext("A with pic")).Queue(srt("eventually", "x"))
	if err := f.svc.Attach(context.Background(), id, []FileInput{{Name: "p.png", Reader: strings.NewReader("img")}}); err != nil {
		t.Fatal(err)
	}
	if exs, _ := f.st.ListExtractions(id); len(exs) != 2 {
		t.Fatal("attach must re-extract")
	}
	atts, _ := f.st.ListAttachments(id)
	f.fake.Queue(ext("A again")).Queue(srt("eventually", "x"))
	if err := f.svc.Detach(context.Background(), atts[1].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(atts[1].Path); !os.IsNotExist(err) {
		t.Fatal("detached file still on disk")
	}
	if exs, _ := f.st.ListExtractions(id); len(exs) != 3 {
		t.Fatal("detach must re-extract")
	}
	atts, _ = f.st.ListAttachments(id)
	if err := f.svc.Detach(context.Background(), atts[0].ID); err == nil {
		t.Fatal("must refuse to empty an item")
	}
}

func TestDeleteRemovesRecordsAndFiles(t *testing.T) {
	f := newFixture(t)
	f.fake.Queue(ext("A")).Queue(srt("eventually", "x"))
	id, _ := f.svc.Dump(context.Background(), DumpInput{Text: "a", Files: []FileInput{{Name: "f", Reader: strings.NewReader("x")}}})
	atts, _ := f.st.ListAttachments(id)
	if err := f.svc.Delete(id); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.GetItem(id); !errors.Is(err, ErrNotFound) {
		t.Fatal("item still there")
	}
	for _, a := range atts {
		if _, err := os.Stat(a.Path); !os.IsNotExist(err) {
			t.Fatalf("file still there: %s", a.Path)
		}
	}
	if err := f.svc.Delete(id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("%v", err)
	}
}

func TestSubscribersGetSnapshotsAndProcessingFlag(t *testing.T) {
	f := newFixture(t)
	ch, cancel, err := f.svc.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	first := <-ch
	if first.Counts != (Counts{}) {
		t.Fatalf("initial snapshot should be empty: %+v", first.Counts)
	}
	f.fake.Queue(ext("A")).Queue(srt("2min", "x"))
	f.svc.Dump(context.Background(), DumpInput{Text: "a"})
	// Slow reader: only the latest snapshot is kept.
	last := <-ch
	if last.Counts.TwoMin != 1 || last.Piles["2min"][0].Processing {
		t.Fatalf("%+v", last)
	}
	select {
	case <-ch:
		t.Fatal("should not have more than one buffered snapshot")
	default:
	}
}

func TestAsyncProcessingMarksItemAsProcessing(t *testing.T) {
	f := newFixture(t)
	f.svc.opts.Sync = false
	block := make(chan struct{})
	f.svc.model = blockingRunner{block: block, inner: f.fake}
	f.fake.Queue(ext("A")).Queue(srt("2min", "x"))
	id, _ := f.svc.Dump(context.Background(), DumpInput{Text: "a"})
	b, _ := f.svc.Snapshot()
	if !b.Piles["unsorted"][0].Processing || b.Piles["unsorted"][0].ID != id {
		t.Fatalf("%+v", b.Piles["unsorted"])
	}
	close(block)
	f.svc.Wait()
	b, _ = f.svc.Snapshot()
	if b.Counts.TwoMin != 1 || b.Piles["2min"][0].Processing {
		t.Fatalf("%+v", b)
	}
}

type blockingRunner struct {
	block <-chan struct{}
	inner model.Runner
}

func (b blockingRunner) Run(ctx context.Context, req model.Request) (model.Response, error) {
	<-b.block
	return b.inner.Run(ctx, req)
}

func TestDumpFailsLoudlyWhenDiskUnwritable(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores permissions")
	}
	f := newFixture(t)
	ro := filepath.Join(f.root, "ro")
	os.MkdirAll(ro, 0o500)
	f.svc.blob = blob.New(ro)
	if _, err := f.svc.Dump(context.Background(), DumpInput{Text: "a"}); err == nil {
		t.Fatal("want error")
	}
	items, _ := f.st.ListItems("")
	if len(items) != 0 {
		t.Fatal("no record should survive a failed save")
	}
	if len(f.fake.Requests) != 0 {
		t.Fatal("no model call on failed save")
	}
}

func TestPastConfidentDateIsOverdueDeadline(t *testing.T) {
	f := newFixture(t)
	f.fake.Queue(ext("Rent", extract.Date{Date: "2026-09-01", Confident: true})).Queue(srt("eventually", "x"))
	id, _ := f.svc.Dump(context.Background(), DumpInput{Text: "rent"})
	it, _ := f.st.GetItem(id)
	if it.Pile != "deadline" || it.Deadline == nil || it.Reason != "was due Tue 1 Sep" {
		t.Fatalf("%+v", it)
	}
}

func TestSorterCannotInventADeadline(t *testing.T) {
	f := newFixture(t)
	f.fake.Queue(ext("Vague")).Queue(srt("deadline", "feels urgent"))
	id, _ := f.svc.Dump(context.Background(), DumpInput{Text: "vague"})
	it, _ := f.st.GetItem(id)
	if it.Pile != "unsorted" || it.Deadline != nil || !strings.Contains(it.Reason, "no date") {
		t.Fatalf("%+v", it)
	}
}

func TestReExtractionRespectsUserPlacement(t *testing.T) {
	f := newFixture(t)
	f.fake.Queue(ext("A")).Queue(srt("2min", "now"))
	id, _ := f.svc.Dump(context.Background(), DumpInput{Text: "a"})
	f.svc.Move(id, "eventually", "not today")
	f.fake.Queue(ext("A+")).Queue(srt("2min", "still now"))
	f.svc.Attach(context.Background(), id, []FileInput{{Name: "p.png", Reader: strings.NewReader("x")}})
	it, _ := f.st.GetItem(id)
	if it.Pile != "eventually" || it.Reason != "not today" {
		t.Fatalf("system overrode the user: %+v", it)
	}
	if it.Estimate != "10m" {
		t.Fatal("estimate should still refresh")
	}
}

func TestUserMoveOutOfDeadlineClearsDateAndIntoNeedsOne(t *testing.T) {
	f := newFixture(t)
	f.fake.Queue(ext("A", extract.Date{Date: "2026-09-10", Confident: true})).Queue(srt("deadline", "x"))
	id, _ := f.svc.Dump(context.Background(), DumpInput{Text: "a"})
	if _, err := f.svc.Move(id, "eventually", ""); err != nil {
		t.Fatal(err)
	}
	it, _ := f.st.GetItem(id)
	if it.Deadline != nil {
		t.Fatal("date should be cleared when leaving deadline")
	}
	if _, err := f.svc.Move(id, "deadline", ""); err == nil {
		t.Fatal("moving into deadline without a date must be refused")
	}
}

func TestRecoverMarksInterruptedItems(t *testing.T) {
	f := newFixture(t)
	f.fake.Queue(ext("Done")).Queue(srt("2min", "x"))
	ok, _ := f.svc.Dump(context.Background(), DumpInput{Text: "fine"})
	stuck, _ := f.st.CreateItem()
	f.st.AddAttachment(stuck.ID, store.Attachment{Kind: "text", Name: "t", Path: "attachments/x/t"})
	if err := f.svc.Recover(); err != nil {
		t.Fatal(err)
	}
	b, _ := f.svc.Snapshot()
	if len(b.Failed) != 1 || b.Failed[0].ID != stuck.ID || !strings.Contains(b.Failed[0].Error, "interrupted") {
		t.Fatalf("%+v", b.Failed)
	}
	it, _ := f.st.GetItem(ok)
	if it.Error != "" {
		t.Fatal("healthy item marked")
	}
}

func TestCloseInterruptsInFlightWork(t *testing.T) {
	f := newFixture(t)
	f.svc.opts.Sync = false
	started := make(chan struct{})
	f.svc.model = ctxRunner{started: started}
	id, _ := f.svc.Dump(context.Background(), DumpInput{Text: "a"})
	<-started
	done := make(chan struct{})
	go func() { f.svc.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return")
	}
	it, _ := f.st.GetItem(id)
	if !strings.Contains(it.Error, "interrupted") {
		t.Fatalf("%+v", it)
	}
}

// ctxRunner blocks until its context is cancelled.
type ctxRunner struct{ started chan struct{} }

func (r ctxRunner) Run(ctx context.Context, req model.Request) (model.Response, error) {
	select {
	case r.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return model.Response{}, ctx.Err()
}

func TestConcurrentRequestsForOneItemRunOnePipelineAgain(t *testing.T) {
	f := newFixture(t)
	f.svc.opts.Sync = false
	gate := make(chan struct{})
	f.svc.model = blockingRunner{block: gate, inner: f.fake}
	f.fake.Queue(ext("v1")).Queue(srt("2min", "x")).Queue(ext("v2")).Queue(srt("2min", "x"))
	id, _ := f.svc.Dump(context.Background(), DumpInput{Text: "a"})
	// Two more requests while the first pipeline is blocked: they must
	// coalesce into exactly one extra run, not two parallel ones.
	f.svc.Retry(context.Background(), id)
	f.svc.Retry(context.Background(), id)
	close(gate)
	f.svc.Wait()
	exs, _ := f.st.ListExtractions(id)
	if len(exs) != 2 {
		t.Fatalf("want 2 extractions (initial + one coalesced rerun), got %d", len(exs))
	}
	it, _ := f.st.GetItem(id)
	if it.Error != "" {
		t.Fatalf("unexpected error: %s", it.Error)
	}
	if len(f.fake.Responses) != 0 {
		t.Fatalf("%d scripted responses unused", len(f.fake.Responses))
	}
}

func TestSubscribeNeverBlocksUnderPublishStorm(t *testing.T) {
	f := newFixture(t)
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				f.svc.publish()
			}
		}
	}()
	defer close(stop)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 200; i++ {
			_, cancel, err := f.svc.Subscribe()
			if err != nil {
				t.Error(err)
				return
			}
			cancel()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Subscribe blocked")
	}
}
