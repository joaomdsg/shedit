package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/joaomdsg/shedit/internal/core"
	"github.com/joaomdsg/shedit/internal/when"
)

const Version = "0.1.0"

type Server struct {
	svc *core.Service
	wg  sync.WaitGroup
}

func NewServer(svc *core.Service) *Server { return &Server{svc: svc} }

// Listener owns the socket and the lock that proves this process is the
// daemon. Close releases both, removing the socket file only if it is ours.
type Listener struct {
	net.Listener
	lock *os.File
	path string
}

func (l *Listener) Close() error {
	err := l.Listener.Close()
	os.Remove(l.path)
	l.lock.Close()
	return err
}

// Listen becomes the daemon or fails. Two clients racing to spawn a daemon
// both get here; the lock file decides, and only the winner may unlink a
// stale socket. Without the lock the loser could delete the winner's live
// socket and leave it serving nothing.
func Listen(path string) (*Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, fmt.Errorf("another daemon owns %s", path)
	}
	os.Remove(path)
	l, err := net.Listen("unix", path)
	if err != nil {
		lock.Close()
		return nil, err
	}
	os.Chmod(path, 0o600)
	return &Listener{Listener: l, lock: lock, path: path}, nil
}

func (s *Server) Serve(ctx context.Context, l net.Listener) error {
	go func() {
		<-ctx.Done()
		l.Close()
	}()
	for {
		conn, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				s.wg.Wait()
				return nil
			}
			return err
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handle(ctx, conn)
		}()
	}
}

func write(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = w.Write(b)
	return err
}

func (s *Server) handle(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	line, err := r.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return
	}
	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		write(conn, Response{Error: "bad request: " + err.Error()})
		return
	}
	if req.Cmd == "watch" {
		s.watch(ctx, conn)
		return
	}
	res, err := s.dispatch(ctx, req)
	if err != nil {
		write(conn, Response{Error: err.Error()})
		return
	}
	b, _ := json.Marshal(res)
	write(conn, Response{OK: true, Result: b})
}

func (s *Server) watch(ctx context.Context, conn net.Conn) {
	ch, cancel, err := s.svc.Subscribe()
	if err != nil {
		write(conn, Response{Error: err.Error()})
		return
	}
	defer cancel()
	gone := make(chan struct{})
	go func() {
		// Any read (EOF included) means the client left.
		io.Copy(io.Discard, conn)
		close(gone)
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-gone:
			return
		case b := <-ch:
			if err := write(conn, b); err != nil {
				return
			}
		}
	}
}

func decode[T any](raw json.RawMessage) (T, error) {
	var v T
	if len(raw) == 0 {
		return v, nil
	}
	err := json.Unmarshal(raw, &v)
	return v, err
}

func openFiles(paths []string) ([]core.FileInput, func(), error) {
	var files []core.FileInput
	var closers []io.Closer
	cleanup := func() {
		for _, c := range closers {
			c.Close()
		}
	}
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			cleanup()
			return nil, nil, err
		}
		closers = append(closers, f)
		files = append(files, core.FileInput{Name: filepath.Base(p), Reader: f})
	}
	return files, cleanup, nil
}

func parseDate(s string, loc *time.Location) (when.When, error) {
	if w, ok := when.Parse(strings.TrimSpace(s), loc); ok {
		return w, nil
	}
	return when.When{}, fmt.Errorf("bad date %q (use YYYY-MM-DD or \"YYYY-MM-DD HH:MM\")", s)
}

func (s *Server) dispatch(ctx context.Context, req Request) (any, error) {
	switch req.Cmd {
	case "board":
		return s.svc.Snapshot()
	case "status":
		return Status{CostUSD: s.svc.CostUSD(), Version: Version}, nil
	case "dump":
		a, err := decode[DumpArgs](req.Args)
		if err != nil {
			return nil, err
		}
		files, cleanup, err := openFiles(a.Files)
		if err != nil {
			return nil, err
		}
		defer cleanup()
		id, err := s.svc.Dump(ctx, core.DumpInput{Text: a.Text, Files: files})
		if err != nil {
			return nil, err
		}
		return IDArgs{ID: id}, nil
	case "move":
		a, err := decode[MoveArgs](req.Args)
		if err != nil {
			return nil, err
		}
		mid, err := s.svc.Move(a.ID, a.Pile, a.Reason)
		if err != nil {
			return nil, err
		}
		return ReasonArgs{MoveID: mid}, nil
	case "reason":
		a, err := decode[ReasonArgs](req.Args)
		if err != nil {
			return nil, err
		}
		return nil, s.svc.SetMoveReason(a.MoveID, a.Reason)
	case "done", "archive", "reopen", "retry", "delete":
		a, err := decode[IDArgs](req.Args)
		if err != nil {
			return nil, err
		}
		switch req.Cmd {
		case "done":
			return nil, s.svc.Done(a.ID)
		case "archive":
			return nil, s.svc.Archive(a.ID)
		case "reopen":
			return nil, s.svc.Reopen(a.ID)
		case "retry":
			return nil, s.svc.Retry(ctx, a.ID)
		default:
			return nil, s.svc.Delete(a.ID)
		}
	case "edit":
		a, err := decode[EditArgs](req.Args)
		if err != nil {
			return nil, err
		}
		return nil, s.svc.Edit(a.ID, core.Edit{Title: a.Title, Summary: a.Summary})
	case "deadline":
		a, err := decode[DeadlineArgs](req.Args)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(a.Date) == "" {
			return nil, s.svc.ClearDeadline(a.ID)
		}
		d, err := parseDate(a.Date, s.svc.Location())
		if err != nil {
			return nil, err
		}
		return nil, s.svc.SetDeadline(a.ID, d)
	case "attach":
		a, err := decode[AttachArgs](req.Args)
		if err != nil {
			return nil, err
		}
		files, cleanup, err := openFiles(a.Files)
		if err != nil {
			return nil, err
		}
		defer cleanup()
		return nil, s.svc.Attach(ctx, a.ID, files)
	case "detach":
		a, err := decode[DetachArgs](req.Args)
		if err != nil {
			return nil, err
		}
		return nil, s.svc.Detach(ctx, a.AttachmentID)
	}
	return nil, errors.New("unknown command " + req.Cmd)
}
