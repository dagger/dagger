package main

type BadSiblingDep struct{}

func (m *BadSiblingDep) Hello() string {
	return "hello"
}
