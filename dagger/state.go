package dagger

import (
	"context"
	"os"

	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
)

type State struct {
	// Before last solve
	input llb.State
	// After last solve
	output bkgw.Reference
	// How to produce the output
	s Solver
}

func NewState(s Solver) *State {
	return &State{
		input: llb.Scratch(),
		s:     s,
	}
}

func (s *State) ReadFile(ctx context.Context, filename string) ([]byte, error) {
	if s.output == nil {
		return nil, os.ErrNotExist
	}
	return s.output.ReadFile(ctx, bkgw.ReadRequest{Filename: filename})
}

func (s *State) Change(ctx context.Context, op interface{}) error {
	input := s.input
	switch OP := op.(type) {
	case llb.State:
		input = OP
	case func(llb.State) llb.State:
		input = OP(input)
	}
	output, err := s.s.Solve(ctx, input)
	if err != nil {
		return err
	}
	s.input = input
	s.output = output
	return nil
}

func (s *State) LLB() llb.State {
	return s.input
}
