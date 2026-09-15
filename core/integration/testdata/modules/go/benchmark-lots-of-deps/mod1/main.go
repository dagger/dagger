package main

import "context"

type Mod1 struct{}

func (m *Mod1) Fn(ctx context.Context) (string, error) {
	s := "mod1"
	var depS string
	_ = depS
	var err error
	_ = err
	depS, err = dag.Mod0().Fn(ctx)
	if err != nil {
		return "", err
	}
	s += depS
	return s, nil
}
