package main

import (
	"crypto/rand"

	"dagger/fresh/internal/dagger"
)

type Fresh struct {
	Stamp string
}

// New stamps every object with new contents.
// +cache="never"
func New() *Fresh {
	return &Fresh{Stamp: rand.Text()}
}

// Stamped returns a file with the object's stamp; the method itself is cached.
func (m *Fresh) Stamped() *dagger.File {
	return dag.Directory().WithNewFile("stamp", m.Stamp).File("stamp")
}
