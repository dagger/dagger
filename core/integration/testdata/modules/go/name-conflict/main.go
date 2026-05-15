package main

type Minimal struct {
	Foo Foo
	Bar Bar
	Baz Baz
}

type Foo struct{}
type Bar struct{}
type Baz struct{}

func (m *Foo) Hello(name string) string {
	return name
}

func (f *Bar) Hello(name string, name2 string) string {
	return name + name2
}

func (b *Baz) Hello() (string, error) {
	return "", nil
}
