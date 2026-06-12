package main

import (
	"dagger/test/internal/dagger"
)

type Test struct {
	Dirs []*dagger.Directory
}

func (m *Test) Nothing() (*dagger.Directory, error) {
	return nil, nil
}

func (m *Test) ListWithNothing() ([]*dagger.Directory, error) {
	return []*dagger.Directory{nil}, nil
}

func (m *Test) ObjsWithNothing() ([]*Test, error) {
	return []*Test{
		nil,
		{
			Dirs: []*dagger.Directory{nil},
		},
	}, nil
}
