package main

import "dagger/secreter/internal/dagger"

type Secreter struct{}

func (_ *Secreter) Make(uniq string) *dagger.Secret {
	return dag.SetSecret("MY_SECRET", uniq)
}
