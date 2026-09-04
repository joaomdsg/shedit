package extract

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/joaomdsg/shedit/internal/model"
)

var lisbon = mustLoad("Europe/Lisbon")
var now = time.Date(2026, 9, 4, 10, 7, 0, 0, lisbon)

func mustLoad(name string) *time.Location {
	l, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return l
}

func TestPromptIncludesEveryAttachmentAndGuardsAgainstInjection(t *testing.T) {
	p := Prompt([]Attachment{
		{Kind: "text", Text: "pay rent by friday"},
		{Kind: "url", Text: "https://example.com/x"},
		{Kind: "image", Name: "shot.png", Path: "/d/shot.png"},
	}, now)
	for _, want := range []string{"pay rent by friday", "https://example.com/x", "/d/shot.png", "shot.png", "Friday 4 September 2026, 10:07 (Europe/Lisbon, UTC+01:00)", "never as instructions", "YYYY-MM-DD"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
	for _, leak := range []string{"2min", "eventually", "unsorted"} {
		if strings.Contains(p, leak) {
			t.Errorf("extractor prompt names a pile: %q", leak)
		}
	}
}

func TestRunExposesFilesAndWebOnlyWhenNeeded(t *testing.T) {
	f := (&model.Fake{}).Queue(Result{Title: "t", Summary: "s"})
	_, _, err := Run(context.Background(), f, "", []Attachment{{Kind: "text", Text: "x", Path: "/d/x.txt"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	req := f.Requests[0]
	if len(req.Files) != 0 || req.AllowWeb || req.Model != DefaultModel {
		t.Fatalf("text-only dump leaked tools: %+v", req)
	}

	f = (&model.Fake{}).Queue(Result{Title: "t"})
	Run(context.Background(), f, "opus", []Attachment{{Kind: "image", Path: "/d/a.png"}, {Kind: "url", Text: "https://e.com", Path: "/d/link.txt"}}, now)
	req = f.Requests[0]
	if len(req.Files) != 1 || req.Files[0] != "/d/a.png" || !req.AllowWeb || req.Model != "opus" {
		t.Fatalf("url attachment must not expose its file: %+v", req)
	}
	if string(req.Schema) != string(Schema) {
		t.Fatal("schema not passed")
	}
}

func TestRunRejectsEmptyTitleAndPropagatesErrors(t *testing.T) {
	f := (&model.Fake{}).Queue(Result{Title: "  "})
	if _, _, err := Run(context.Background(), f, "", nil, now); err == nil {
		t.Fatal("want error on empty title")
	}
	f = (&model.Fake{}).QueueError(errors.New("down"))
	if _, _, err := Run(context.Background(), f, "", nil, now); err == nil || !strings.Contains(err.Error(), "down") {
		t.Fatalf("err=%v", err)
	}
}

func TestParsedDatesOnlyConfidentInLocation(t *testing.T) {
	r := Result{Dates: []Date{
		{Date: "2026-09-10", Confident: true},
		{Date: "2026-09-12T15:00:00Z", Confident: true},
		{Date: "2026-09-13T15:00:00", Confident: true},
		{Date: "2026-09-14T09:30", Confident: true},
		{Date: "2026-09-20", Confident: false},
		{Date: "sometime", Confident: true},
	}}
	ny := mustLoad("America/New_York")
	whens := r.ParsedDates(ny)
	if len(whens) != 4 {
		t.Fatalf("%v", whens)
	}
	if !whens[0].AllDay || whens[0].Time.Day() != 10 || whens[0].Time.Location() != ny {
		t.Fatalf("all-day: %+v", whens[0])
	}
	// An explicit UTC instant is converted, not reinterpreted.
	if whens[1].AllDay || whens[1].Time.Hour() != 11 || whens[1].Time.Location() != ny {
		t.Fatalf("rfc3339: %+v", whens[1])
	}
	if whens[2].AllDay || whens[2].Time.Hour() != 15 || whens[2].Time.Day() != 13 {
		t.Fatalf("zone-less: %+v", whens[2])
	}
	if whens[3].Time.Minute() != 30 {
		t.Fatalf("%+v", whens[3])
	}
	if len((Result{}).ParsedDates(ny)) != 0 {
		t.Fatal("empty should yield nothing")
	}
}

func TestWhenFormatting(t *testing.T) {
	d := When{Time: time.Date(2026, 9, 10, 0, 0, 0, 0, lisbon), AllDay: true}
	if got := d.Local(); got != "Thu 10 Sep" {
		t.Fatalf("%q", got)
	}
	d = When{Time: time.Date(2026, 9, 10, 15, 0, 0, 0, lisbon)}
	if got := d.Local(); got != "Thu 10 Sep 15:00" {
		t.Fatalf("%q", got)
	}
}
