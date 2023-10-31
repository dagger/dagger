//go:build daggerverse_tests

package core

import (
	"testing"

	"dagger.io/dagger"
	"github.com/stretchr/testify/require"
)

// TODO: dynamically update this list? Currently would require web-scraping...
// This is incomplete. Also nothing is pinned.
var testedModules = []string{
	"github.com/kpenfound/dagger-modules/golang",
	"github.com/kpenfound/dagger-modules/netlify",
	"github.com/kpenfound/dagger-modules/fly",
	"github.com/kpenfound/dagger-modules/encircle",
	"github.com/kpenfound/dagger-modules/vault",
	"github.com/kpenfound/dagger-modules/proxy",
	"github.com/kpenfound/dagger-modules/secretsmanager",

	"github.com/jpadams/daggerverse/trivy",
	"github.com/jpadams/daggerverse/drupalTest",
	"github.com/jpadams/daggerverse/mariadb",
	"github.com/jpadams/daggerverse/drupal",
	"github.com/jpadams/daggerverse/staticcheck",
	"github.com/jpadams/daggerverse/goVersion",
	"github.com/jpadams/daggerverse/helloSecret",

	"github.com/shykes/daggerverse/imagemagick",
	"github.com/shykes/daggerverse/dagger",
	"github.com/shykes/daggerverse/helloWorld",
	"github.com/shykes/daggerverse/make",
	"github.com/shykes/daggerverse/myip",
	"github.com/shykes/daggerverse/datetime",
	"github.com/shykes/daggerverse/tailscale",

	"github.com/aweris/daggerverse/kind",
	"github.com/aweris/daggerverse/gh",
	"github.com/aweris/daggerverse/helm",
	"github.com/aweris/daggerverse/kubectl",
	"github.com/aweris/daggerverse/gale",

	"github.com/quartz-technology/daggerverse/node",
	"github.com/quartz-technology/daggerverse/redis",
}

func TestDaggerverse(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	for _, moduleName := range testedModules {
		moduleName := moduleName
		t.Run(moduleName, func(t *testing.T) {
			t.Parallel()

			_, err := c.Container().From("alpine:3.18").
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithExec([]string{testCLIBinPath, "-m", moduleName, "functions"}, dagger.ContainerWithExecOpts{
					ExperimentalPrivilegedNesting: true,
				}).
				Stdout(ctx)
			require.NoError(t, err)
		})
	}
}
