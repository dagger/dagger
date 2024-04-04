package core

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"dagger.io/dagger"
)

func testOwnership(
	ctx context.Context,
	t *testing.T,
	c *dagger.Client,
	addContent func(ctr *dagger.Container, name, owner string) *dagger.Container,
) {
	t.Parallel()

	ctr := c.Container().From(alpineImage).
		WithExec([]string{"adduser", "-D", "inherituser"}).
		WithExec([]string{"adduser", "-u", "1234", "-D", "auser"}).
		WithExec([]string{"addgroup", "-g", "4321", "agroup"}).
		WithUser("inherituser").
		WithWorkdir("/data")

	type example struct {
		name   string
		owner  string
		output string
	}

	for _, example := range []example{
		{name: "userid", owner: "1234", output: "auser auser"},
		{name: "userid-twice", owner: "1234:1234", output: "auser auser"},
		{name: "username", owner: "auser", output: "auser auser"},
		{name: "username-twice", owner: "auser:auser", output: "auser auser"},
		{name: "ids", owner: "1234:4321", output: "auser agroup"},
		{name: "username-gid", owner: "auser:4321", output: "auser agroup"},
		{name: "uid-groupname", owner: "1234:agroup", output: "auser agroup"},
		{name: "names", owner: "auser:agroup", output: "auser agroup"},

		// NB: inheriting the user/group from the container was implemented, but we
		// decided to back out for a few reasons:
		//
		// 1. performance: right now chowning has to be a separate Copy operation,
		//    which currently literally copies the relevant files even for a chown,
		//    which seems prohibitively expensive as a default. maybe with metacopy
		//    support in Buildkit this would become more feasible.
		// 2. bumping timestamps: chown operations are also technically writes, so
		//    we would be bumping timestamps all over the place and making builds
		//    non-reproducible. this has an especially unfortunate interaction with
		//    WithTimestamps where if you were to pass the timestamped values to
		//    another container you would immediately lose those timestamps.
		// 3. no opt-out: what if the user actually _wants_ to keep the permissions
		//    as they are? we would need to add another API for this. given all of
		//    the above, making it opt-in seems obvious.
		{name: "no-inherit", owner: "", output: "root root"},
	} {
		example := example
		t.Run(example.name, func(t *testing.T) {
			withOwner := addContent(ctr, example.name, example.owner)
			output, err := withOwner.
				WithUser("root"). // go back to root so we can see 0400 files
				WithExec([]string{
					"sh", "-exc",
					"find * | xargs stat -c '%U %G'", // stat recursively
				}).
				Stdout(ctx)
			require.NoError(t, err)
			for _, line := range strings.Split(output, "\n") {
				if line == "" {
					continue
				}

				require.Equal(t, example.output, line)
			}
		})
	}
}
