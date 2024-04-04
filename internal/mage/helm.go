package mage

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/magefile/mage/mg"

	"dagger.io/dagger"
)

type Helm mg.Namespace

func (Helm) Test(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	c = c.Pipeline("helm").Pipeline("test")
	helmChart := c.Host().Directory("helm/dagger")
	helm := c.Container().From("alpine/helm:3.12.3").
		WithDirectory("/tmp/dagger-helm", helmChart).
		WithWorkdir("/tmp/dagger-helm")

	_, err = helm.WithExec([]string{"lint"}).Sync(ctx)

	if err != nil {
		return err
	}

	_, err = helm.WithExec([]string{"lint", "--debug", "--namespace", "dagger", "--set", "magicache.token=hello-world", "--set", "magicache.enabled=true"}).Sync(ctx)

	if err != nil {
		return err
	}

	_, err = helm.WithExec([]string{"template", ".", "--debug", "--namespace", "dagger", "--set", "magicache.token=hello-world", "--set", "magicache.enabled=true"}).Sync(ctx)

	if err != nil {
		return err
	}

	return nil
}

func (Helm) Publish(ctx context.Context, tag string) error {
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

	version := strings.TrimPrefix(tag, "helm/chart/v")

	helm := c.Container().From("alpine/helm:3.12.3").
		WithDirectory("/tmp/dagger-helm", helmChart).
		WithWorkdir("/tmp/dagger-helm")

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
