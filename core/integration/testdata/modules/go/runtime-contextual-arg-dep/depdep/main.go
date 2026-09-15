package main

import (
	"crypto/rand"

	"dagger/depdep/internal/dagger"
)

type Depdep struct{}

func (m *Depdep) Test(
	// +defaultPath="."
	dir *dagger.Directory,
) *dagger.Directory {
	return dir.WithNewFile("rand.txt", rand.Text())
}

func (m *Depdep) TestFile(
	// +defaultPath="dagger.json"
	f *dagger.File,
) *dagger.Directory {
	return dag.Directory().
		WithFile("dagger.json", f).
		WithNewFile("rand.txt", rand.Text())
}
