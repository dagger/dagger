package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/dagger/dagger/.dagger/internal/dagger"
	"github.com/dagger/dagger/util/parallel"
)

// Verify that generated code is up to date
func (dev *DaggerDev) CheckGenerated(ctx context.Context) (CheckStatus, error) {
	_, err := dev.Generate(ctx, true)
	return CheckCompleted, err
}

// Run all code generation - SDKs, docs, grpc stubs, changelog
func (dev *DaggerDev) Generate(ctx context.Context,
	// +optional
	check bool,
) (*dagger.Changeset, error) {
	var (
		genDocs, genEngine, genChangelog, genGHA *dagger.Changeset
		genSDKs                                  []*dagger.Changeset
	)
	maybeCheck := func(ctx context.Context, cs *dagger.Changeset) (*dagger.Changeset, error) {
		if !check {
			// Always use the context, for correct span attribution
			return cs.Sync(ctx)
		}
		diffSize, err := cs.AsPatch().Size(ctx)
		if err != nil {
			return nil, err
		}
		if diffSize > 0 {
			added, err := cs.AddedPaths(ctx)
			if err != nil {
				return nil, err
			}
			removed, err := cs.RemovedPaths(ctx)
			if err != nil {
				return nil, err
			}
			modified, err := cs.ModifiedPaths(ctx)
			if err != nil {
				return nil, err
			}
			fmt.Fprintf(os.Stderr, `%d MODIFIED:
%s

%d REMOVED:
%s

%d ADDED:
%s
`,
				len(modified), strings.Join(modified, "\n"),
				len(removed), strings.Join(removed, "\n"),
				len(added), strings.Join(added, "\n"),
			)
			return cs, errors.New("generated files are not up-to-date")
		}
		return cs, nil
	}
	verb := "generate "
	if check {
		verb += "& check "
	}
	err := parallel.New().
		WithJob(verb+"docs", func(ctx context.Context) error {
			var err error
			genDocs, err = maybeCheck(ctx, dag.Docs().Generate())
			return err
		}).
		WithJob(verb+"engine", func(ctx context.Context) error {
			var err error
			genEngine, err = maybeCheck(ctx, dag.DaggerEngine().Generate())
			return err
		}).
		WithJob(verb+"changelog", func(ctx context.Context) error {
			var err error
			genChangelog, err = maybeCheck(ctx, dag.Changelog().Generate())
			return err
		}).
		WithJob(verb+"Github Actions config", func(ctx context.Context) error {
			var err error
			genGHA, err = maybeCheck(ctx, dag.Ci().Generate())
			return err
		}).
		WithJob(verb+"SDKs", func(ctx context.Context) error {
			type generator interface {
				Name() string
				Generate(context.Context) (*dagger.Changeset, error)
			}
			generators := allSDKs[generator](dev)
			genSDKs = make([]*dagger.Changeset, len(generators))
			jobs := parallel.New()
			for i, sdk := range generators {
				jobs = jobs.WithJob(sdk.Name(), func(ctx context.Context) error {
					genSDK, err := sdk.Generate(ctx)
					if err != nil {
						return err
					}
					genSDKs[i], err = maybeCheck(ctx, genSDK)
					return err
				})
			}
			return jobs.Run(ctx)
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
		changes := slices.Clone(genSDKs)
		changes = append(changes, genDocs, genEngine, genChangelog, genGHA)
		result, err = changesetMerge(dev.Source, changes...).Sync(ctx)
		return err
	})
	return result, err
}
