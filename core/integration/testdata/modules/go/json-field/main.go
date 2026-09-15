package main

import (
	"dagger/minimal/internal/dagger"
)

type Minimal struct {
	Config dagger.JSON
}

func New() *Minimal {
	return &Minimal{
		Config: "{\"a\":1}",
	}
}
