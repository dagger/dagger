// Everything you need to develop the Dagger Engine
// https://dagger.io
package main

import (
	"context"
	"os"

	"github.com/dagger/dagger/.dagger/internal/dagger"
	"github.com/dagger/dagger/util/parallel"
)

// A dev environment for the DaggerDev Engine
type DaggerDev struct{}

// Verify that generated code is up to date
// +check
func (dev *DaggerDev) CheckGenerated(ctx context.Context) error {
	_, err := dev.Generate(ctx, true)
	return err
}

// Run all code generation - SDKs, docs, grpc stubs, changelog
func (dev *DaggerDev) Generate(ctx context.Context,
	// +optional
	check bool,
) (*dagger.Changeset, error) {
	var genDocs, genEngine, genChangelog, genGHA, genSDKs *dagger.Changeset
	maybeCheck := func(ctx context.Context, changes *dagger.Changeset) error {
		if !check {
			return nil
		}
		return assertNoChanges(ctx, changes, os.Stderr)
	}
	err := parallel.New().
		WithJob("docs", func(ctx context.Context) error {
			var err error
			genDocs, err = dev.Docs().Generate().Sync(ctx)
			if err != nil {
				return err
			}
			return maybeCheck(ctx, genDocs)
		}).
		WithJob("engine", func(ctx context.Context) error {
			var err error
			genEngine, err = dag.EngineDev().Generate().Sync(ctx)
			if err != nil {
				return err
			}
			return maybeCheck(ctx, genEngine)
		}).
		WithJob("changelog", func(ctx context.Context) error {
			var err error
			genChangelog, err = dag.Changelog().Generate().Sync(ctx)
			if err != nil {
				return err
			}
			return maybeCheck(ctx, genChangelog)
		}).
		WithJob("Github Actions config", func(ctx context.Context) error {
			// FIXME
			return nil
			//var err error
			//genGHA, err = dag.Ci().Generate().Sync(ctx)
			//if err != nil {
			//	return err
			//}
			//return maybeCheck(ctx, genGHA)
		}).
		WithJob("SDKs", func(ctx context.Context) error {
			var err error
			genSDKs, err = dag.Sdks().Generate().Sync(ctx)
			if err != nil {
				return err
			}
			return maybeCheck(ctx, genSDKs)
		}).
		Run(ctx)
	if err != nil {
		return nil, err
	}
	if check {
		return nil, nil
	}
	var result *dagger.Changeset
	// FIXME: this is a workaround to TUI being too noisy
	err = parallel.Run(ctx, "merge all changesets", func(ctx context.Context) error {
		var err error
		var gen []*dagger.Changeset
		gen = append(gen, genDocs, genEngine, genChangelog, genGHA, genSDKs)
		result, err = changesetMerge(gen...).Sync(ctx)
		return err
	})
	return result, err
}

func (dev *DaggerDev) Bump(ctx context.Context, version string) (*dagger.Changeset, error) {
	var (
		bumpDocs, bumpHelm, bumpSDKs *dagger.Changeset
	)
	err := parallel.New().
		WithJob("bump docs version", func(ctx context.Context) error {
			var err error
			bumpDocs, err = dag.DaggerDocs().Bump(version).Sync(ctx)
			return err
		}).
		WithJob("bump helm chart version", func(ctx context.Context) error {
			chartYaml, err := dag.Helm().SetVersion(version).Sync(ctx)
			if err != nil {
				return err
			}
			bumpHelm, err = dag.Directory().
				WithFile("helm/dagger/Chart.yaml", chartYaml).
				Changes(dag.Directory()).
				Sync(ctx)
			return err
		}).
		WithJob("bump SDK versions", func(ctx context.Context) error {
			var err error
			bumpSDKs, err = dag.Sdks().Bump(version).Sync(ctx)
			return err
		}).
		Run(ctx)
	if err != nil {
		return nil, err
	}
	return changesetMerge(bumpSDKs, bumpDocs, bumpHelm), nil
}

// "CI in CI": check that Dagger can still run its own CI
// Note: this doesn't actually call all CI checks: only a small subset,
// selected for maximum coverage of Dagger features with limited compute expenditure.
// The actual checks being performed is an implementation detail, and should NOT be relied on.
// In other words, don't skip running <foo> just because it happens to be run here!
//
// +check
func (dev *DaggerDev) CiInCi(
	ctx context.Context,
	// The Dagger repository to run CI against
	// +defaultPath="/"
	repo *dagger.GitRepository,
) error {
	source := repo.Head().Tree().WithChanges(repo.Uncommitted())
	engine := dag.EngineDev()
	cmd := []string{"dagger", "call"}
	if engine.ClientDockerConfig() != nil {
		cmd = append(cmd, "--docker-cfg=file:$HOME/.docker/config.json")
	}
	cmd = append(cmd, "test-sdks")
	_, err := dag.EngineDev().
		Playground().
		WithMountedDirectory("./dagger", source).
		WithWorkdir("./dagger").
		WithExec(cmd, dagger.ContainerWithExecOpts{Expand: true}).
		Sync(ctx)
	return err
}
