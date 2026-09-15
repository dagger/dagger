package main

import "context"

type Test struct{}

func (m *Test) DirectoryID(ctx context.Context) (string, error) {
	id, err := dag.Directory().WithNewFile("foo", "bar").ID(ctx)
	return string(id), err
}
