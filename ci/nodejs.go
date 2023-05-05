package main

import (
	"strings"

	"dagger.io/dagger"
	"golang.org/x/sync/errgroup"
)

type Nodejs struct {
	RepoSrcDir *dagger.Directory
	SDKSrcDir  *dagger.Directory
	Base       *dagger.Container
}

func (s SDK) Nodejs(ctx dagger.Context) (Nodejs, error) {
	sdkSrcDir := s.SrcDir.Directory("sdk/nodejs")

	base := ctx.Client().Container().
		// ⚠️  Keep this in sync with the engine version defined in package.json
		From("node:16-alpine").
		WithWorkdir("/workdir")

	deps := base.WithRootfs(
		base.
			Rootfs().
			WithFile("/workdir/package.json", sdkSrcDir.File("package.json")).
			WithFile("/workdir/yarn.lock", sdkSrcDir.File("yarn.lock")),
	).
		WithExec([]string{"yarn", "install"})

	src := deps.WithRootfs(
		deps.
			Rootfs().
			WithDirectory("/workdir", sdkSrcDir),
	)

	return Nodejs{
		RepoSrcDir: s.SrcDir,
		SDKSrcDir:  sdkSrcDir,
		Base:       src,
	}, nil
}

func (n Nodejs) Lint(ctx dagger.Context) (string, error) {
	// TODO: pipeline should be automatically set
	c := ctx.Client().Pipeline("sdk").Pipeline("nodejs").Pipeline("lint")

	eg, gctx := errgroup.WithContext(ctx)

	var yarnLintOut string
	eg.Go(func() error {
		var err error
		yarnLintOut, err = n.Base.
			WithExec([]string{"yarn", "lint"}).
			Stdout(gctx)
		return err
	})

	var docLintOut string
	eg.Go(func() error {
		snippets := c.Directory().
			WithDirectory("/", n.RepoSrcDir.Directory("docs/current/sdk/nodejs/snippets"))
		var err error
		docLintOut, err = n.Base.
			WithMountedDirectory("/snippets", snippets).
			WithWorkdir("/snippets").
			WithExec([]string{"yarn", "install"}).
			WithExec([]string{"yarn", "lint"}).
			Stdout(gctx)
		return err
	})

	// TODO: test generated code too

	return strings.Join([]string{
		yarnLintOut,
		docLintOut,
	}, "\n"), eg.Wait()
}
