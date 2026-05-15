package main

type Test struct{}

func (test *Test) Hello() string {
	return "hello"
}

type DanglingObject struct{}

func (obj *DanglingObject) Hello(x DanglingIface) DanglingIface {
	return x
}

type DanglingIface interface {
	DoThing() error
}
