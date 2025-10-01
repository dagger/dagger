package main

import (
	"context"

	"github.com/dagger/dagger/.dagger/internal/dagger"
	"github.com/dagger/dagger/util/parallel"
)

// Scan source code and artifacts for security vulnerabilities
func (dev *DaggerDev) CheckScan(ctx context.Context) error {
	ignoreFiles := dag.Directory().WithDirectory("/", dev.Source, dagger.DirectoryWithDirectoryOpts{
		Include: []string{
			".trivyignore",
			".trivyignore.yml",
			".trivyignore.yaml",
		},
	})
	ignoreFileNames, err := ignoreFiles.Entries(ctx)
	if err != nil {
		return err
	}

	ctr := dag.Container().
		From("aquasec/trivy:0.65.0@sha256:a22415a38938a56c379387a8163fcb0ce38b10ace73e593475d3658d578b2436").
		WithMountedDirectory("/mnt/ignores", ignoreFiles).
		WithMountedCache("/root/.cache/", dag.CacheVolume("trivy-cache")).
		With(dev.withDockerCfg)

	commonArgs := []string{
		"--format=json",
		"--exit-code=1",
		"--severity=CRITICAL,HIGH",
		"--show-suppressed",
	}
	if len(ignoreFileNames) > 0 {
		commonArgs = append(commonArgs, "--ignorefile=/mnt/ignores/"+ignoreFileNames[0])
	}

	return parallel.New().
		WithJob("scan the source code", func(ctx context.Context) error {
			args := []string{
				"trivy",
				"fs",
				"--scanners=vuln",
				"--pkg-types=library",
			}
			args = append(args, commonArgs...)
			args = append(args, "/mnt/src")

			// HACK: filter out directories that present occasional issues
			src := dev.Source
			src = src.
				WithoutDirectory("docs").
				WithoutDirectory("sdk/rust/examples").
				WithoutDirectory("sdk/rust/crates/dagger-sdk/examples").
				WithoutDirectory("core/integration/testdata").
				WithoutDirectory("dagql/idtui/viztest")

			_, err := ctr.
				WithMountedDirectory("/mnt/src", src).
				WithExec(args).
				Sync(ctx)
			return err
		}).
		// this can catch dependencies that are only discoverable in the final build
		WithJob("scan the engine image", func(ctx context.Context) error {
			args := []string{
				"trivy",
				"image",
				"--pkg-types=os,library",
			}
			args = append(args, commonArgs...)
			engineTarball := "/mnt/engine.tar"
			args = append(args, "--input", engineTarball)

			target := dag.DaggerEngine().Container()
			_, err = ctr.
				WithMountedFile(engineTarball, target.AsTarball()).
				WithExec(args).
				Sync(ctx)
			return err
		}).
		Run(ctx)
}
