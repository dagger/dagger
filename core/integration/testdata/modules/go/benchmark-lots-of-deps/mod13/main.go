package main

import "context"

type Mod13 struct{}

func (m *Mod13) Fn(ctx context.Context) (string, error) {
	s := "mod13"
	var depS string
	_ = depS
	var err error
	_ = err
	depS, err = dag.Mod6().Fn(ctx)
	if err != nil {
		return "", err
	}
	s += depS
	depS, err = dag.Mod7().Fn(ctx)
	if err != nil {
		return "", err
	}
	s += depS
	depS, err = dag.Mod8().Fn(ctx)
	if err != nil {
		return "", err
	}
	s += depS
	depS, err = dag.Mod9().Fn(ctx)
	if err != nil {
		return "", err
	}
	s += depS
	return s, nil
}
