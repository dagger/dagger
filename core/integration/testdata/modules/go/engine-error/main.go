package main

import "context"

type Test struct{}

func (m *Test) Fn(ctx context.Context) error {
	_, _ = dag.Engine().LocalCache().EntrySet().Entries(ctx)
	return nil
}
