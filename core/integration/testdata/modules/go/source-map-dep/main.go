package main

import "context"

type Dep struct {
	FieldDef string
}

func (m *Dep) FuncDef(
	arg1 string,
	arg2 string, // +optional
) string {
	return ""
}

type MyEnum string

const (
	MyEnumA MyEnum = "MyEnumA"
	MyEnumB MyEnum = "MyEnumB"
)

type MyInterface interface {
	DaggerObject
	Do(ctx context.Context, val int) (string, error)
}

func (m *Dep) Collect(MyEnum, MyInterface) error {
	// force all the types here to be collected
	return nil
}
