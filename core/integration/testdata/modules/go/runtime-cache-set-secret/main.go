package main

import (
	"crypto/rand"

	"dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) TestSetSecret() *dagger.Container {
	r := rand.Text()
	s := dag.SetSecret(r, r)
	return dag.Container().
		From("alpine:3.22.1").
		WithSecretVariable("TOP_SECRET", s)
}
