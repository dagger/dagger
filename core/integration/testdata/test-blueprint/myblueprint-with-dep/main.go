package main

import (
	"context"
)

type MyblueprintWithDep struct{}

func (m *MyblueprintWithDep) Hello(ctx context.Context) (string, error) {
	return dag.Hello().Message(ctx)
}
