package main

import (
	"dagger/dep/internal/dagger"
)

type Dep struct{}

func (m *Dep) GetSource(
	// +defaultPath="/dep"
	// +ignore=["**", "!yo"]
	source *dagger.Directory,
) *dagger.Directory {
	return source
}

func (m *Dep) GetRelSource(
	// +defaultPath="."
	// +ignore=["**", "!yo"]
	source *dagger.Directory,
) *dagger.Directory {
	return source
}
