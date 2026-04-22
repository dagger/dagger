package core

// Workspace alignment: not workspace-sensitive; no cleanup needed for the workspace branch.
// Scope: Shared nested Dagger and fixture helpers for env-file and legacy .env integration tests.
// Intent: Keep env-centric setup reusable without blurring ownership of behavioral suites.

import (
	"os"
	"path"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

var nestedExec = dagger.ContainerWithExecOpts{ExperimentalPrivilegedNesting: true}

func nestedDaggerContainer(t *testctx.T, c *dagger.Client, modLang, modName string) *dagger.Container {
	ctr := c.Container().
		From(alpineImage).
		WithWorkdir("/work").
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c))
	if modLang != "" && modName != "" {
		ctr = ctr.
			WithExec([]string{"apk", "add", "git"}).
			WithExec([]string{"git", "init"}).
			WithDirectory(modName, c.Host().Directory(testModule(t, modLang, modName)))
	}
	return ctr
}

func testModule(t *testctx.T, lang, name string) string {
	modulePath, err := filepath.Abs(path.Join("testdata", "modules", lang, name))
	require.NoError(t, err)
	return modulePath
}

func tempDirWithEnvFile(t *testctx.T, environ ...string) string {
	tmp := t.TempDir()
	os.WriteFile(tmp+"/.env", []byte(strings.Join(environ, "\n")), 0o600)
	return tmp
}

func trimDaggerFunctionUsageText(s string) string {
	start := strings.Index(s, "ARGUMENTS")
	end := strings.Index(s, "OPTIONS")
	if start >= 0 && end > start {
		s = s[start:end]
	}
	return s
}
