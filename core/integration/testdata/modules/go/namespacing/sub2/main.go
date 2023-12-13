package main

import (
	"context"
)

type Sub2 struct{}

func (m *Sub2) Fn(ctx context.Context, s string) *Sub2Obj {
	return &Sub2Obj{Bar: "2:" + s}
}

type Sub2Obj struct {
	Bar string `json:"bar"`
}

func (m *Sub2Obj) GetBar(ctx context.Context) (string, error) {
	return m.Bar, nil
}
