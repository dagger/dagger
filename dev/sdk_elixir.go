package main

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/dev/internal/dagger"
)

const (
	elixirSDKPath            = "sdk/elixir"
	elixirSDKGeneratedPath   = elixirSDKPath + "/lib/dagger/gen"
	elixirSDKVersionFilePath = elixirSDKPath + "/lib/dagger/core/engine_conn.ex"
)

// https://hub.docker.com/r/hexpm/elixir/tags?page=1&name=debian-buster
var elixirVersions = []string{"1.16.2", "1.15.7", "1.14.5"}

const (
	otpVersion    = "26.2.4"
	debianVersion = "20240423"
)

type ElixirSDK struct {
	Dagger *DaggerDev // +private
}

// Lint the Elixir SDK
func (t ElixirSDK) Lint(ctx context.Context) error {
	installer, err := t.Dagger.installer(ctx, "sdk-elixir-lint")
	if err != nil {
		return err
	}

	_, err = t.elixirBase(elixirVersions[0]).
		With(installer).
		WithExec([]string{"mix", "lint"}).
		Sync(ctx)
	return err
}

// Test the Elixir SDK
func (t ElixirSDK) Test(ctx context.Context) error {
	installer, err := t.Dagger.installer(ctx, "sdk-elixir-test")
	if err != nil {
		return err
	}

	eg, ctx := errgroup.WithContext(ctx)
	for _, elixirVersion := range elixirVersions {
		ctr := t.elixirBase(elixirVersion).With(installer)

		eg.Go(func() error {
			_, err := ctr.
				WithExec([]string{"mix", "test"}).
				Sync(ctx)
			return err
		})
	}
	return eg.Wait()
}

// Regenerate the Elixir SDK API
func (t ElixirSDK) Generate(ctx context.Context) (*Directory, error) {
	installer, err := t.Dagger.installer(ctx, "sdk-elixir-generate")
	if err != nil {
		return nil, err
	}
	introspection, err := t.Dagger.introspection(ctx, installer)
	if err != nil {
		return nil, err
	}
	gen := t.elixirBase(elixirVersions[0]).
		With(installer).
		WithWorkdir("dagger_codegen").
		WithExec([]string{"mix", "deps.get"}).
		WithExec([]string{"mix", "escript.build"}).
		WithMountedFile("/schema.json", introspection).
		WithExec([]string{"./dagger_codegen", "generate", "--introspection", "/schema.json", "--outdir", "gen"}).
		WithExec([]string{"mix", "format", "gen/*.ex"}).
		Directory("gen")

	dir := dag.Directory().WithDirectory("sdk/elixir/lib/dagger/gen", gen)
	return dir, nil
}

// Publish the Elixir SDK
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
		mixFile = "/sdk/elixir/mix.exs"
	)

	ctr := t.elixirBase(elixirVersions[1])

	if !dryRun {
		mixExs, err := t.Dagger.Source.File(mixFile).Contents(ctx)
		if err != nil {
			return err
		}
		newMixExs := strings.Replace(mixExs, `@version "0.0.0"`, `@version "`+version+`"`, 1)
		ctr = ctr.WithNewFile(mixFile, dagger.ContainerWithNewFileOpts{
			Contents: newMixExs,
		})
	}

	if dryRun {
		ctr = ctr.
			WithEnvVariable("HEX_API_KEY", "").
			WithExec([]string{"mix", "hex.publish", "--yes", "--dry-run"})
	} else {
		ctr = ctr.
			WithSecretVariable("HEX_API_KEY", hexAPIKey).
			WithExec([]string{"mix", "hex.publish", "--yes"})
	}

	_, err := ctr.Sync(ctx)
	return err
}

var elixirVersionRe = regexp.MustCompile(`@dagger_cli_version "([0-9\.-a-zA-Z]+)"`)

// Bump the Elixir SDK's Engine dependency
func (t ElixirSDK) Bump(ctx context.Context, version string) (*Directory, error) {
	contents, err := t.Dagger.Source.File(elixirSDKVersionFilePath).Contents(ctx)
	if err != nil {
		return nil, err
	}

	newVersion := fmt.Sprintf(`@dagger_cli_version "%s"`, strings.TrimPrefix(version, "v"))
	newContents := elixirVersionRe.ReplaceAllString(contents, newVersion)

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
