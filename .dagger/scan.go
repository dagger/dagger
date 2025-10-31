package main

import (
	"context"

	"github.com/dagger/dagger/.dagger/internal/dagger"
	"github.com/dagger/dagger/util/parallel"
)

// Scan source code and artifacts for security vulnerabilities
// +cache="session"
func (dev *DaggerDev) Scan(ctx context.Context) (MyCheckStatus, error) {
	ignoreFiles := dag.Directory().WithDirectory("/", dev.Source, dagger.DirectoryWithDirectoryOpts{
		Include: []string{
			".trivyignore",
			".trivyignore.yml",
			".trivyignore.yaml",
		},
	})
	ignoreFileNames, err := ignoreFiles.Entries(ctx)
	if err != nil {
		return CheckCompleted, err
	}

	ctr := dag.Container().
		From("aquasec/trivy:0.67.2@sha256:e2b22eac59c02003d8749f5b8d9bd073b62e30fefaef5b7c8371204e0a4b0c08").
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

	return CheckCompleted, parallel.New().
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
