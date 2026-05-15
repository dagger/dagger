package main

import (
	"context"
	"dagger/test/internal/dagger"
)

type Test struct{}

func (t *Test) Dirs(
	ctx context.Context,

	// +defaultPath="/"
	root *dagger.Directory,

	// +defaultPath="."
	relativeRoot *dagger.Directory,
) ([]string, error) {
	res, err := root.Entries(ctx)
	if err != nil {
		return nil, err
	}
	relativeRes, err := relativeRoot.Entries(ctx)
	if err != nil {
		return nil, err
	}
	return append(res, relativeRes...), nil
}

func (t *Test) DirsIgnore(
	ctx context.Context,

	// +defaultPath="/"
	// +ignore=["**", "!backend", "!frontend"]
	root *dagger.Directory,

	// +defaultPath="."
	// +ignore=["dagger.json", "LICENSE"]
	relativeRoot *dagger.Directory,
) ([]string, error) {
	res, err := root.Entries(ctx)
	if err != nil {
		return nil, err
	}
	relativeRes, err := relativeRoot.Entries(ctx)
	if err != nil {
		return nil, err
	}
	return append(res, relativeRes...), nil
}

func (t *Test) RootDirPath(
	ctx context.Context,

	// +defaultPath="/backend"
	backend *dagger.Directory,

	// +defaultPath="/frontend"
	frontend *dagger.Directory,

	// +defaultPath="/ci/dagger/sub"
	modSrcDir *dagger.Directory,
) ([]string, error) {
	backendFiles, err := backend.Entries(ctx)
	if err != nil {
		return nil, err
	}
	frontendFiles, err := frontend.Entries(ctx)
	if err != nil {
		return nil, err
	}
	modSrcDirFiles, err := modSrcDir.Entries(ctx)
	if err != nil {
		return nil, err
	}

	res := append(backendFiles, append(frontendFiles, modSrcDirFiles...)...)

	return res, nil
}

func (t *Test) RelativeDirPath(
	ctx context.Context,

	// +defaultPath="./dagger/sub"
	modSrcDir *dagger.Directory,

	// +defaultPath="../backend"
	backend *dagger.Directory,
) ([]string, error) {
	modSrcDirFiles, err := modSrcDir.Entries(ctx)
	if err != nil {
		return nil, err
	}
	backendFiles, err := backend.Entries(ctx)
	if err != nil {
		return nil, err
	}

	return append(modSrcDirFiles, backendFiles...), nil
}

func (t *Test) Files(
	ctx context.Context,

	// +defaultPath="/ci/LICENSE"
	license *dagger.File,

	// +defaultPath="./dagger/sub/sub.txt"
	index *dagger.File,
) ([]string, error) {
	licenseName, err := license.Name(ctx)
	if err != nil {
		return nil, err
	}
	indexName, err := index.Name(ctx)
	if err != nil {
		return nil, err
	}

	return []string{licenseName, indexName}, nil
}
