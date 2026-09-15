package main

type Dep struct{}

type MyEnum string

const (
	MyEnumFalse MyEnum = "false"
	MyEnumTrue  MyEnum = "true"
	MyEnumNull  MyEnum = "null"
)

func (m *Dep) Thing(f MyEnum) MyEnum {
	return f
}
