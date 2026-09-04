package model

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeClaude writes a script that records argv and cwd, then prints stdout.
func fakeClaude(t *testing.T, stdout string, exit int) (bin, recordDir string) {
	t.Helper()
	recordDir = t.TempDir()
	bin = filepath.Join(recordDir, "claude")
	script := "#!/bin/sh\npwd > " + recordDir + "/cwd\nprintf '%s\\n' \"$@\" > " + recordDir + "/args\ncat <<'JSON'\n" + stdout + "\nJSON\nexit " + string(rune('0'+exit)) + "\n"
	os.WriteFile(bin, []byte(script), 0o755)
	return
}

func recorded(t *testing.T, dir, name string) string {
	b, _ := os.ReadFile(filepath.Join(dir, name))
	return strings.TrimSpace(string(b))
}

const okResult = `{"is_error":false,"result":"{\"title\":\"x\"}","structured_output":{"title":"x"},"total_cost_usd":0.02}`

func TestRunIsSandboxedAndGrantsOnlyNeededTools(t *testing.T) {
	bin, rec := fakeClaude(t, okResult, 0)
	work := t.TempDir()
	res, err := Claude{Binary: bin, WorkDir: work}.Run(context.Background(), Request{
		Model:    "sonnet",
		Prompt:   "hello",
		Schema:   json.RawMessage(`{"type":"object"}`),
		Files:    []string{"/data/a/1.txt", "/data/a/2.png", "/data/b/x.pdf"},
		AllowWeb: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Output) != `{"title":"x"}` || res.CostUSD != 0.02 {
		t.Fatalf("%+v", res)
	}
	if got := recorded(t, rec, "cwd"); got != work {
		t.Fatalf("ran in %q, want scratch dir %q", got, work)
	}
	args := recorded(t, rec, "args")
	for _, want := range []string{"--restricted", "--strict-mcp-config", "--model\nsonnet", "--json-schema\n{\"type\":\"object\"}", "--add-dir\n/data/a", "--add-dir\n/data/b", "--tools\nRead,WebFetch", "--allowedTools\nRead,WebFetch", "--\nhello"} {
		if !strings.Contains(args, want) {
			t.Errorf("args missing %q in:\n%s", want, args)
		}
	}
	if strings.Count(args, "--add-dir") != 2 {
		t.Errorf("dirs not deduped:\n%s", args)
	}
}

func TestRunWithoutFilesDisablesAllTools(t *testing.T) {
	bin, rec := fakeClaude(t, okResult, 0)
	Claude{Binary: bin}.Run(context.Background(), Request{Prompt: "p"})
	args := recorded(t, rec, "args")
	if !strings.Contains(args, "--tools\n\n") || strings.Contains(args, "--allowedTools") || strings.Contains(args, "--add-dir") {
		t.Fatalf("tools not fully disabled:\n%s", args)
	}
}

func TestRunWebOnlyGrantsNoRead(t *testing.T) {
	bin, rec := fakeClaude(t, okResult, 0)
	Claude{Binary: bin}.Run(context.Background(), Request{Prompt: "p", AllowWeb: true})
	args := recorded(t, rec, "args")
	if !strings.Contains(args, "--tools\nWebFetch") || strings.Contains(args, "Read") {
		t.Fatalf("%s", args)
	}
}

func TestRunTimesOut(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	os.WriteFile(bin, []byte("#!/bin/sh\nsleep 5\n"), 0o755)
	start := time.Now()
	_, err := Claude{Binary: bin, Timeout: 100 * time.Millisecond}.Run(context.Background(), Request{Prompt: "p"})
	if err == nil || !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("err=%v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("did not cut off the process")
	}
}

func TestRunReportsModelError(t *testing.T) {
	bin, _ := fakeClaude(t, `{"is_error":true,"result":"rate limited"}`, 0)
	_, err := Claude{Binary: bin}.Run(context.Background(), Request{Prompt: "p"})
	if err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("err=%v", err)
	}
}

func TestRunReportsExitFailure(t *testing.T) {
	bin, _ := fakeClaude(t, `boom`, 1)
	if _, err := (Claude{Binary: bin}).Run(context.Background(), Request{Prompt: "p"}); err == nil {
		t.Fatal("want error")
	}
}

func TestRunRejectsNonJSON(t *testing.T) {
	bin, _ := fakeClaude(t, `{"is_error":false,"result":"not json"}`, 0)
	if _, err := (Claude{Binary: bin}).Run(context.Background(), Request{Prompt: "p"}); err == nil {
		t.Fatal("want error")
	}
}

func TestFakeConsumesInOrderAndRecords(t *testing.T) {
	f := (&Fake{}).Queue(map[string]int{"a": 1}).Queue(map[string]int{"b": 2})
	r1, _ := f.Run(context.Background(), Request{Prompt: "one"})
	r2, _ := f.Run(context.Background(), Request{Prompt: "two"})
	if string(r1.Output) != `{"a":1}` || string(r2.Output) != `{"b":2}` {
		t.Fatalf("%s %s", r1.Output, r2.Output)
	}
	if len(f.Requests) != 2 || f.Requests[1].Prompt != "two" {
		t.Fatalf("%+v", f.Requests)
	}
	if _, err := f.Run(context.Background(), Request{}); err == nil {
		t.Fatal("want error when exhausted")
	}
}
