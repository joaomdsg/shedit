package store

import (
	"path/filepath"
	"testing"
	"time"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "shedit.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestCreateAndGetItem(t *testing.T) {
	s := open(t)
	it, err := s.CreateItem()
	if err != nil {
		t.Fatal(err)
	}
	if it.ID == "" || it.Status != StatusOpen || it.Pile != PileUnsorted {
		t.Fatalf("bad new item: %+v", it)
	}
	got, err := s.GetItem(it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != it.ID {
		t.Fatalf("got %q want %q", got.ID, it.ID)
	}
	if _, err := s.GetItem("nope"); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestAttachments(t *testing.T) {
	s := open(t)
	it, _ := s.CreateItem()
	a, err := s.AddAttachment(it.ID, Attachment{Kind: KindText, Name: "note.txt", Path: "/x/note.txt", SHA256: "abc", Size: 3})
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == "" || a.ItemID != it.ID {
		t.Fatalf("bad attachment %+v", a)
	}
	list, err := s.ListAttachments(it.ID)
	if err != nil || len(list) != 1 || list[0].Name != "note.txt" {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	if err := s.RemoveAttachment(a.ID); err != nil {
		t.Fatal(err)
	}
	list, _ = s.ListAttachments(it.ID)
	if len(list) != 0 {
		t.Fatalf("want empty, got %+v", list)
	}
	if err := s.RemoveAttachment(a.ID); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestExtractionsAreVersioned(t *testing.T) {
	s := open(t)
	it, _ := s.CreateItem()
	e1, err := s.AddExtraction(it.ID, Extraction{Model: "m", JSON: `{"title":"a"}`})
	if err != nil {
		t.Fatal(err)
	}
	e2, _ := s.AddExtraction(it.ID, Extraction{Model: "m", JSON: `{"title":"b"}`})
	if e1.Version != 1 || e2.Version != 2 {
		t.Fatalf("versions %d %d", e1.Version, e2.Version)
	}
	latest, err := s.LatestExtraction(it.ID)
	if err != nil || latest.JSON != `{"title":"b"}` {
		t.Fatalf("latest=%+v err=%v", latest, err)
	}
	all, _ := s.ListExtractions(it.ID)
	if len(all) != 2 {
		t.Fatalf("want 2 got %d", len(all))
	}
	if _, err := s.LatestExtraction("nope"); err != ErrNotFound {
		t.Fatalf("want ErrNotFound got %v", err)
	}
}

func TestMoveRecordsHistoryAndUpdatesPile(t *testing.T) {
	s := open(t)
	it, _ := s.CreateItem()
	m, err := s.Move(it.ID, PileDeadline, ActorSystem, "has a date")
	if err != nil {
		t.Fatal(err)
	}
	if m.From != PileUnsorted || m.To != PileDeadline || m.Actor != ActorSystem {
		t.Fatalf("bad move %+v", m)
	}
	got, _ := s.GetItem(it.ID)
	if got.Pile != PileDeadline || got.Reason != "has a date" {
		t.Fatalf("item not updated: %+v", got)
	}
	if _, err := s.Move(it.ID, "bogus", ActorUser, ""); err == nil {
		t.Fatal("want error for invalid pile")
	}
	s.Move(it.ID, PileEventually, ActorUser, "")
	hist, _ := s.ListMoves(it.ID)
	if len(hist) != 2 || hist[1].Actor != ActorUser {
		t.Fatalf("history %+v", hist)
	}
}

func TestUserMoveReasonCanBeSetLater(t *testing.T) {
	s := open(t)
	it, _ := s.CreateItem()
	m, _ := s.Move(it.ID, PileEventually, ActorUser, "")
	if err := s.SetMoveReason(m.ID, "not urgent"); err != nil {
		t.Fatal(err)
	}
	hist, _ := s.ListMoves(it.ID)
	if hist[0].Reason != "not urgent" {
		t.Fatalf("reason not set: %+v", hist[0])
	}
	if err := s.SetMoveReason("nope", "x"); err != ErrNotFound {
		t.Fatalf("want ErrNotFound got %v", err)
	}
}

func TestRecentUserMoves(t *testing.T) {
	s := open(t)
	a, _ := s.CreateItem()
	b, _ := s.CreateItem()
	s.Move(a.ID, PileDeadline, ActorSystem, "sys")
	s.Move(a.ID, PileEventually, ActorUser, "user1")
	s.Move(b.ID, Pile2Min, ActorUser, "user2")
	got, err := s.RecentUserMoves(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Reason != "user2" || got[1].Reason != "user1" {
		t.Fatalf("got %+v", got)
	}
	got, _ = s.RecentUserMoves(1)
	if len(got) != 1 {
		t.Fatalf("limit ignored: %+v", got)
	}
}

func TestStatusTransitions(t *testing.T) {
	s := open(t)
	it, _ := s.CreateItem()
	if err := s.SetStatus(it.ID, StatusDone); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetItem(it.ID)
	if got.Status != StatusDone || got.DoneAt == nil {
		t.Fatalf("%+v", got)
	}
	if err := s.SetStatus(it.ID, "weird"); err == nil {
		t.Fatal("want error")
	}
	if err := s.SetStatus("nope", StatusArchived); err != ErrNotFound {
		t.Fatalf("want ErrNotFound got %v", err)
	}
}

func TestUpdateItemFields(t *testing.T) {
	s := open(t)
	it, _ := s.CreateItem()
	lisbon, _ := time.LoadLocation("Europe/Lisbon")
	d := time.Date(2026, 9, 10, 0, 0, 0, 0, lisbon)
	title := "My title"
	allDay := true
	if err := s.UpdateItem(it.ID, ItemUpdate{Title: &title, Deadline: &d, AllDay: &allDay}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetItem(it.ID)
	if got.TitleOverride != "My title" || got.Deadline == nil || !got.Deadline.Equal(d) || !got.AllDay {
		t.Fatalf("%+v", got)
	}
	// The wall-clock date survives the round trip: offset is kept, not
	// normalised to UTC.
	if y, m, day := got.Deadline.Date(); y != 2026 || m != 9 || day != 10 {
		t.Fatalf("date shifted: %v", got.Deadline)
	}
	_, off := got.Deadline.Zone()
	if off != 3600 {
		t.Fatalf("offset lost: %v", got.Deadline)
	}
	empty := ""
	if err := s.UpdateItem(it.ID, ItemUpdate{Title: &empty, ClearDeadline: true}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetItem(it.ID)
	if got.TitleOverride != "" || got.Deadline != nil || got.AllDay {
		t.Fatalf("clear failed: %+v", got)
	}
}

func TestSortingResultAndError(t *testing.T) {
	s := open(t)
	it, _ := s.CreateItem()
	if err := s.SetSorting(it.ID, Sorting{Estimate: "15m", Tags: []string{"home", "money"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetError(it.ID, "extractor blew up"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetItem(it.ID)
	if got.Estimate != "15m" || len(got.Tags) != 2 || got.Error != "extractor blew up" {
		t.Fatalf("%+v", got)
	}
	s.SetError(it.ID, "")
	got, _ = s.GetItem(it.ID)
	if got.Error != "" {
		t.Fatalf("error not cleared")
	}
}

func TestDeleteItemRemovesEverything(t *testing.T) {
	s := open(t)
	it, _ := s.CreateItem()
	s.AddAttachment(it.ID, Attachment{Kind: KindText, Name: "a"})
	s.AddExtraction(it.ID, Extraction{JSON: "{}"})
	s.Move(it.ID, Pile2Min, ActorUser, "")
	if err := s.DeleteItem(it.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetItem(it.ID); err != ErrNotFound {
		t.Fatal("item still there")
	}
	if l, _ := s.ListAttachments(it.ID); len(l) != 0 {
		t.Fatal("attachments still there")
	}
	if l, _ := s.ListMoves(it.ID); len(l) != 0 {
		t.Fatal("moves still there")
	}
	if err := s.DeleteItem(it.ID); err != ErrNotFound {
		t.Fatalf("want ErrNotFound got %v", err)
	}
}

func TestOpenRefusesNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	s, _ := Open(path)
	s.db.Exec(`PRAGMA user_version=99`)
	s.Close()
	if _, err := Open(path); err == nil {
		t.Fatal("should refuse a newer schema")
	}
}

func TestListItemsByStatus(t *testing.T) {
	s := open(t)
	a, _ := s.CreateItem()
	b, _ := s.CreateItem()
	s.SetStatus(b.ID, StatusDone)
	openItems, err := s.ListItems(StatusOpen)
	if err != nil || len(openItems) != 1 || openItems[0].ID != a.ID {
		t.Fatalf("%+v %v", openItems, err)
	}
	all, _ := s.ListItems("")
	if len(all) != 2 {
		t.Fatalf("want 2 got %d", len(all))
	}
}

func TestRelations(t *testing.T) {
	s := open(t)
	a, _ := s.CreateItem()
	b, _ := s.CreateItem()
	if err := s.ProposeRelation(a.ID, b.ID, "same trip"); err != nil {
		t.Fatal(err)
	}
	rels, _ := s.ListRelations(a.ID)
	if len(rels) != 1 || rels[0].OtherID != b.ID || rels[0].Reason != "same trip" {
		t.Fatalf("%+v", rels)
	}
	// proposing again is idempotent
	s.ProposeRelation(a.ID, b.ID, "same trip again")
	rels, _ = s.ListRelations(a.ID)
	if len(rels) != 1 {
		t.Fatalf("dup relation: %+v", rels)
	}
}
