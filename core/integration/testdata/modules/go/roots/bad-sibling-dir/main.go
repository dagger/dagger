package main

type BadSiblingDir struct{}

func (m *BadSiblingDir) Hello() string {
	return "hello"
}
