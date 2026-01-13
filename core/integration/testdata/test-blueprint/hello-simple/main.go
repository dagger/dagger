package main

type HelloSimple struct{}

func (m *HelloSimple) Message() string {
	return "hello from simple"
}

func (m *HelloSimple) Goodbye() string {
	return "goodbye from simple"
}
