package main

import "context"

type Dep struct{}

func (m *Dep) DepFn(ctx context.Context) string {
	return "hi from dep"
}

func (m *Dep) GetDepObj(ctx context.Context) *DepObj {
	return &DepObj{Str: "yo from dep"}
}

type DepObj struct {
	Str string
}

func (m *Dep) GetOtherObj(ctx context.Context) *OtherObj {
	return &OtherObj{Str: "hey from dep"}
}

type OtherObj struct {
	Str string
}
