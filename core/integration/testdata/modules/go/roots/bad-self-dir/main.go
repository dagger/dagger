package main

type BadSelfDir struct{}

func (m *BadSelfDir) Hello() string {
	return "hello"
}
