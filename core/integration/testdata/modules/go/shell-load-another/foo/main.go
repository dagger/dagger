package main

func New() *Foo {
	return &Foo{
		Bar: "foobar",
	}
}

type Foo struct {
	Bar string
}
