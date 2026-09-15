package main

import "context"

type Mod0 struct{}

func (m *Mod0) Fn(ctx context.Context) (string, error) {
	s := "mod0"
	var depS string
	_ = depS
	var err error
	_ = err
	return s, nil
}
