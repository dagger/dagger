package sdk

import (
	"context"
	"os"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/mage/util"
	"github.com/magefile/mage/mg"
)

var rubyGeneratedAPIPath = "sdk/ruby/lib/dagger/client_gen.rb"

var _ SDK = Ruby{}

type Ruby mg.Namespace

// Lint lints the Ruby SDK
func (t Ruby) Lint(ctx context.Context) error {
	return nil
}

// Test tests the Ruby SDK
func (t Ruby) Test(ctx context.Context) error {
	return nil
}

// Generate re-generates the SDK API
func (t Ruby) Generate(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	c = c.Pipeline("sdk").Pipeline("ruby").Pipeline("generate")

	devEngine, endpoint, err := util.CIDevEngineContainerAndEndpoint(ctx, c.Pipeline("dev-engine"), util.DevEngineOpts{Name: "sdk-ruby-generate"})
	if err != nil {
		return err
	}
	cliBinPath := "/.dagger-cli"

	generated, err := rubyBase(c).
		WithMountedFile("/usr/local/bin/client-gen", util.ClientGenBinary(c)).
		WithServiceBinding("dagger-engine", devEngine).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpoint).
		WithMountedFile(cliBinPath, util.DaggerBinary(c)).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinPath).
		WithExec([]string{"client-gen", "--lang", "ruby", "-o", rubyGeneratedAPIPath}).
		File(rubyGeneratedAPIPath).
		Contents(ctx)
	if err != nil {
		return err
	}
	return os.WriteFile(rubyGeneratedAPIPath, []byte(generated), 0o600)
}

// Publish publishes the Ruby SDK
func (t Ruby) Publish(ctx context.Context, tag string) error {
	return nil
}

// Bump the Ruby SDK's Engine dependency
func (t Ruby) Bump(ctx context.Context, version string) error {
	return nil
}

func rubyBase(c *dagger.Client) *dagger.Container {
	workdir := c.Directory().WithDirectory("/", util.Repository(c).Directory("sdk/ruby"))

	base := c.Container().
		From("alpine").
		WithWorkdir("/workdir")

	deps := base.WithRootfs(base.Rootfs())

	src := deps.WithRootfs(
		deps.
			Rootfs().
			WithDirectory("/workdir", workdir),
	)

	return src
}
