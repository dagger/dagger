package main

import "context"

type Mod4 struct{}

func (m *Mod4) Fn(ctx context.Context) (string, error) {
	s := "mod4"
	var depS string
	_ = depS
	var err error
	_ = err
	depS, err = dag.Mod1().Fn(ctx)
	if err != nil {
		return "", err
	}
	s += depS
	depS, err = dag.Mod2().Fn(ctx)
	if err != nil {
		return "", err
	}
	s += depS
	return s, nil
}
