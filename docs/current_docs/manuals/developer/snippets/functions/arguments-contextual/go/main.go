package main

import (
  "context"
  "dagger/my-module/internal/dagger"
)

type MyModule struct{}

func (m *MyModule) RepoFiles(
  ctx context.Context,

  // +defaultPath="/"
  repo *dagger.Directory,
) ([]string, error) {
  return repo.Entries(ctx)
}

func (m *MyModule) ModuleFiles(
  ctx context.Context,

  // +defaultPath="."
  module *dagger.Directory,
) ([]string, error) {
  return module.Entries(ctx)
}

func (m *MyModule) ReadMe(
  ctx context.Context,

  // +defaultPath="/README.md"
  readme *dagger.File,
) (string, error) {
  return readme.Contents(ctx)
}