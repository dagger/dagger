package main

type Minimal struct{}

type Foo struct {
	Bar bar
}

type bar struct{}

func (m *Minimal) Hello(name string) Foo {
	return Foo{}
}

func (f *Foo) Hello(name string) string {
	return name
}

func (b *bar) Hello(name string) string {
	return name
}
