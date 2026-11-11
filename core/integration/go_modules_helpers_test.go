package core

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"dagger.io/dagger/core"

	"dagger.io/dagger"
	"golang.org/x/mod/module"
)

// repoGoModuleFiles is the directory of the repo's module downloads
// (repoGoModuleDownloadDir) and, relative to it, the files for the modules in
// the repo's go.sum.
//
// Tests that compile parts of this repo run in the dev engine, whose module
// caches start empty for every test run. Without this, each of those caches
// downloads most of the repo's dependency graph again, although the test
// runner already has it.
type repoGoModuleFiles struct {
	downloadDir string
	files       []string
}

var repoGoModules = sync.OnceValues(func() (repoGoModuleFiles, error) {
	downloadDir, err := repoGoModuleDownloadDir()
	if err != nil {
		return repoGoModuleFiles{}, err
	}
	goSum, err := os.ReadFile("../../go.sum")
	if err != nil {
		return repoGoModuleFiles{}, err
	}
	var files []string
	seen := map[string]bool{}
	for line := range strings.Lines(string(goSum)) {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		path, version := fields[0], fields[1]
		exts := []string{".info", ".mod", ".zip"}
		if v, ok := strings.CutSuffix(version, "/go.mod"); ok {
			version, exts = v, []string{".mod"}
		}
		escPath, err := module.EscapePath(path)
		if err != nil {
			continue
		}
		escVersion, err := module.EscapeVersion(version)
		if err != nil {
			continue
		}
		for _, ext := range exts {
			f := escPath + "/@v/" + escVersion + ext
			if !seen[f] {
				seen[f] = true
				files = append(files, f)
			}
		}
	}
	return repoGoModuleFiles{downloadDir, files}, nil
})

// repoGoModuleDownloadDir is the directory of the repo's module downloads:
// the one engine-dev's test container mounts and names in
// _DAGGER_TESTS_GO_MODULE_DOWNLOADS, else the test runner's own module cache
// download directory, as when the tests run outside engine-dev.
func repoGoModuleDownloadDir() (string, error) {
	if dir := os.Getenv("_DAGGER_TESTS_GO_MODULE_DOWNLOADS"); dir != "" {
		return dir, nil
	}
	out, err := exec.Command("go", "env", "GOMODCACHE").Output()
	if err != nil {
		return "", fmt.Errorf("go env GOMODCACHE: %w", err)
	}
	return filepath.Join(strings.TrimSpace(string(out)), "cache", "download"), nil
}

// withRepoGoModules makes Go commands in the container get the repo's
// dependencies from the test runner's module cache, used as a file://
// module proxy, and go to the network only for anything else.
func withRepoGoModules(c *dagger.Client) core.WithContainerFunc {
	return func(ctr *core.Container) *core.Container {
		mods, err := repoGoModules()
		if err != nil {
			// Not fatal: the container downloads modules as before.
			return ctr
		}
		const proxyDir = "/repo-go-modules"
		return ctr.
			WithMountedDirectory(proxyDir, core.NewQuery(c).Host().Directory(mods.downloadDir, core.HostDirectoryOpts{
				Include: mods.files,
			})).
			WithEnvVariable("GOPROXY", "file://"+proxyDir+",https://proxy.golang.org,direct")
	}
}
