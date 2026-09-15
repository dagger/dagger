package main

type Dep struct{}

func (m *Dep) Dep(n float64) float32 {
	return float32(n)
}
