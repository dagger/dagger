package main

import (
	"context"
	"errors"
)

type NestedDep struct{}

func (*NestedDep) FailingFunction(ctx context.Context) error {
	return errors.New("im a failing nested function")
}
