package main

import (
	"context"
	"dagger/minimal/internal/dagger"
)

type Minimal struct{}

func (m *Minimal) Read(ctx context.Context, dir dagger.Directory) (string, error) {
	return dir.File("foo").Contents(ctx)
}

func (m *Minimal) ReadPointer(ctx context.Context, dir *dagger.Directory) (string, error) {
	return dir.File("foo").Contents(ctx)
}

func (m *Minimal) ReadSlice(ctx context.Context, dir []dagger.Directory) (string, error) {
	return dir[0].File("foo").Contents(ctx)
}

func (m *Minimal) ReadVariadic(ctx context.Context, dir ...dagger.Directory) (string, error) {
	return dir[0].File("foo").Contents(ctx)
}

func (m *Minimal) ReadOptional(
	ctx context.Context,
	dir *dagger.Directory, // +optional
) (string, error) {
	if dir != nil {
		return dir.File("foo").Contents(ctx)
	}
	return "", nil
}
