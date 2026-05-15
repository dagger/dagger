package main

import (
	"strconv"
	"time"

	"dagger/test/internal/dagger"
)

type Test struct{}

func (*Test) Rand(
	// +defaultPath="/crap"
	dir *dagger.Directory,
) string {
	return strconv.Itoa(int(time.Now().UnixNano()))
}
