package main

import "dagger/util"

type Dagger struct {
	Source *Directory
}

func New(source *Directory) *Dagger {
	return &Dagger{
		Source: source,
	}
}

func (dagger *Dagger) repo() *util.Repository {
	// XXX: this is a really annoying hack
	// we should have a better way of consuming types from inside util packages
	return util.NewRepository(dagger.Source)
}
