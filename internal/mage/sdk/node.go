package sdk

import (
	"context"
	"os"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/mage/util"
	"github.com/magefile/mage/mg"
)

var _ SDK = Node{}

type Node mg.Namespace

// Lint lints the NodeJS SDK
func (t Node) Lint(ctx context.Context) error {
	panic("FIXME")
}

// Test tests the NodeJS SDK
func (t Node) Test(ctx context.Context) error {
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
func (t Node) Generate(ctx context.Context) error {
	panic("FIXME")
}

// Publish publishes the NodeJS SDK
func (t Node) Publish(ctx context.Context, tag string) error {
	panic("FIXME")
}

func nodeJSBase(c *dagger.Client) *dagger.Container {
	// FIXME: change to `util.Repository(c).Directory("sdk/python")` once #3459 is merged

	src := c.Directory().WithDirectory("/", util.Repository(c))

	base := c.Container().
		From("node:18.12-alpine").
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
