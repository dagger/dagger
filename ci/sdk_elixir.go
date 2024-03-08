package main

import (
	"context"
	"dagger/internal/dagger"
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/sync/errgroup"
)

const (
	elixirSDKPath            = "sdk/elixir"
	elixirSDKGeneratedPath   = elixirSDKPath + "/lib/dagger/gen"
	elixirSDKVersionFilePath = elixirSDKPath + "/lib/dagger/core/engine_conn.ex"
)

// https://hub.docker.com/r/hexpm/elixir/tags?page=1&name=debian-buster
var elixirVersions = []string{"1.14.5", "1.15.4"}

const (
	otpVersion    = "25.3.2.4"
	debianVersion = "20230612"
)

type ElixirSDK struct {
	Dagger *Dagger // +private
}

// Lint lints the Elixir SDK
func (t ElixirSDK) Lint(ctx context.Context) error {
	ctr, err := t.Dagger.installDagger(ctx, t.elixirBase(elixirVersions[1]), "sdk-elixir-lint")
	if err != nil {
		return err
	}

	_, err = ctr.
		WithExec([]string{"mix", "lint"}).
		Sync(ctx)
	return err
}

// Test tests the Elixir SDK
func (t ElixirSDK) Test(ctx context.Context) error {
	ctrs := []*dagger.Container{}
	for _, elixirVersion := range elixirVersions {
		ctrs = append(ctrs, t.elixirBase(elixirVersion))
	}
	ctrs, err := t.Dagger.installDaggers(ctx, ctrs, "sdk-elixir-test")
	if err != nil {
		return err
	}

	eg, ctx := errgroup.WithContext(ctx)
	for _, ctr := range ctrs {
		ctr := ctr
		eg.Go(func() error {
			_, err := ctr.
				WithExec([]string{"mix", "test"}).
				Sync(ctx)
			return err
		})
	}
	return eg.Wait()
}

// Generate re-generates the Elixir SDK API
func (t ElixirSDK) Generate(ctx context.Context) (*Directory, error) {
	ctr, err := t.Dagger.installDagger(ctx, t.elixirBase(elixirVersions[1]), "sdk-elixir-generate")
	if err != nil {
		return nil, err
	}

	generated := ctr.
		WithExec([]string{"mix", "dagger.gen"}).
		Directory(".")

	dir := dag.Directory().WithDirectory("sdk/elixir", generated)
	return dir, nil
}

// Publish publishes the Elixir SDK
func (t ElixirSDK) Publish(
	ctx context.Context,
	tag string,

	// +optional
	dryRun bool,
	// +optional
	hexAPIKey *dagger.Secret,
) error {
	var (
		version = strings.TrimPrefix(tag, "sdk/elixir/v")
		mixFile = "sdk/elixir/mix.exs"
	)

	ctr := t.elixirBase(elixirVersions[1])

	mixExs, err := t.Dagger.Source.File(mixFile).Contents(ctx)
	if err != nil {
		return err
	}
	newMixExs := strings.Replace(mixExs, `@version "0.0.0"`, `@version "`+version+`"`, 1)

	ctr = ctr.WithNewFile(mixFile, dagger.ContainerWithNewFileOpts{
		Contents: newMixExs,
	})

	args := []string{"mix", "hex.publish", "--yes"}
	if dryRun {
		args = append(args, "--dry-run")
	}

	_, err = ctr.
		WithSecretVariable("HEX_API_KEY", hexAPIKey).
		WithExec(args).
		Sync(ctx)

	return err
}

// Bump the Elixir SDK's Engine dependency
func (t ElixirSDK) Bump(ctx context.Context, version string) (*Directory, error) {
	contents, err := t.Dagger.Source.File(elixirSDKVersionFilePath).Contents(ctx)
	if err != nil {
		return nil, err
	}

	newVersion := fmt.Sprintf(`@dagger_cli_version "%s"`, strings.TrimPrefix(version, "v"))
	versionRe, err := regexp.Compile(`@dagger_cli_version "([0-9\.-a-zA-Z]+)"`)
	if err != nil {
		return nil, err
	}
	newContents := versionRe.ReplaceAllString(contents, newVersion)

	return dag.Directory().WithNewFile(elixirSDKVersionFilePath, newContents), nil
}

func (t ElixirSDK) elixirBase(elixirVersion string) *dagger.Container {
	src := t.Dagger.Source.Directory(elixirSDKPath)
	mountPath := "/" + elixirSDKPath

	return dag.Container().
		From(fmt.Sprintf("hexpm/elixir:%s-erlang-%s-debian-buster-%s-slim", elixirVersion, otpVersion, debianVersion)).
		WithWorkdir(mountPath).
		WithDirectory(mountPath, src).
		WithExec([]string{"mix", "local.hex", "--force"}).
		WithExec([]string{"mix", "local.rebar", "--force"}).
		WithExec([]string{"mix", "deps.get"})
}
