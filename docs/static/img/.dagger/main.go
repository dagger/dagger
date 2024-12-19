package main

import "dagger/img/internal/dagger"

type Img struct{}

func (img *Img) Generate() *dagger.Directory {
	return dag.Directory().
		WithDirectory("current_docs/features", dag.Features().Recordings())
}
