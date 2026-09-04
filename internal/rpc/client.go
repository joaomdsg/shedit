package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"time"

	"github.com/joaomdsg/shedit/internal/core"
)

type Client struct {
	Path string
}

func (c Client) dial() (net.Conn, error) {
	return net.DialTimeout("unix", c.Path, time.Second)
}

func (c Client) Ping() bool {
	conn, err := c.dial()
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func (c Client) Call(cmd string, args any, out any) error {
	conn, err := c.dial()
	if err != nil {
		return err
	}
	defer conn.Close()
	var raw json.RawMessage
	if args != nil {
		raw, err = json.Marshal(args)
		if err != nil {
			return err
		}
	}
	if err := write(conn, Request{Cmd: cmd, Args: raw}); err != nil {
		return err
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return err
	}
	var res Response
	if err := json.Unmarshal(line, &res); err != nil {
		return err
	}
	if !res.OK {
		return errors.New(res.Error)
	}
	if out != nil && len(res.Result) > 0 {
		return json.Unmarshal(res.Result, out)
	}
	return nil
}

func (c Client) Watch(ctx context.Context, fn func(core.Board) error) error {
	conn, err := c.dial()
	if err != nil {
		return err
	}
	defer conn.Close()
	go func() {
		<-ctx.Done()
		conn.Close()
	}()
	if err := write(conn, Request{Cmd: "watch"}); err != nil {
		return err
	}
	r := bufio.NewReader(conn)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		var b core.Board
		if err := json.Unmarshal(line, &b); err != nil {
			return err
		}
		if err := fn(b); err != nil {
			return err
		}
	}
}
