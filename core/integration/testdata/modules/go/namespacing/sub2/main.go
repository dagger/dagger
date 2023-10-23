package main

import (
	"context"
)

type Sub2 struct{}

func (m *Sub2) Fn(ctx context.Context, s string) *Obj {
	return &Obj{Bar: "2:" + s}
}

type Obj struct {
	Bar string `json:"bar"`
}

func (m *Obj) GetBar(ctx context.Context) (string, error) {
	return m.Bar, nil
}
