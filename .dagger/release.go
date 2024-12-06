package main

import (
	"context"

	"github.com/dagger/dagger/.dagger/internal/dagger"
	"golang.org/x/sync/errgroup"
)

type Release struct {
	// +private
	SDK *SDK
	// +private
	Docs *Docs
}

// Bump the engine version used by all SDKs and the Helm chart
func (r *Release) Bump(ctx context.Context, version string) (*dagger.Directory, error) {
	var eg errgroup.Group

	var sdkDir *dagger.Directory
	var docsDir *dagger.Directory
	var helmFile *dagger.File

	eg.Go(func() error {
		var err error
		sdkDir, err = r.SDK.All().Bump(ctx, version)
		return err
	})

	eg.Go(func() error {
		var err error
		docsDir, err = r.Docs.Bump(version)
		return err
	})

	eg.Go(func() error {
		helmFile = dag.Helm().SetVersion(version)
		return nil
	})

	if err := eg.Wait(); err != nil {
		return nil, err
	}

	dir := dag.Directory().
		WithDirectory("", sdkDir).
		WithDirectory("", docsDir).
		WithFile("helm/dagger/Chart.yaml", helmFile)
	return dir, nil
}
