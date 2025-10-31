package main

import (
	"context"

	"github.com/dagger/dagger/.dagger/internal/dagger"
)

// Dagger docs toolchain
func (dev *DaggerDev) Docs() *Docs {
	return &Docs{}
}

// Dagger docs toolchain
type Docs struct{}

// Lint the documentation
func (d *Docs) Lint(ctx context.Context) (MyCheckStatus, error) {
	_, err := dag.DaggerDocs().Lint(ctx)
	return CheckCompleted, err
}

// Generate the documentation
func (d *Docs) Generate() *dagger.Changeset {
	return dag.DaggerDocs().Generate()
}

// Publish the documentation
func (d *Docs) Publish(ctx context.Context, netlifyToken *dagger.Secret) error {
	return dag.DaggerDocs().Publish(ctx, netlifyToken)
}

// Bump the documentation version
func (d *Docs) Bump(version string) *dagger.Changeset {
	return dag.DaggerDocs().Bump(version)
}
