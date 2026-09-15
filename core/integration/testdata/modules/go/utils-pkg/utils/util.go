package utils

import "dagger/minimal/internal/dagger"

func Foo() *dagger.Directory {
	return dagger.Connect().Directory().WithNewFile("/foo", "hello world")
}
