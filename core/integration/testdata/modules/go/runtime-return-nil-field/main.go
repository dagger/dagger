package main

type Test struct {
	A *Thing
	B *Thing
}

type Thing struct{}

func New() *Test {
	return &Test{
		A: &Thing{},
	}
}

func (m *Test) Hello() string {
	return "Hello"
}
