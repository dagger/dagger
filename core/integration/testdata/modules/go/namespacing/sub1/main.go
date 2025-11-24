package main

type Sub1 struct{}

func (m *Sub1) Fn(s string) *Obj {
	return &Obj{Foo: "1:" + s}
}

type Obj struct {
	Foo string `json:"foo"`
}

//nolint:unparam
func (m *Obj) GetFoo() (string, error) {
	return m.Foo, nil
}
