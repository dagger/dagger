package main

import (
	"crypto/rand"

	"dagger/dep/internal/dagger"
)

type Dep struct{}

func (m *Dep) DepFn(s *dagger.Secret) string {
	return rand.Text()
}
