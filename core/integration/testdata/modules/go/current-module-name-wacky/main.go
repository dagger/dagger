package main

import "context"

type WaCkY struct{}

func (m *WaCkY) Fn(ctx context.Context) (string, error) {
	return dag.CurrentModule().Name(ctx)
}
