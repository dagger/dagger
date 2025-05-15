package main

import (
	"context"
	"dagger/dep/internal/dagger"
)

type Dep struct{}

func (*Dep) GetFiles() ([]*dagger.File, error) {
	return []*dagger.File{
		dag.File("a", "AAA"),
		dag.File("b", "BBB"),
		dag.File("c", "CCC"),
	}, nil
}

func (*Dep) FileContents(ctx context.Context, files []*dagger.File) (string, error) {
	var s string
	for _, f := range files {
		c, err := f.Contents(ctx)
		if err != nil {
			return "", err
		}
		s += c
	}
	return s, nil
}
