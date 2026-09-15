package main

type Test struct{}

type Foo struct {
	MsgContainer Bar
}

type Bar struct {
	Msg string
}

func (m *Test) MyFunction() Foo {
	return Foo{MsgContainer: Bar{Msg: "hello world"}}
}
