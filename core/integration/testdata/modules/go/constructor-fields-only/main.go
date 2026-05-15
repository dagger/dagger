package main

import (
	"context"
)

func New(ctx context.Context) (Test, error) {
	v, err := dag.Container().From("alpine:3.22.1").File("/etc/alpine-release").Contents(ctx)
	if err != nil {
		return Test{}, err
	}
	return Test{
		AlpineVersion: v,
	}, nil
}

type Test struct {
	AlpineVersion string
}
