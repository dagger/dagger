package mage

import (
	"context"
	"fmt"
	"os"
	"strings"

	"dagger.io/dagger"
	"github.com/magefile/mage/mg"
)

type Helm mg.Namespace

func (Helm) Publish(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	c = c.Pipeline("helm").Pipeline("publish")

	helmChart := c.Host().Directory("helm/dagger")

	env, ok := os.LookupEnv("GITHUB_TOKEN")
	if !ok {
		return fmt.Errorf("GITHUB_TOKEN is not set")
	}

	secret := c.SetSecret("GITHUB_TOKEN", env)

	helm := c.Container().From("alpine/helm:3.12.3").
		WithDirectory("/tmp/dagger-helm", helmChart).
		WithWorkdir("/tmp/dagger-helm")

	version, err := helm.WithExec([]string{"sh", "-c", "grep ^version Chart.yaml | cut -f 2 -d ' '"}, dagger.ContainerWithExecOpts{SkipEntrypoint: true}).Stdout(ctx)

	version = strings.TrimSpace(version)

	if err != nil {
		return err
	}

	loggedIn := helm.
		WithSecretVariable("GITHUB_TOKEN", secret).
		WithExec([]string{"sh", "-c", "helm registry login -u dagger ghcr.io/dagger --password $GITHUB_TOKEN"}, dagger.ContainerWithExecOpts{SkipEntrypoint: true})

	packaged := loggedIn.WithExec([]string{"package", "."})

	_, err = packaged.WithExec([]string{"push", "dagger-helm-" + version + ".tgz", "oci://ghcr.io/dagger"}).Sync(ctx)

	if err != nil {
		return err
	}

	fmt.Println("PUBLISHED HELM VERSION:", version)

	return nil
}
