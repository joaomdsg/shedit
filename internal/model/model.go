// Package model is the seam between shedit and the language model. Every
// model call goes through a Runner so tests can substitute a fake and the
// real implementation (headless Claude Code) can be swapped later.
package model

import (
	"context"
	"encoding/json"
)

type Request struct {
	Model  string
	Prompt string
	Schema json.RawMessage
	// Files the model may read. Their directories are exposed, so keep
	// attachments in per-item directories.
	Files    []string
	AllowWeb bool
}

type Response struct {
	Output  json.RawMessage
	CostUSD float64
	Model   string
}

type Runner interface {
	Run(ctx context.Context, req Request) (Response, error)
}
