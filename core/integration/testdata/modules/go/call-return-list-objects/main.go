package main

type Minimal struct{}

type Foo struct {
	Bar int `json:"bar"`
}

func (m *Minimal) Fn() []*Foo {
	var foos []*Foo
	for i := 0; i < 3; i++ {
		foos = append(foos, &Foo{Bar: i})
	}
	return foos
}
