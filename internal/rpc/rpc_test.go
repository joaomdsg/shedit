package rpc

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joaomdsg/shedit/internal/blob"
	"github.com/joaomdsg/shedit/internal/core"
	"github.com/joaomdsg/shedit/internal/extract"
	"github.com/joaomdsg/shedit/internal/model"
	"github.com/joaomdsg/shedit/internal/sorter"
	"github.com/joaomdsg/shedit/internal/store"
)

func setup(t *testing.T) (Client, *model.Fake, *store.Store) {
	t.Helper()
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	fake := &model.Fake{}
	lisbon, _ := time.LoadLocation("Europe/Lisbon")
	svc := core.New(st, blob.New(root), fake, core.Options{Sync: true, Now: func() time.Time { return time.Now().In(lisbon) }})
	// Short socket path: unix sockets cap at ~108 bytes.
	dir, _ := os.MkdirTemp("", "sh")
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s")
	l, err := Listen(sock)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go NewServer(svc).Serve(ctx, l)
	return Client{Path: sock}, fake, st
}

func queueItem(f *model.Fake, title, pile string) {
	f.Queue(extract.Result{Title: title, Summary: "s"}).Queue(sorter.Result{Pile: pile, Reason: "r", Tags: []string{}})
}

func TestDumpBoardAndStatus(t *testing.T) {
	c, f, _ := setup(t)
	if !c.Ping() {
		t.Fatal("ping")
	}
	queueItem(f, "Hello", "2min")
	var id IDArgs
	if err := c.Call("dump", DumpArgs{Text: "hello"}, &id); err != nil {
		t.Fatal(err)
	}
	var b core.Board
	if err := c.Call("board", nil, &b); err != nil {
		t.Fatal(err)
	}
	if b.Counts.TwoMin != 1 || b.Piles["2min"][0].ID != id.ID {
		t.Fatalf("%+v", b)
	}
	var st Status
	c.Call("status", nil, &st)
	if st.CostUSD <= 0 || st.Version == "" {
		t.Fatalf("%+v", st)
	}
}

func TestDumpWithFilesByPath(t *testing.T) {
	c, f, st := setup(t)
	p := filepath.Join(t.TempDir(), "pic.png")
	os.WriteFile(p, []byte("img"), 0o600)
	queueItem(f, "Pic", "eventually")
	var id IDArgs
	if err := c.Call("dump", DumpArgs{Files: []string{p}}, &id); err != nil {
		t.Fatal(err)
	}
	atts, _ := st.ListAttachments(id.ID)
	if len(atts) != 1 || atts[0].Kind != "image" || atts[0].Name != "pic.png" {
		t.Fatalf("%+v", atts)
	}
	if err := c.Call("dump", DumpArgs{Files: []string{"/nope/none"}}, nil); err == nil {
		t.Fatal("missing file should fail")
	}
}

func TestErrorsComeBackAsErrors(t *testing.T) {
	c, _, _ := setup(t)
	err := c.Call("done", IDArgs{ID: "nope"}, nil)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("%v", err)
	}
	if err := c.Call("bogus", nil, nil); err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("%v", err)
	}
}

func TestAllActions(t *testing.T) {
	c, f, st := setup(t)
	queueItem(f, "A", "unsorted")
	var id IDArgs
	c.Call("dump", DumpArgs{Text: "a"}, &id)

	var mv ReasonArgs
	if err := c.Call("move", MoveArgs{ID: id.ID, Pile: "eventually"}, &mv); err != nil || mv.MoveID == "" {
		t.Fatalf("%v %+v", err, mv)
	}
	if err := c.Call("reason", ReasonArgs{MoveID: mv.MoveID, Reason: "later"}, nil); err != nil {
		t.Fatal(err)
	}
	moves, _ := st.ListMoves(id.ID)
	if moves[len(moves)-1].Reason != "later" {
		t.Fatal("reason not set")
	}

	title := "T"
	if err := c.Call("edit", EditArgs{ID: id.ID, Title: &title}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Call("deadline", DeadlineArgs{ID: id.ID, Date: "2030-01-02"}, nil); err != nil {
		t.Fatal(err)
	}
	it, _ := st.GetItem(id.ID)
	if it.Pile != "deadline" || it.TitleOverride != "T" || it.Deadline == nil || !it.AllDay {
		t.Fatalf("%+v", it)
	}
	if err := c.Call("deadline", DeadlineArgs{ID: id.ID, Date: "2030-01-02 15:30"}, nil); err != nil {
		t.Fatal(err)
	}
	it, _ = st.GetItem(id.ID)
	if it.AllDay || it.Deadline.Hour() != 15 || it.Deadline.Minute() != 30 {
		t.Fatalf("timed deadline wrong: %+v", it.Deadline)
	}
	_, off := it.Deadline.Zone()
	if off != 0 { // Lisbon is UTC+0 in January
		t.Fatalf("offset %d", off)
	}
	if err := c.Call("deadline", DeadlineArgs{ID: id.ID, Date: "not a date"}, nil); err == nil {
		t.Fatal("bad date accepted")
	}
	if err := c.Call("deadline", DeadlineArgs{ID: id.ID}, nil); err != nil {
		t.Fatal(err)
	}
	it, _ = st.GetItem(id.ID)
	if it.Deadline != nil || it.Pile != "eventually" {
		t.Fatalf("%+v", it)
	}

	p := filepath.Join(t.TempDir(), "doc.pdf")
	os.WriteFile(p, []byte("pdf"), 0o600)
	queueItem(f, "A+doc", "eventually")
	if err := c.Call("attach", AttachArgs{ID: id.ID, Files: []string{p}}, nil); err != nil {
		t.Fatal(err)
	}
	atts, _ := st.ListAttachments(id.ID)
	if len(atts) != 2 {
		t.Fatalf("%+v", atts)
	}
	queueItem(f, "A", "eventually")
	if err := c.Call("detach", DetachArgs{AttachmentID: atts[1].ID}, nil); err != nil {
		t.Fatal(err)
	}

	if err := c.Call("retry", "not an object", nil); err == nil {
		t.Fatal("malformed args must be rejected")
	}
	f.QueueError(errorf("down"))
	if err := c.Call("retry", IDArgs{ID: id.ID}, nil); err != nil {
		t.Fatal(err)
	}
	it, _ = st.GetItem(id.ID)
	if it.Error == "" {
		t.Fatal("retry should have recorded the failure")
	}

	for _, cmd := range []string{"done", "archive", "reopen"} {
		if err := c.Call(cmd, IDArgs{ID: id.ID}, nil); err != nil {
			t.Fatalf("%s: %v", cmd, err)
		}
	}
	if err := c.Call("delete", IDArgs{ID: id.ID}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetItem(id.ID); err != store.ErrNotFound {
		t.Fatal("not deleted")
	}
}

type strErr string

func (e strErr) Error() string { return string(e) }
func errorf(s string) error    { return strErr(s) }

func TestWatchStreamsSnapshots(t *testing.T) {
	c, f, _ := setup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got := make(chan core.Board, 16)
	go c.Watch(ctx, func(b core.Board) error { got <- b; return nil })
	first := <-got
	if first.Counts.TwoMin != 0 {
		t.Fatalf("%+v", first)
	}
	queueItem(f, "X", "2min")
	c.Call("dump", DumpArgs{Text: "x"}, nil)
	deadline := time.After(3 * time.Second)
	for {
		select {
		case b := <-got:
			if b.Counts.TwoMin == 1 && !b.Piles["2min"][0].Processing {
				return
			}
		case <-deadline:
			t.Fatal("never saw the sorted item")
		}
	}
}

func TestWatchStopsWhenClientCancels(t *testing.T) {
	c, _, _ := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Watch(ctx, func(core.Board) error { return nil }) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watch did not stop")
	}
}

func TestListenRefusesLiveSocketAndReplacesStale(t *testing.T) {
	c, _, _ := setup(t)
	if _, err := Listen(c.Path); err == nil {
		t.Fatal("should refuse a live socket")
	}
	if !c.Ping() {
		t.Fatal("loser must not have disturbed the live socket")
	}
	dir, _ := os.MkdirTemp("", "sh")
	defer os.RemoveAll(dir)
	stale := filepath.Join(dir, "s")
	os.WriteFile(stale, nil, 0o600)
	l, err := Listen(stale)
	if err != nil {
		t.Fatal(err)
	}
	l.Close()
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("socket file should be removed on close")
	}
}

func TestListenRaceHasExactlyOneWinner(t *testing.T) {
	dir, _ := os.MkdirTemp("", "sh")
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "s")
	results := make(chan *Listener, 8)
	for i := 0; i < 8; i++ {
		go func() {
			l, _ := Listen(path)
			results <- l
		}()
	}
	var winners []*Listener
	for i := 0; i < 8; i++ {
		if l := <-results; l != nil {
			winners = append(winners, l)
		}
	}
	if len(winners) != 1 {
		t.Fatalf("want 1 winner, got %d", len(winners))
	}
	if _, err := net.Dial("unix", path); err != nil {
		t.Fatalf("winner's socket not reachable: %v", err)
	}
	winners[0].Close()
}
