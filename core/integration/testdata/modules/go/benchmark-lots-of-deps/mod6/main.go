package main

import "context"

type Mod6 struct{}

func (m *Mod6) Fn(ctx context.Context) (string, error) {
	s := "mod6"
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
