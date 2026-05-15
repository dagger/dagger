package main

type Minimal struct{}

func (m *Minimal) Hello(name string, opts struct{}, opts2 struct{}) string {
	return name
}
