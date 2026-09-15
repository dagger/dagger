package main

import (
	"strconv"
	"time"

	"dagger/secreter/internal/dagger"
)

type Secreter struct{}

func (_ *Secreter) Make() *dagger.Secret {
	return dag.SetSecret("FOO", strconv.Itoa(int(time.Now().UnixNano())))
}
