package main

import "dagger/secreter/internal/dagger"

type Secreter struct{}

func (*Secreter) GiveBack(s *dagger.Secret) *dagger.Secret {
	return s
}
