package main

import (
	"crypto/rand"

	"dagger/dep/internal/dagger"
)

type Dep struct{}

func randomFile(name string) *dagger.File {
	return dag.Directory().WithNewFile(name, rand.Text()).File(name)
}

// Never returns a file with new contents on every call.
// +cache="never"
func (m *Dep) Never() *dagger.File {
	return randomFile("never")
}

// Cached returns the same file on every call.
func (m *Dep) Cached() *dagger.File {
	return randomFile("cached")
}
