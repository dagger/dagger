package main

import (
	"context"
)

type Caller struct{}

func (*Caller) Test(ctx context.Context) (string, error) {
	if err := dag.Secreter().CheckEmptyPlaintext(ctx, dag.SetSecret("FOO", "")); err != nil {
		return "", err
	}
	return "ok", nil
}
