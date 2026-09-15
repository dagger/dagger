package main

import "context"

type Foo struct{}

func (f *Foo) Echo(ctx context.Context, ctx2 context.Context) (string, error) {
	return "", nil
}
