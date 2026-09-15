package main

type Test struct {
	Foo string

	Bar string // +private
}

func (m *Test) Set(foo string, bar string) *Test {
	m.Foo = foo
	m.Bar = bar
	return m
}

func (m *Test) Hello() string {
	return m.Foo + m.Bar
}
