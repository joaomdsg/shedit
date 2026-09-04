package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joaomdsg/shedit/internal/core"
	"github.com/joaomdsg/shedit/internal/rpc"
)

var binPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "shedit-bin")
	if err != nil {
		panic(err)
	}
	binPath = filepath.Join(dir, "shedit")
	build := exec.Command("go", "build", "-o", binPath, "../../cmd/shedit")
	if out, err := build.CombinedOutput(); err != nil {
		panic(string(out))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// fakeClaude answers by looking at the prompt: sorter prompts get the sort
// output, everything else the extract output. Keyed on content rather than
// call order so a missing or extra call cannot silently desync the answers.
func fakeClaude(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
for last; do :; done
case "$last" in
  *"You are the sorter"*) cat <<'JSON'
` + sortOut + `
JSON
  ;;
  *) cat <<'JSON'
` + extractOut + `
JSON
  ;;
esac
`
	bin := filepath.Join(dir, "claude")
	os.WriteFile(bin, []byte(script), 0o755)
	return bin
}

const extractOut = `{"type":"result","is_error":false,"result":"","structured_output":{"title":"Buy milk","summary":"Get milk","dates":[],"people":[],"links":[],"actions":["buy"],"size_guess":"tiny","confidence":0.9},"total_cost_usd":0.01}`
const sortOut = `{"type":"result","is_error":false,"result":"","structured_output":{"pile":"2min","estimate":"5m","tags":["home"],"reason":"quick errand","related_id":"","related_reason":""},"total_cost_usd":0.002}`

type harness struct {
	t    *testing.T
	env  Env
	out  *bytes.Buffer
	errb *bytes.Buffer
}

func newHarness(t *testing.T, claude string) *harness {
	t.Helper()
	data := t.TempDir()
	sockDir, _ := os.MkdirTemp("", "sh")
	t.Cleanup(func() { os.RemoveAll(sockDir) })
	t.Setenv("SHEDIT_DATA", data)
	t.Setenv("SHEDIT_SOCKET", filepath.Join(sockDir, "s"))
	t.Setenv("SHEDIT_CLAUDE_BIN", claude)
	h := &harness{t: t, out: &bytes.Buffer{}, errb: &bytes.Buffer{}}
	h.env = Env{Stdout: h.out, Stderr: h.errb, Exe: binPath, StdinIsPipe: func() bool { return false }}
	t.Cleanup(func() {
		// Stop the spawned daemon.
		if c := (rpc.Client{Path: SocketPath()}); c.Ping() {
			exec.Command("pkill", "-f", binPath+" serve").Run()
		}
	})
	return h
}

func (h *harness) run(args ...string) (int, string) {
	h.out.Reset()
	h.errb.Reset()
	code := Main(args, h.env)
	return code, strings.TrimSpace(h.out.String())
}

func (h *harness) waitSorted(id string) core.Board {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var b core.Board
		rpc.Client{Path: SocketPath()}.Call("board", nil, &b)
		for _, items := range b.Piles {
			for _, it := range items {
				if it.ID == id && !it.Processing {
					return b
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	h.t.Fatal("item never finished processing")
	return core.Board{}
}

func TestHelp(t *testing.T) {
	h := newHarness(t, "/nonexistent")
	h.env.NoSpawn = true
	code, out := h.run()
	if code != 0 || !strings.Contains(out, "usage:") {
		t.Fatalf("%d %s", code, out)
	}
}

func TestClientWithoutDaemonFailsWhenSpawnDisabled(t *testing.T) {
	h := newHarness(t, "/nonexistent")
	h.env.NoSpawn = true
	code, _ := h.run("board")
	if code != 1 || !strings.Contains(h.errb.String(), "daemon not running") {
		t.Fatalf("%d %s", code, h.errb.String())
	}
}

func TestBareWordsDumpSpawnDaemonAndSort(t *testing.T) {
	h := newHarness(t, fakeClaude(t))
	code, id := h.run("buy", "milk")
	if code != 0 || id == "" {
		t.Fatalf("%d %q %s", code, id, h.errb.String())
	}
	b := h.waitSorted(id)
	if b.Counts.TwoMin != 1 || b.Piles["2min"][0].Title != "Buy milk" || b.Piles["2min"][0].Reason != "quick errand" {
		t.Fatalf("%+v", b)
	}
	// Second command reuses the running daemon.
	code, out := h.run("board")
	if code != 0 || !strings.Contains(out, `"2min": 1`) {
		t.Fatalf("%d %s", code, out)
	}
	code, out = h.run("status")
	if code != 0 || !strings.Contains(out, "cost_usd") {
		t.Fatalf("%d %s", code, out)
	}
	// Raw text is on disk.
	data, _ := os.ReadFile(b.Piles["2min"][0].Attachments[0].Path)
	if string(data) != "buy milk" {
		t.Fatalf("%q", data)
	}
}

func TestStdinDump(t *testing.T) {
	h := newHarness(t, fakeClaude(t))
	h.env.Stdin = strings.NewReader("from a pipe\n")
	h.env.StdinIsPipe = func() bool { return true }
	code, id := h.run()
	if code != 0 || id == "" {
		t.Fatalf("%d %s", code, h.errb.String())
	}
	h.waitSorted(id)
}

func TestDumpWithFilesAndActions(t *testing.T) {
	h := newHarness(t, fakeClaude(t))
	f := filepath.Join(t.TempDir(), "receipt.pdf")
	os.WriteFile(f, []byte("pdf"), 0o600)
	code, id := h.run("dump", "-f", f, "expense", "receipt")
	if code != 0 {
		t.Fatal(h.errb.String())
	}
	b := h.waitSorted(id)
	if len(b.Piles["2min"][0].Attachments) != 2 {
		t.Fatalf("%+v", b.Piles["2min"][0].Attachments)
	}

	code, mid := h.run("move", id, "eventually", "not", "now")
	if code != 0 || mid == "" {
		t.Fatal(h.errb.String())
	}
	if code, _ := h.run("reason", mid, "changed my mind"); code != 0 {
		t.Fatal(h.errb.String())
	}
	if code, _ := h.run("edit", id, "-title", "Receipt"); code != 0 {
		t.Fatal(h.errb.String())
	}
	if code, _ := h.run("edit", id); code == 0 {
		t.Fatal("edit with nothing should fail")
	}
	if code, _ := h.run("deadline", id, "2030-05-01"); code != 0 {
		t.Fatal(h.errb.String())
	}
	code, out := h.run("board")
	var board core.Board
	json.Unmarshal([]byte(out), &board)
	if code != 0 || len(board.Piles["deadline"]) != 1 || board.Piles["deadline"][0].Title != "Receipt" {
		t.Fatalf("%s", out)
	}
	if code, _ := h.run("deadline", id); code != 0 {
		t.Fatal(h.errb.String())
	}
	g := filepath.Join(t.TempDir(), "more.txt")
	os.WriteFile(g, []byte("x"), 0o600)
	if code, _ := h.run("attach", id, g); code != 0 {
		t.Fatal(h.errb.String())
	}
	b = h.waitSorted(id)
	var attID string
	for _, items := range b.Piles {
		for _, it := range items {
			if it.ID == id {
				attID = it.Attachments[len(it.Attachments)-1].ID
			}
		}
	}
	if code, _ := h.run("detach", attID); code != 0 {
		t.Fatal(h.errb.String())
	}
	h.waitSorted(id)
	for _, cmd := range []string{"done", "archive", "reopen", "retry"} {
		if code, _ := h.run(cmd, id); code != 0 {
			t.Fatalf("%s: %s", cmd, h.errb.String())
		}
	}
	h.waitSorted(id)
	if code, _ := h.run("delete", id); code == 0 || !strings.Contains(h.errb.String(), "-y") {
		t.Fatal("delete must require -y")
	}
	if code, _ := h.run("delete", "-y", id); code != 0 {
		t.Fatal(h.errb.String())
	}
	if code, _ := h.run("done", id); code == 0 {
		t.Fatal("deleted item should be gone")
	}
}

func TestWatchStreamsUntilInterrupted(t *testing.T) {
	h := newHarness(t, fakeClaude(t))
	h.run("board")
	cmd := exec.Command(binPath, "watch")
	cmd.Env = os.Environ()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(stdout)
	var first core.Board
	if err := dec.Decode(&first); err != nil || first.Counts.TwoMin != 0 {
		t.Fatalf("first snapshot: %v %+v", err, first.Counts)
	}
	h.run("hello")
	deadline := time.Now().Add(10 * time.Second)
	for {
		var b core.Board
		if err := dec.Decode(&b); err != nil {
			t.Fatalf("stream ended: %v", err)
		}
		if b.Counts.TwoMin == 1 && !b.Piles["2min"][0].Processing {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("never saw the sorted item")
		}
	}
	cmd.Process.Signal(os.Interrupt)
	if err := cmd.Wait(); err != nil {
		t.Fatalf("watch should exit cleanly on interrupt: %v", err)
	}
}

func TestArgumentParsingEdges(t *testing.T) {
	h := newHarness(t, fakeClaude(t))
	h.env.NoSpawn = true
	cases := []struct {
		args    []string
		wantErr string
	}{
		{[]string{"board", "meeting", "notes"}, "takes no arguments"},
		{[]string{"done", "the", "dishes"}, "shedit dump done the dishes"},
		{[]string{"--version"}, "unknown flag"},
		{[]string{"delete", "abc"}, "-y"},
		{[]string{"edit", "abc"}, "usage: edit"},
	}
	for _, c := range cases {
		code, _ := h.run(c.args...)
		if code != 1 || !strings.Contains(h.errb.String(), c.wantErr) {
			t.Errorf("%v: code=%d err=%q want %q", c.args, code, h.errb.String(), c.wantErr)
		}
	}
	// These parse fine and reach the daemon (failing only because none runs).
	for _, args := range [][]string{
		{"dump", "done", "the", "dishes"},
		{"dump", "--", "-starts", "with", "dash"},
		{"delete", "abc", "-y"},
		{"delete", "-y", "abc"},
	} {
		h.run(args...)
		if !strings.Contains(h.errb.String(), "daemon not running") {
			t.Errorf("%v: parsed wrongly: %s", args, h.errb.String())
		}
	}
}

func TestDaemonRestartKeepsData(t *testing.T) {
	h := newHarness(t, fakeClaude(t))
	code, id := h.run("survives", "restart")
	if code != 0 {
		t.Fatal(h.errb.String())
	}
	h.waitSorted(id)
	exec.Command("pkill", "-TERM", "-f", binPath+" serve").Run()
	deadline := time.Now().Add(5 * time.Second)
	for (rpc.Client{Path: SocketPath()}).Ping() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	code, out := h.run("board")
	if code != 0 || !strings.Contains(out, id) {
		t.Fatalf("data lost across restart: %d %s %s", code, out, h.errb.String())
	}
}
