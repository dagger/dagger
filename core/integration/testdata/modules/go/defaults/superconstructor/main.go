// A module with a super big constructor, to test passing constructor args of all kinds
package main

import (
	"dagger/superconstructor/internal/dagger"
)

func New(
	password *dagger.Secret,
	greeting string,
	dir *dagger.Directory,
	file *dagger.File,
	// +default=42
	count int,
	service *dagger.Service,
) *Superconstructor {
	return &Superconstructor{
		Password: password,
		Greeting: greeting,
		Dir:      dir,
		File:     file,
		Count:    count,
		Service:  service,
	}
}

type Superconstructor struct {
	Password *dagger.Secret
	Greeting string
	Dir      *dagger.Directory
	File     *dagger.File
	Count    int
	Service  *dagger.Service
}
