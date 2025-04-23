package main

type Broken struct{}

func (m *Broken) Broken() {
	_ = ctx
}
