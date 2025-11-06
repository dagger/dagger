package main

import (
	"context"
	"toolchains/security/internal/dagger"
)

// A toolchain for supply chain security of the Dagger project
type Security struct{}

// Common flags for the trivy CLI
var trivyOpts = []string{
	"--exit-code=1",
	"--severity=CRITICAL,HIGH",
}

// +check
// Scan the source repository for security vulnerabilities
func (sec *Security) ScanSource(
	ctx context.Context,
	// +defaultPath="/"
	source *dagger.Directory,
) error {
	_, err := trivyBase().
		WithMountedDirectory(".", source).
		WithExec([]string{
			"trivy", "fs",
			"--scanners=vuln",
			"--pkg-types=library",
			"--exit-code=1",
			"--severity=CRITICAL,HIGH",
			".",
		}).Sync(ctx)
	return err
}

// +check
// Build the engine container, and scan it for vulnerabilities
func (sec *Security) ScanEngineContainer(
	ctx context.Context,
	// +optional
	target *dagger.Container,
) error {
	if target == nil {
		target = dag.EngineDev().Container()
	}
	tarballPath := "target.tar"
	_, err := trivyBase().
		WithMountedFile(tarballPath, target.AsTarball()).
		WithExec([]string{
			"trivy", "image",
			"--pkg-types=os,library",
			"--exit-code=1",
			"--severity=CRITICAL,HIGH",
			"--input=" + tarballPath,
		}).
		Sync(ctx)
	return err
}

func trivyBase() *dagger.Container {
	return dag.Container().
		From("aquasec/trivy:0.67.2@sha256:e2b22eac59c02003d8749f5b8d9bd073b62e30fefaef5b7c8371204e0a4b0c08").
		WithMountedCache("/root/.cache/", dag.CacheVolume("trivy-cache"), dagger.ContainerWithMountedCacheOpts{
			Sharing: dagger.CacheSharingModeLocked,
		}).
		WithWorkdir("/home/trivy")
}
