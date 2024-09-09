package main

import (
	"context"
	"dagger/my-module/internal/dagger"
)

type MyModule struct{}

type Files struct {
  RepoFiles []string
  ModuleFiles []string
  ReadmeContent string
}

func (m *MyModule) Example(
	ctx context.Context,

	// +defaultPath="/"
	repo *dagger.Directory,

	// +defaultPath="."
	moduleDir *dagger.Directory,

	// +defaultPath="/README.md"
	readme *dagger.File,
) (*Files, error) {
	repoFiles, err := repo.Entries(ctx)
  if err != nil {
    return nil, err
  }

  moduleFiles, err := moduleDir.Entries(ctx)
  if err != nil {
    return nil, err
  }

  readmeContent, err := readme.Contents(ctx)
  if err != nil {
    return nil, err
  }
  
	return &Files{
		RepoFiles: repoFiles,
		ModuleFiles: moduleFiles,
		ReadmeContent: readmeContent,
	}, nil
}
