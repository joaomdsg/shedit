// Package cli implements the shedit command: `serve` runs the daemon, every
// other subcommand is a thin client over the Unix socket. When no daemon is
// running, client commands start one.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/joaomdsg/shedit/internal/blob"
	"github.com/joaomdsg/shedit/internal/core"
	"github.com/joaomdsg/shedit/internal/model"
	"github.com/joaomdsg/shedit/internal/rpc"
	"github.com/joaomdsg/shedit/internal/when"
)

const usage = `shedit — shed it from your mind

usage:
  shedit <text...>                 dump text; reads stdin when piped and no text given
  shedit dump [-f file]... [text]  dump text and/or files as one item
  shedit board                     print the board as JSON
  shedit watch                     stream board snapshots, one JSON per line
  shedit move <id> <pile> [reason] re-pile by hand (2min|deadline|eventually|unsorted)
  shedit reason <move-id> <text>   add a reason to a move
  shedit done|archive|reopen <id>
  shedit edit <id> [-title t] [-summary s]
  shedit deadline <id> [date]      set (YYYY-MM-DD or "YYYY-MM-DD HH:MM", local) or clear
  shedit attach <id> <file>...
  shedit detach <attachment-id>
  shedit retry <id>
  shedit delete <id> -y            remove the item and all its data
  shedit status
  shedit serve                     run the daemon in the foreground

Text starting with a subcommand name needs "dump": shedit dump done the dishes

env: SHEDIT_DATA, SHEDIT_SOCKET, SHEDIT_EXTRACT_MODEL, SHEDIT_SORT_MODEL, SHEDIT_CLAUDE_BIN
`

var errUsage = errors.New("usage")

type Env struct {
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
	Exe         string
	NoSpawn     bool
	StdinIsPipe func() bool
}

func Main(args []string, env Env) int {
	env = env.withDefaults()
	err := run(args, env)
	switch {
	case err == nil:
		return 0
	case errors.Is(err, errUsage):
		fmt.Fprint(env.Stdout, usage)
		return 0
	default:
		fmt.Fprintln(env.Stderr, "shedit:", err)
		return 1
	}
}

func (e Env) withDefaults() Env {
	if e.Stdout == nil {
		e.Stdout = os.Stdout
	}
	if e.Stderr == nil {
		e.Stderr = os.Stderr
	}
	if e.Stdin == nil {
		e.Stdin = os.Stdin
	}
	if e.StdinIsPipe == nil {
		e.StdinIsPipe = func() bool {
			fi, err := os.Stdin.Stat()
			return err == nil && fi.Mode()&os.ModeCharDevice == 0
		}
	}
	return e
}

var subcommands = map[string]bool{
	"dump": true, "board": true, "watch": true, "move": true, "reason": true, "done": true,
	"archive": true, "reopen": true, "edit": true, "deadline": true, "attach": true,
	"detach": true, "retry": true, "delete": true, "status": true, "serve": true, "help": true,
}

func run(args []string, env Env) error {
	if len(args) == 0 {
		if env.StdinIsPipe() {
			return dumpStdin(env, nil)
		}
		return errUsage
	}
	cmd, rest := args[0], args[1:]
	if strings.HasPrefix(cmd, "-") {
		if cmd == "-h" || cmd == "--help" {
			return errUsage
		}
		return fmt.Errorf("unknown flag %s (text starting with a dash needs: shedit dump -- %s)", cmd, strings.Join(args, " "))
	}
	if !subcommands[cmd] {
		return dump(env, strings.Join(args, " "), nil)
	}
	switch cmd {
	case "help":
		return errUsage
	case "serve":
		return serve(env)
	case "board", "status", "watch":
		if len(rest) != 0 {
			return fmt.Errorf("%s takes no arguments (to dump text starting with %q: shedit dump %s)", cmd, cmd, strings.Join(args, " "))
		}
		if cmd == "watch" {
			return watch(env)
		}
		var raw json.RawMessage
		if err := call(env, cmd, nil, &raw); err != nil {
			return err
		}
		return printJSON(env.Stdout, raw)
	case "dump":
		files, words := splitFlag(rest, "-f")
		text := strings.Join(words, " ")
		if text == "" && len(files) == 0 && env.StdinIsPipe() {
			return dumpStdin(env, files)
		}
		return dump(env, text, files)
	case "move":
		if len(rest) < 2 {
			return errors.New("usage: move <id> <pile> [reason]")
		}
		var out rpc.ReasonArgs
		if err := call(env, "move", rpc.MoveArgs{ID: rest[0], Pile: rest[1], Reason: strings.Join(rest[2:], " ")}, &out); err != nil {
			return err
		}
		fmt.Fprintln(env.Stdout, out.MoveID)
		return nil
	case "reason":
		if len(rest) < 2 {
			return errors.New("usage: reason <move-id> <text>")
		}
		return call(env, "reason", rpc.ReasonArgs{MoveID: rest[0], Reason: strings.Join(rest[1:], " ")}, nil)
	case "done", "archive", "reopen", "retry":
		if len(rest) != 1 {
			return fmt.Errorf("usage: %s <id> (to dump text starting with %q: shedit dump %s)", cmd, cmd, strings.Join(args, " "))
		}
		return call(env, cmd, rpc.IDArgs{ID: rest[0]}, nil)
	case "delete":
		yes, words := splitBool(rest, "-y")
		if len(words) != 1 {
			return errors.New("usage: delete <id> -y")
		}
		if !yes {
			return errors.New("delete is permanent; pass -y to confirm")
		}
		return call(env, "delete", rpc.IDArgs{ID: words[0]}, nil)
	case "edit":
		titles, rest := splitFlag(rest, "-title")
		summaries, rest := splitFlag(rest, "-summary")
		if len(rest) != 1 || len(titles) > 1 || len(summaries) > 1 || len(titles)+len(summaries) == 0 {
			return errors.New("usage: edit <id> [-title t] [-summary s]")
		}
		a := rpc.EditArgs{ID: rest[0]}
		if len(titles) == 1 {
			a.Title = &titles[0]
		}
		if len(summaries) == 1 {
			a.Summary = &summaries[0]
		}
		return call(env, "edit", a, nil)
	case "deadline":
		if len(rest) < 1 || len(rest) > 2 {
			return errors.New("usage: deadline <id> [date]")
		}
		a := rpc.DeadlineArgs{ID: rest[0]}
		if len(rest) == 2 {
			a.Date = rest[1]
		}
		return call(env, "deadline", a, nil)
	case "attach":
		if len(rest) < 2 {
			return errors.New("usage: attach <id> <file>...")
		}
		return call(env, "attach", rpc.AttachArgs{ID: rest[0], Files: absAll(rest[1:])}, nil)
	case "detach":
		if len(rest) != 1 {
			return errors.New("usage: detach <attachment-id>")
		}
		return call(env, "detach", rpc.DetachArgs{AttachmentID: rest[0]}, nil)
	}
	return errUsage
}

// splitFlag pulls every "<flag> value" pair out of args, wherever it sits,
// so `dump text -f file` and `dump -f file text` both work. A bare "--"
// ends flag parsing.
func splitFlag(args []string, flag string) (values, rest []string) {
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--":
			return values, append(rest, args[i+1:]...)
		case args[i] == flag && i+1 < len(args):
			values = append(values, args[i+1])
			i++
		default:
			rest = append(rest, args[i])
		}
	}
	return values, rest
}

func splitBool(args []string, flag string) (found bool, rest []string) {
	for _, a := range args {
		if a == flag {
			found = true
		} else {
			rest = append(rest, a)
		}
	}
	return found, rest
}

func absAll(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if a, err := filepath.Abs(p); err == nil {
			p = a
		}
		out = append(out, p)
	}
	return out
}

func dumpStdin(env Env, files []string) error {
	b, err := io.ReadAll(env.Stdin)
	if err != nil {
		return err
	}
	return dump(env, string(b), files)
}

func dump(env Env, text string, files []string) error {
	if strings.TrimSpace(text) == "" && len(files) == 0 {
		return errors.New("nothing to dump")
	}
	var out rpc.IDArgs
	if err := call(env, "dump", rpc.DumpArgs{Text: text, Files: absAll(files)}, &out); err != nil {
		return err
	}
	fmt.Fprintln(env.Stdout, out.ID)
	return nil
}

func printJSON(w io.Writer, raw json.RawMessage) error {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w, buf.String())
	return err
}

func watch(env Env) error {
	if err := ensureDaemon(env); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	enc := json.NewEncoder(env.Stdout)
	return rpc.Client{Path: SocketPath()}.Watch(ctx, func(b core.Board) error { return enc.Encode(b) })
}

func call(env Env, cmd string, args any, out any) error {
	if err := ensureDaemon(env); err != nil {
		return err
	}
	return rpc.Client{Path: SocketPath()}.Call(cmd, args, out)
}

// ensureDaemon starts `shedit serve` detached if nothing answers. Several
// clients may race here; rpc.Listen guarantees exactly one wins and the
// others exit, so we only have to wait for someone to answer.
func ensureDaemon(env Env) error {
	c := rpc.Client{Path: SocketPath()}
	if c.Ping() {
		return nil
	}
	if env.NoSpawn {
		return errors.New("daemon not running (start with `shedit serve`)")
	}
	exe := env.Exe
	if exe == "" {
		var err error
		if exe, err = os.Executable(); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(DataDir(), 0o700); err != nil {
		return err
	}
	logPath := filepath.Join(DataDir(), "daemon.log")
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(exe, "serve")
	cmd.Dir = DataDir()
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}
	cmd.Process.Release()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if c.Ping() {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("daemon did not come up; see %s", logPath)
}

func serve(env Env) error {
	logger := log.New(env.Stderr, "shedit ", log.LstdFlags)
	data := DataDir()
	scratch := filepath.Join(data, "scratch")
	if err := os.MkdirAll(scratch, 0o700); err != nil {
		return err
	}
	// Take the socket before touching the database, so a losing racer exits
	// without ever opening the store.
	l, err := rpc.Listen(SocketPath())
	if err != nil {
		return err
	}
	defer l.Close()

	st, err := openStore(filepath.Join(data, "shedit.db"))
	if err != nil {
		return err
	}
	defer st.Close()

	loc := when.UserLocation()
	svc := core.New(st, blob.New(data), model.Claude{Binary: os.Getenv("SHEDIT_CLAUDE_BIN"), WorkDir: scratch}, core.Options{
		ExtractModel: os.Getenv("SHEDIT_EXTRACT_MODEL"),
		SortModel:    os.Getenv("SHEDIT_SORT_MODEL"),
		Now:          func() time.Time { return time.Now().In(loc) },
		Logger:       logger,
	})
	if err := svc.Recover(); err != nil {
		return err
	}
	defer svc.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger.Printf("listening on %s, data in %s, zone %s", SocketPath(), data, loc)
	return rpc.NewServer(svc).Serve(ctx, l)
}
