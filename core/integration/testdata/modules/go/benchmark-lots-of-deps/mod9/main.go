package main

import "context"

type Mod9 struct{}

func (m *Mod9) Fn(ctx context.Context) (string, error) {
	s := "mod9"
	var depS string
	_ = depS
	var err error
	_ = err
	depS, err = dag.Mod3().Fn(ctx)
	if err != nil {
		return "", err
	}
	s += depS
	depS, err = dag.Mod4().Fn(ctx)
	if err != nil {
		return "", err
	}
	s += depS
	depS, err = dag.Mod5().Fn(ctx)
	if err != nil {
		return "", err
	}
	s += depS
	return s, nil
}
