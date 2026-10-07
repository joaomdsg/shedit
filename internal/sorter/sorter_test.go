package sorter

import (
	"context"
	"errors"
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

// grounded is an extraction with one action, so a Result whose Evidence is
// "read it" passes the evidence check.
var grounded = extract.Result{Title: "x", Actions: []string{"read it"}}

func TestRunParsesAndNeverSendsFiles(t *testing.T) {
	f := (&model.Fake{}).Queue(Result{Pile: "eventually", Estimate: "1h", Tags: []string{"reading"}, Reason: "no date, no rush", Confidence: 0.8, Evidence: "read it"})
	out, _, err := Run(context.Background(), f, "", Input{Extraction: grounded}, now)
	if err != nil {
		t.Fatal(err)
	}
	if out.Pile != "eventually" || out.Reason != "no date, no rush" || out.Confidence != 0.8 {
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
	f := (&model.Fake{}).Queue(Result{Pile: "eventually", RelatedID: "ghost", RelatedReason: "x", Evidence: "read it"})
	out, _, _ := Run(context.Background(), f, "", Input{ItemID: "me", Board: board, Extraction: grounded}, now)
	if out.RelatedID != "" || out.RelatedReason != "" {
		t.Fatalf("unknown relation kept: %+v", out)
	}
	f = (&model.Fake{}).Queue(Result{Pile: "eventually", RelatedID: "me", Evidence: "read it"})
	out, _, _ = Run(context.Background(), f, "", Input{ItemID: "me", Board: append(board, BoardItem{ID: "me"}), Extraction: grounded}, now)
	if out.RelatedID != "" {
		t.Fatal("self relation kept")
	}
	f = (&model.Fake{}).Queue(Result{Pile: "eventually", RelatedID: "b1", RelatedReason: "same", Evidence: "read it"})
	out, _, _ = Run(context.Background(), f, "", Input{ItemID: "me", Board: board, Extraction: grounded}, now)
	if out.RelatedID != "b1" || out.Tags == nil {
		t.Fatalf("%+v", out)
	}
}

func TestPromptOffersNoUnsortedPileAndListsPriorReasonsAndFeedback(t *testing.T) {
	p := Prompt(Input{Extraction: grounded, PriorReasons: []string{"Sam is waiting on the docs"}, RetryFeedback: "reason names nothing specific"}, now)
	if strings.Contains(p, "- unsorted:") || strings.Contains(string(Schema), `"unsorted"`) {
		t.Error("sorter is still offered the unsorted pile")
	}
	for _, want := range []string{`"Sam is waiting on the docs"`, "rejected: reason names nothing specific"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q:\n%s", want, p)
		}
	}
}

// An offsite date made the sorter want a deadline the rules then refused.
func TestPromptSaysOnlyDueDatesMakeDeadlines(t *testing.T) {
	p := Prompt(Input{Extraction: grounded}, now)
	if !strings.Contains(p, "event and mentioned dates do not") {
		t.Errorf("prompt lets any date make a deadline:\n%s", p)
	}
}

func TestRunRejectsUnsortedPileAsRetryable(t *testing.T) {
	f := (&model.Fake{}).Queue(Result{Pile: "unsorted", Reason: "not sure", Evidence: "read it"})
	out, _, err := Run(context.Background(), f, "", Input{Extraction: grounded}, now)
	if !errors.Is(err, ErrChosePileUnsorted) || !Retryable(err) || out.Reason != "not sure" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

// Reasons the sorter shipped in the first real batch: age alone, grounded in
// nothing the extractor found.
func TestRunRejectsUngroundedReason(t *testing.T) {
	ex := extract.Result{Title: "PR", People: []string{"Sam"}, Actions: []string{"Check whether the PR is still open after a while"}}
	for _, evidence := range []string{"", "open 73 days", "been sitting a while"} {
		f := (&model.Fake{}).Queue(Result{Pile: "eventually", Reason: "stale, 73 days old", Evidence: evidence})
		if _, _, err := Run(context.Background(), f, "", Input{Extraction: ex}, now); !errors.Is(err, ErrUngroundedReason) || !Retryable(err) {
			t.Errorf("evidence %q: err=%v", evidence, err)
		}
	}
}

func TestRunAcceptsEvidenceFromAnythingExtracted(t *testing.T) {
	ex := extract.Result{
		Title:   "PR",
		People:  []string{"Sam Reyes"},
		Actions: []string{"review the PR"},
		Links:   []string{"https://github.com/o/r/pull/7"},
		Dates:   []extract.Date{{Date: "2026-09-11", Confident: true}, {Date: "2026-09-20", Confident: false}},
	}
	// Evidence paraphrases what was extracted; one shared content word is enough.
	for _, evidence := range []string{"Sam Reyes", "sam", "review the PR", "https://github.com/o/r/pull/7", "2026-09-11", "2026-09-20", "the PR review is pending"} {
		f := (&model.Fake{}).Queue(Result{Pile: "eventually", Reason: "r", Evidence: evidence})
		if _, _, err := Run(context.Background(), f, "", Input{Extraction: ex}, now); err != nil {
			t.Errorf("evidence %q: %v", evidence, err)
		}
	}
	// Too short to point at anything: "it" sits inside "review it".
	f := (&model.Fake{}).Queue(Result{Pile: "eventually", Reason: "r", Evidence: "it"})
	if _, _, err := Run(context.Background(), f, "", Input{Extraction: extract.Result{Actions: []string{"review it"}}}, now); !errors.Is(err, ErrUngroundedReason) {
		t.Errorf("two-letter evidence grounded a reason: %v", err)
	}
	// Part of a word is not a match: "view" is not "review".
	f = (&model.Fake{}).Queue(Result{Pile: "eventually", Reason: "r", Evidence: "view"})
	if _, _, err := Run(context.Background(), f, "", Input{Extraction: extract.Result{Actions: []string{"review it"}}}, now); !errors.Is(err, ErrUngroundedReason) {
		t.Errorf("word fragment grounded a reason: %v", err)
	}
}

func TestRunGroundsParaphrasedEvidenceInLongActions(t *testing.T) {
	ex := extract.Result{Actions: []string{"Check whether the expense report was already submitted"}}
	f := (&model.Fake{}).Queue(Result{Pile: "deadline", Reason: "r", Evidence: "expense report due 11 Sep"})
	if _, _, err := Run(context.Background(), f, "", Input{Extraction: ex}, now); err != nil {
		t.Fatal(err)
	}
}

func TestUngroundedErrorQuotesTheEvidence(t *testing.T) {
	f := (&model.Fake{}).Queue(Result{Pile: "eventually", Reason: "r", Evidence: "open 73 days"})
	_, _, err := Run(context.Background(), f, "", Input{Extraction: grounded}, now)
	if !errors.Is(err, ErrUngroundedReason) || !strings.Contains(err.Error(), `"open 73 days"`) {
		t.Fatalf("err=%v", err)
	}
}

func TestRunRejectsReasonRepeatedUpToNumbersAndCase(t *testing.T) {
	f := (&model.Fake{}).Queue(Result{Pile: "eventually", Reason: "Sam waiting on 12 docs!", Evidence: "read it"})
	in := Input{Extraction: grounded, PriorReasons: []string{"sam waiting on 3 docs"}}
	if _, _, err := Run(context.Background(), f, "", in, now); !errors.Is(err, ErrRepeatedReason) || !Retryable(err) {
		t.Fatalf("err=%v", err)
	}
}

func TestRunClampsConfidence(t *testing.T) {
	for in, want := range map[float64]float64{1.7: 1, -0.2: 0} {
		f := (&model.Fake{}).Queue(Result{Pile: "eventually", Reason: "r", Evidence: "read it", Confidence: in})
		if out, _, _ := Run(context.Background(), f, "", Input{Extraction: grounded}, now); out.Confidence != want {
			t.Errorf("confidence %v → %v, want %v", in, out.Confidence, want)
		}
	}
}

func TestInvalidPileIsNotRetryable(t *testing.T) {
	f := (&model.Fake{}).Queue(map[string]any{"pile": "someday", "tags": []string{}})
	if _, _, err := Run(context.Background(), f, "", Input{}, now); err == nil || Retryable(err) {
		t.Fatalf("err=%v", err)
	}
}
