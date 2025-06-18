package main

import (
	"context"
	"dagger/dep/internal/dagger"
	"errors"
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

// FailingFunction returns a simple error to test error origin stamping
func (*Dep) FailingFunction() error {
	return errors.New("this function always fails")
}

// FailingFunction returns a simple error to test error origin stamping
func (*Dep) BubblingFunction(ctx context.Context) error {
	return dag.NestedDep().FailingFunction(ctx)
}
