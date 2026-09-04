package model

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
)

type Fake struct {
	mu        sync.Mutex
	Responses []json.RawMessage
	Errors    []error
	Requests  []Request
}

func (f *Fake) Queue(v any) *Fake {
	b, _ := json.Marshal(v)
	f.mu.Lock()
	f.Responses = append(f.Responses, b)
	f.Errors = append(f.Errors, nil)
	f.mu.Unlock()
	return f
}

func (f *Fake) QueueError(err error) *Fake {
	f.mu.Lock()
	f.Responses = append(f.Responses, nil)
	f.Errors = append(f.Errors, err)
	f.mu.Unlock()
	return f
}

func (f *Fake) Run(ctx context.Context, req Request) (Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Requests = append(f.Requests, req)
	if len(f.Responses) == 0 {
		return Response{}, errors.New("fake: no scripted response")
	}
	out, err := f.Responses[0], f.Errors[0]
	f.Responses, f.Errors = f.Responses[1:], f.Errors[1:]
	if err != nil {
		return Response{}, err
	}
	return Response{Output: out, CostUSD: 0.001, Model: req.Model}, nil
}
