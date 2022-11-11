package sdk

import (
	"context"
	"os"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/mage/util"
	"github.com/magefile/mage/mg"
)

var _ SDK = NodeJS{}

type NodeJS mg.Namespace

// Lint lints the NodeJS SDK
func (t NodeJS) Lint(ctx context.Context) error {
	panic("FIXME")
}

// Test tests the NodeJS SDK
func (t NodeJS) Test(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	return util.WithDevEngine(ctx, c, func(ctx context.Context, c *dagger.Client) error {
		_, err = nodeJSBase(c).
			Exec(dagger.ContainerExecOpts{
				Args:                          []string{"yarn", "run", "test-sdk"},
				ExperimentalPrivilegedNesting: true,
			}).
			ExitCode(ctx)
		return err
	})
}

// Generate re-generates the SDK API
func (t NodeJS) Generate(ctx context.Context) error {
	panic("FIXME")
}

// Publish publishes the NodeJS SDK
func (t NodeJS) Publish(ctx context.Context, tag string) error {
	panic("FIXME")
}

// Bump the NodeJS SDK's Engine dependency
func (t NodeJS) Bump(ctx context.Context, version string) error {
	panic("Andrea / Erik / Tom: https://github.com/dagger/dagger/pull/3783#issuecomment-1311833703")
}

func nodeJSBase(c *dagger.Client) *dagger.Container {
	// FIXME: change to `util.Repository(c).Directory("sdk/python")` once #3459 is merged

	src := c.Directory().WithDirectory("/", util.Repository(c))

	base := c.Container().
		// ⚠️  Keep this in sync with the engine version defined in package.json
		From("node:16-alpine").
		WithWorkdir("/app")

	base = base.WithFS(
		base.
			FS().
			WithFile("/app/package.json", src.File("package.json")).
			WithFile("/app/yarn.lock", src.File("yarn.lock")),
	).
		Exec(dagger.ContainerExecOpts{
			Args: []string{"env"},
		}).
		Exec(dagger.ContainerExecOpts{
			Args: []string{"yarn", "install"},
		})

	base = base.WithFS(
		base.
			FS().
			WithDirectory("/app", src),
	)

	return base
}
