package main

import "context"

type InvlaidDep struct{}

func (m *InvlaidDep) UseInvalid(ctx context.Context) error {
	return dag.Invalid().Invalid(ctx)
}
