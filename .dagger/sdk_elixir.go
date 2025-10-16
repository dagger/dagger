package main

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/dagger/dagger/.dagger/internal/dagger"
)

type ElixirSDK struct {
	Dagger *DaggerDev // +private
}

func (t ElixirSDK) Name() string {
	return "elixir"
}

// Lint the Elixir SDK
func (t ElixirSDK) Lint(ctx context.Context) (MyCheckStatus, error) {
	return CheckCompleted, t.native().Lint(ctx)
}

// Test the Elixir SDK
func (t ElixirSDK) Test(ctx context.Context) (MyCheckStatus, error) {
	return CheckCompleted, t.native().Test(ctx)
}

// Regenerate the Elixir SDK API
func (t ElixirSDK) Generate(_ context.Context) (*dagger.Changeset, error) {
	// Make sure everything is relatve to git root
	before := dag.Directory().WithDirectory("sdk/elixir", t.native().Source())
	layer := t.native().Generate(t.Dagger.introspectionJSON())
	after := before.WithDirectory("", layer)
	return changes(before, after, nil), nil
}

// Test the publishing process
func (t ElixirSDK) ReleaseDryRun(ctx context.Context) (MyCheckStatus, error) {
	return CheckCompleted, t.Publish(ctx, "HEAD", true, nil)
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

	ctr := t.Container()

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
func (t ElixirSDK) Bump(ctx context.Context, version string) (*dagger.Changeset, error) {
	versionFilePath := "sdk/elixir/lib/dagger/core/version.ex"
	contents, err := t.Dagger.Source.File(versionFilePath).Contents(ctx)
	if err != nil {
		return nil, err
	}
	newVersion := fmt.Sprintf(`@dagger_cli_version "%s"`, strings.TrimPrefix(version, "v"))
	newContents := elixirVersionRe.ReplaceAllString(contents, newVersion)
	layer := dag.Directory().WithNewFile(versionFilePath, newContents)
	return layer.Changes(dag.Directory()), nil
}

// Wrap the native elixir SDK dev module, with the dev engine sidecar attached
func (t ElixirSDK) native() *dagger.ElixirSDKDev {
	// Start from the native module's default container,
	// then attach the dev engine on top
	return dag.ElixirSDKDev(dagger.ElixirSDKDevOpts{
		Container: dag.ElixirSDKDev().Container().With(t.Dagger.devEngineSidecar()),
	})
}

// Return the dev container from the native elixir SDK dev
func (t ElixirSDK) Container() *dagger.Container {
	return dag.ElixirSDKDev().Container()
}
