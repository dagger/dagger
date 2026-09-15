package main

import (
	"context"
	"os"

	"dagger/test/internal/dagger"
)

func New() (*Test, error) {
	if err := os.WriteFile("/rootfile.txt", []byte("notnice"), 0o644); err != nil {
		return nil, err
	}
	if err := os.MkdirAll("/foo", 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile("/foo/foofile.txt", []byte("notnice"), 0o644); err != nil {
		return nil, err
	}

	return &Test{}, nil
}

type Test struct{}

func (m *Test) GeneratedContextDirectory(ctx context.Context) *dagger.Directory {
	return dag.CurrentModule().GeneratedContextDirectory()
}

func (m *Test) SourceFile(ctx context.Context) *dagger.File {
	return dag.CurrentModule().Source().File("subdir/coolfile.txt")
}

func (m *Test) WorkdirDir(ctx context.Context) (*dagger.Directory, error) {
	if err := os.MkdirAll("subdir/moresubdir", 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile("subdir/moresubdir/coolfile.txt", []byte("nice"), 0o644); err != nil {
		return nil, err
	}
	return dag.CurrentModule().Workdir("subdir/moresubdir"), nil
}

func (m *Test) WorkdirFile(ctx context.Context) (*dagger.File, error) {
	if err := os.MkdirAll("subdir/moresubdir", 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile("subdir/moresubdir/coolfile.txt", []byte("nice"), 0o644); err != nil {
		return nil, err
	}
	return dag.CurrentModule().WorkdirFile("subdir/moresubdir/coolfile.txt"), nil
}

func (m *Test) EscapeFile(ctx context.Context) *dagger.File {
	return dag.CurrentModule().WorkdirFile("../rootfile.txt")
}

func (m *Test) EscapeFileAbs(ctx context.Context) *dagger.File {
	return dag.CurrentModule().WorkdirFile("/rootfile.txt")
}

func (m *Test) EscapeDir(ctx context.Context) *dagger.Directory {
	return dag.CurrentModule().Workdir("../foo")
}

func (m *Test) EscapeDirAbs(ctx context.Context) *dagger.Directory {
	return dag.CurrentModule().Workdir("/foo")
}
