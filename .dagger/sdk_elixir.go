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
	elixirSDKVersionFilePath = elixirSDKPath + "/lib/dagger/core/version.ex"
)

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

		installer := t.Dagger.installer("sdk")
		return t.sdkDevWithInstaller(installer).Lint(ctx)
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
	installer := t.Dagger.installer("sdk")
	return t.sdkDevWithInstaller(installer).Test(ctx)
}

// Regenerate the Elixir SDK API
func (t ElixirSDK) Generate(ctx context.Context) (*dagger.Directory, error) {
	installer := t.Dagger.installer("sdk")
	introspection := t.Dagger.introspection(installer)

	return t.sdkDevWithInstaller(installer).Generate(introspection), nil
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

	ctr := t.sdkDev().Container()

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
	return err
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

func (t ElixirSDK) sdkDev() *dagger.ElixirSDKDev {
	return dag.ElixirSDKDev()
}

func (t ElixirSDK) sdkDevWithInstaller(
	installer func(*dagger.Container) *dagger.Container,
) *dagger.ElixirSDKDev {
	return dag.ElixirSDKDev(dagger.ElixirSDKDevOpts{Container: t.sdkDev().Container().With(installer)})
}
