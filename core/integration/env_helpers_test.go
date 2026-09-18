package core

// This file contains shared nested-Dagger and fixture helpers for env-file and
// legacy `.env` tests. It is helper-only and should not own behavior coverage.

import (
	"os"
	"strings"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"github.com/dagger/testctx"
)

var nestedExec = core.ContainerWithExecOpts{ExperimentalPrivilegedNesting: true}

func nestedDaggerContainer(t *testctx.T, c *dagger.Client, modLang, modName string) *core.Container {
	ctr := core.NewQuery(c).Container().
		From(alpineImage).
		WithWorkdir("/work").
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c))
	if modLang != "" && modName != "" {
		ctr = ctr.
			WithExec([]string{"apk", "add", "git"}).
			WithExec([]string{"git", "init"}).
			WithDirectory(modName, core.NewQuery(c).Host().Directory(testModule(t, modLang, modName)))
	}
	return ctr
}

func testModule(t *testctx.T, lang, name string) string {
	return testDataPath(t, "modules", lang, name)
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
