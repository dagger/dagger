package main

import (
	"dagger/test/internal/dagger"
)

type Test struct{}

func (t *Test) Call(
	// +ignore=[
	//   "foo.txt",
	//   "bar"
	// ]
	dir *dagger.Directory,
) *dagger.Directory {
	return dir
}
