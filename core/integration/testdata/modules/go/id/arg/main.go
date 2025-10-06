package main

type Test struct{}

func (m *Test) Fn(id string) string {
	return id
}
