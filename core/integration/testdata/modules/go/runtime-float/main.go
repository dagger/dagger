package main

import "context"

type Test struct{}

func (m *Test) Test(n float64) float64 {
	return n
}

func (m *Test) TestFloat32(n float32) float32 {
	return n
}

func (m *Test) Dep(ctx context.Context, n float64) (float64, error) {
	return dag.Dep().Dep(ctx, n)
}
