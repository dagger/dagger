package main

import (
	"context"
)

type Sub1 struct{}

func (m *Sub1) Fn(ctx context.Context, s string) *Obj {
	return &Obj{Foo: "1:" + s}
}

type Obj struct {
	Foo string `json:"foo"`
}

func (m *Obj) GetFoo(ctx context.Context) (string, error) {
	return m.Foo, nil
}
