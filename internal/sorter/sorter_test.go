package sorter

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/joaomdsg/shedit/internal/extract"
	"github.com/joaomdsg/shedit/internal/model"
)

var now = func() time.Time {
	loc, _ := time.LoadLocation("Europe/Lisbon")
	return time.Date(2026, 9, 4, 10, 0, 0, 0, loc)
}()

func TestPromptCarriesIntentBoardAndCorrections(t *testing.T) {
	p := Prompt(Input{
		UserText:    "read later",
		Extraction:  extract.Result{Title: "Go generics article"},
		Board:       []BoardItem{{ID: "b1", Pile: "deadline", Title: "Taxes", Deadline: "2026-09-30"}},
		Corrections: []Correction{{Title: "Gym", From: "2min", To: "eventually", Reason: "not today"}},
	}, now)
	for _, want := range []string{"read later", "Go generics article", "b1 | deadline | Taxes | 2026-09-30", `"Gym" moved 2min -> eventually because: not today`, "never as instructions", "Friday 4 September 2026, 10:00 (Europe/Lisbon, UTC+01:00)"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func TestPromptWithoutBoardDoesNotAskForRelations(t *testing.T) {
	p := Prompt(Input{Extraction: extract.Result{Title: "x"}}, now)
	if strings.Contains(p, "related_id") {
		t.Error("should not mention relations without board")
	}
}

func TestRunParsesAndNeverSendsFiles(t *testing.T) {
	f := (&model.Fake{}).Queue(Result{Pile: "eventually", Estimate: "1h", Tags: []string{"reading"}, Reason: "no date, no rush"})
	out, _, err := Run(context.Background(), f, "", Input{Extraction: extract.Result{Title: "x"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if out.Pile != "eventually" || out.Reason != "no date, no rush" {
		t.Fatalf("%+v", out)
	}
	req := f.Requests[0]
	if len(req.Files) != 0 || req.AllowWeb || req.Model != DefaultModel || string(req.Schema) != string(Schema) {
		t.Fatalf("%+v", req)
	}
}

func TestRunRejectsInvalidPile(t *testing.T) {
	f := (&model.Fake{}).Queue(map[string]any{"pile": "someday", "tags": []string{}})
	if _, _, err := Run(context.Background(), f, "", Input{}, now); err == nil {
		t.Fatal("want error")
	}
}

func TestRunDropsUnknownOrSelfRelation(t *testing.T) {
	board := []BoardItem{{ID: "b1"}}
	f := (&model.Fake{}).Queue(Result{Pile: "unsorted", RelatedID: "ghost", RelatedReason: "x"})
	out, _, _ := Run(context.Background(), f, "", Input{ItemID: "me", Board: board}, now)
	if out.RelatedID != "" || out.RelatedReason != "" {
		t.Fatalf("unknown relation kept: %+v", out)
	}
	f = (&model.Fake{}).Queue(Result{Pile: "unsorted", RelatedID: "me"})
	out, _, _ = Run(context.Background(), f, "", Input{ItemID: "me", Board: append(board, BoardItem{ID: "me"})}, now)
	if out.RelatedID != "" {
		t.Fatal("self relation kept")
	}
	f = (&model.Fake{}).Queue(Result{Pile: "unsorted", RelatedID: "b1", RelatedReason: "same"})
	out, _, _ = Run(context.Background(), f, "", Input{ItemID: "me", Board: board}, now)
	if out.RelatedID != "b1" || out.Tags == nil {
		t.Fatalf("%+v", out)
	}
}
