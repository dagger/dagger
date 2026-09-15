package main

type Dep struct{}

type Obj struct {
	Foo string
}

func (m *Dep) Fn() Obj {
	return Obj{Foo: "foo"}
}
