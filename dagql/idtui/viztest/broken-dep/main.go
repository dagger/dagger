package main

import "context"

type BrokenDep struct{}

func (m *BrokenDep) UseBroken(ctx context.Context) error {
	return dag.Broken().Broken(ctx)
}
