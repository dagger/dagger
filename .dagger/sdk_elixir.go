package main

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"go.opentelemetry.io/otel/codes"
	"golang.org/x/mod/semver"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/.dagger/internal/dagger"
)

const (
	elixirSDKPath            = "sdk/elixir"
	elixirSDKGeneratedPath   = elixirSDKPath + "/lib/dagger/gen"
	elixirSDKVersionFilePath = elixirSDKPath + "/lib/dagger/core/version.ex"
)

var elixirVersions = map[string]string{
	"1.15": "hexpm/elixir:1.15.8-erlang-26.2.5.2-debian-bookworm-20240701-slim@sha256:7f282f3b1a50d795375f5bb95250aeec36d21dc2b56f6fba45b88243ac001e52",
	"1.16": "hexpm/elixir:1.16.2-erlang-26.2.5-debian-bookworm-20240513-slim@sha256:4c3bcf223c896bd817484569164357a49c473556e8773d74a591a3c565e8b8b9",
	"1.17": "hexpm/elixir:1.17.2-erlang-27.0.1-debian-bookworm-20240701-slim@sha256:0e4234e482dd487c78d0f0b73fa9bc9b03ccad0d964ef0e7a5e92a6df68ab289",
}

const elixirLatestVersion = "1.17"

type ElixirSDK struct {
	Dagger *DaggerDev // +private
}

// Lint the Elixir SDK
func (t ElixirSDK) Lint(ctx context.Context) error {
	eg := errgroup.Group{}
	eg.Go(func() (rerr error) {
		ctx, span := Tracer().Start(ctx, "lint the elixir source")
		defer func() {
			if rerr != nil {
				span.SetStatus(codes.Error, rerr.Error())
			}
			span.End()
		}()

		installer, err := t.Dagger.installer(ctx, "sdk")
		if err != nil {
			return err
		}

		sdkDev, ctr := t.base(installer)
		_, err = sdkDev.Lint(ctr).Sync(ctx)
		return err
	})
	eg.Go(func() (rerr error) {
		ctx, span := Tracer().Start(ctx, "check that the generated client library is up-to-date")
		defer func() {
			if rerr != nil {
				span.SetStatus(codes.Error, rerr.Error())
			}
			span.End()
		}()
		before := t.Dagger.Source
		after, err := t.Generate(ctx)
		if err != nil {
			return err
		}
		return dag.Dirdiff().AssertEqual(ctx, before, after, []string{"sdk/elixir/lib/dagger/gen"})
	})
	return eg.Wait()
}

// Test the Elixir SDK
func (t ElixirSDK) Test(ctx context.Context) error {
	installer, err := t.Dagger.installer(ctx, "sdk")
	if err != nil {
		return err
	}

	sdkDev, ctr := t.base(installer)
	_, err = sdkDev.Test(ctr).Sync(ctx)
	return err
}

// Regenerate the Elixir SDK API
func (t ElixirSDK) Generate(ctx context.Context) (*dagger.Directory, error) {
	installer, err := t.Dagger.installer(ctx, "sdk")
	if err != nil {
		return nil, err
	}
	introspection, err := t.Dagger.introspection(ctx, installer)
	if err != nil {
		return nil, err
	}

	sdkDev, _ := t.base(installer)
	ctr := sdkDev.WithBase(t.Dagger.Source.Directory(elixirSDKPath)).With(installer)
	return sdkDev.Generate(ctr, introspection), nil
}

// Test the publishing process
func (t ElixirSDK) TestPublish(ctx context.Context, tag string) error {
	return t.Publish(ctx, tag, true, nil)
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
	version := strings.TrimPrefix(tag, "sdk/elixir/")

	ctr := t.elixirBase(elixirVersions[elixirLatestVersion])

	if semver.IsValid(version) {
		mixFile := "/sdk/elixir/mix.exs"
		mixExs, err := t.Dagger.Source.File(mixFile).Contents(ctx)
		if err != nil {
			return err
		}
		newMixExs := strings.Replace(mixExs, `@version "0.0.0"`, `@version "`+strings.TrimPrefix(version, "v")+`"`, 1)
		ctr = ctr.WithNewFile(mixFile, newMixExs)
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
	if err != nil {
		return err
	}

	return nil
}

var elixirVersionRe = regexp.MustCompile(`@dagger_cli_version "([0-9\.-a-zA-Z]+)"`)

// Bump the Elixir SDK's Engine dependency
func (t ElixirSDK) Bump(ctx context.Context, version string) (*dagger.Directory, error) {
	contents, err := t.Dagger.Source.File(elixirSDKVersionFilePath).Contents(ctx)
	if err != nil {
		return nil, err
	}

	newVersion := fmt.Sprintf(`@dagger_cli_version "%s"`, strings.TrimPrefix(version, "v"))
	newContents := elixirVersionRe.ReplaceAllString(contents, newVersion)

	return dag.Directory().WithNewFile(elixirSDKVersionFilePath, newContents), nil
}

func (t ElixirSDK) base(installer func(*dagger.Container) *dagger.Container) (*dagger.ElixirSDKDev, *dagger.Container) {
	sdkDev := dag.ElixirSDKDev()
	ctr := sdkDev.WithBase(t.Dagger.Source.Directory(elixirSDKPath)).With(installer)
	return sdkDev, ctr
}

func (t ElixirSDK) elixirBase(baseImage string) *dagger.Container {
	src := t.Dagger.Source.Directory(elixirSDKPath)
	mountPath := "/" + elixirSDKPath

	return dag.Container().
		From(baseImage).
		WithWorkdir(mountPath).
		WithDirectory(mountPath, src).
		WithExec([]string{"mix", "local.hex", "--force"}).
		WithExec([]string{"mix", "local.rebar", "--force"}).
		WithExec([]string{"mix", "deps.get"})
}
