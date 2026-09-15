package main

import (
	"context"
	"dagger/test/internal/dagger"
)

type Test struct{}

func (t *Test) TooHighRelativeDirPath(
	ctx context.Context,

	// +defaultPath="../../../"
	backend *dagger.Directory,
) ([]string, error) {
	// The engine should throw an error
	return []string{}, nil
}

func (t *Test) NonExistingPath(
	ctx context.Context,

	// +defaultPath="/invalid"
	dir *dagger.Directory,
) ([]string, error) {
	// The engine should throw an error
	return []string{}, nil
}

func (t *Test) TooHighRelativeFilePath(
	ctx context.Context,

	// +defaultPath="../../../file.txt"
	backend *dagger.File,
) (string, error) {
	// The engine should throw an error
	return "", nil
}

func (t *Test) NonExistingFile(
	ctx context.Context,

	// +defaultPath="/invalid"
	file *dagger.File,
) (string, error) {
	// The engine should throw an error
	return "", nil
}
