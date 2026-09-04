package model

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Claude runs prompts through the `claude` CLI in print mode. Each call is a
// fresh, sandboxed session: it runs in WorkDir (an empty scratch directory,
// so no CLAUDE.md, project settings or MCP servers from the user's own
// projects leak in), gets only the tools the request needs, and is cut off
// after Timeout.
type Claude struct {
	Binary  string
	WorkDir string
	Timeout time.Duration
}

const defaultTimeout = 5 * time.Minute

type claudeResult struct {
	IsError          bool            `json:"is_error"`
	Result           string          `json:"result"`
	StructuredOutput json.RawMessage `json:"structured_output"`
	TotalCostUSD     float64         `json:"total_cost_usd"`
}

func (c Claude) args(req Request) []string {
	args := []string{"-p", "--output-format", "json", "--restricted", "--strict-mcp-config"}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	if len(req.Schema) > 0 {
		args = append(args, "--json-schema", string(req.Schema))
	}
	var tools []string
	if len(req.Files) > 0 {
		for _, d := range dirsOf(req.Files) {
			args = append(args, "--add-dir", d)
		}
		tools = append(tools, "Read")
	}
	if req.AllowWeb {
		tools = append(tools, "WebFetch")
	}
	// Both flags are needed: --tools limits what exists, --allowedTools
	// pre-approves it so print mode never waits for a permission prompt.
	args = append(args, "--tools", strings.Join(tools, ","))
	if len(tools) > 0 {
		args = append(args, "--allowedTools", strings.Join(tools, ","))
	}
	return append(args, "--", req.Prompt)
}

func (c Claude) Run(ctx context.Context, req Request) (Response, error) {
	bin := c.Binary
	if bin == "" {
		bin = "claude"
	}
	timeout := c.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, c.args(req)...)
	cmd.Dir = c.WorkDir
	// claude spawns helpers; kill the whole process group on timeout, or a
	// grandchild holding stdout would keep Run blocked past the deadline.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return Response{}, fmt.Errorf("claude: %w", ctx.Err())
		}
		return Response{}, fmt.Errorf("claude: %w: %s", err, strings.TrimSpace(errb.String()))
	}
	var res claudeResult
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		return Response{}, fmt.Errorf("claude: bad output: %w", err)
	}
	if res.IsError {
		return Response{}, errors.New("claude: " + strings.TrimSpace(res.Result))
	}
	output := res.StructuredOutput
	if len(output) == 0 {
		output = json.RawMessage(res.Result)
	}
	if !json.Valid(output) {
		return Response{}, errors.New("claude: output is not JSON")
	}
	return Response{Output: output, CostUSD: res.TotalCostUSD, Model: req.Model}, nil
}

func dirsOf(files []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range files {
		d := filepath.Dir(f)
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out
}
