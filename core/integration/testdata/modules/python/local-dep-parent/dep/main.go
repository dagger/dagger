package main

type Dep struct{}

func (m *Dep) Hello() string {
	return "hello"
}
