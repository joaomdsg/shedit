// Package rpc exposes core over a Unix socket using one JSON object per
// line. A client sends a Request and gets one Response, except for "watch",
// which streams a board snapshot per line until the client hangs up.
package rpc

import "encoding/json"

type Request struct {
	Cmd  string          `json:"cmd"`
	Args json.RawMessage `json:"args,omitempty"`
}

type Response struct {
	OK     bool            `json:"ok"`
	Error  string          `json:"error,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
}

type DumpArgs struct {
	Text  string   `json:"text"`
	Files []string `json:"files"`
}

type IDArgs struct {
	ID string `json:"id"`
}

type MoveArgs struct {
	ID     string `json:"id"`
	Pile   string `json:"pile"`
	Reason string `json:"reason"`
}

type ReasonArgs struct {
	MoveID string `json:"move_id"`
	Reason string `json:"reason"`
}

type EditArgs struct {
	ID      string  `json:"id"`
	Title   *string `json:"title,omitempty"`
	Summary *string `json:"summary,omitempty"`
}

type DeadlineArgs struct {
	ID string `json:"id"`
	// Date is local wall-clock: YYYY-MM-DD (all-day) or "YYYY-MM-DD HH:MM";
	// empty clears.
	Date string `json:"date"`
}

type AttachArgs struct {
	ID    string   `json:"id"`
	Files []string `json:"files"`
}

type DetachArgs struct {
	AttachmentID string `json:"attachment_id"`
}

type Status struct {
	CostUSD float64 `json:"cost_usd"`
	Version string  `json:"version"`
}
