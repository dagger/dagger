package main

import (
	"dagger/test/internal/dagger"
)

type Test struct{}

func (t *Test) IgnoreAll(
	// +ignore=["**"]
	// +defaultPath="./dagger"
	dir *dagger.Directory,
) *dagger.Directory {
	return dir
}

func (t *Test) IgnoreThenReverseIgnore(
	// +ignore=["**", "!**"]
	// +defaultPath="./dagger"
	dir *dagger.Directory,
) *dagger.Directory {
	return dir
}

func (t *Test) IgnoreThenReverseIgnoreThenExcludeGitFiles(
	// +ignore=["**", "!**", "*.git*"]
	// +defaultPath="./dagger"
	dir *dagger.Directory,
) *dagger.Directory {
	return dir
}

func (t *Test) IgnoreThenExcludeFilesThenReverseIgnore(
	// +ignore=["**", "*.git*", "!**"]
	// +defaultPath="./dagger"
	dir *dagger.Directory,
) *dagger.Directory {
	return dir
}

func (t *Test) IgnoreDir(
	// +ignore=["internal"]
	// +defaultPath="./dagger"
	dir *dagger.Directory,
) *dagger.Directory {
	return dir
}

func (t *Test) IgnoreEverythingButMainGo(
	// +ignore=["**", "!main.go"]
	// +defaultPath="./dagger"
	dir *dagger.Directory,
) *dagger.Directory {
	return dir
}

func (t *Test) NoIgnore(
	// +ignore=["!main.go"]
	// +defaultPath="./dagger"
	dir *dagger.Directory,
) *dagger.Directory {
	return dir
}

func (t *Test) IgnoreEveryGoFileExceptMainGo(
	// +ignore=["**/*.go", "!main.go"]
	// +defaultPath="./dagger"
	dir *dagger.Directory,
) *dagger.Directory {
	return dir
}

func (t *Test) IgnoreDirButKeepFileInSubdir(
	// +ignore=["internal/foo", "!internal/foo/bar.go"]
	// +defaultPath="./dagger"
	dir *dagger.Directory,
) *dagger.Directory {
	return dir
}
