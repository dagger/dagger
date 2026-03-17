package main

import (
	"fmt"
	"strings"

	"dagger/python-sdk-dev/internal/dagger"
)

// Return the changes between two directory, excluding the specified path patterns from the comparison
func changes(before, after *dagger.Directory, exclude []string) *dagger.Changeset {
	if exclude == nil {
		return after.Changes(before)
	}
	return after.
		// 1. Remove matching files from after
		Filter(dagger.DirectoryFilterOpts{Exclude: exclude}).
		// 2. Copy matching files from before
		WithDirectory("", before.Filter(dagger.DirectoryFilterOpts{Include: exclude})).
		Changes(before)
}

// Set up the cache directory for multiple tools.
func toolsCache(args ...string) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		for _, tool := range args {
			ctr = ctr.
				WithMountedCache(
					fmt.Sprintf("/root/.cache/%s", tool),
					dag.CacheVolume(fmt.Sprintf("modpythondev-%s", tool))).
				WithEnvVariable(
					fmt.Sprintf("%s_CACHE_DIR", strings.ToUpper(tool)),
					fmt.Sprintf("/root/.cache/%s", tool))
		}
		return ctr
	}
}

// Add directory as a mount on a container, under `/work`
func mountedWorkdir(src *dagger.Directory) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.
			WithMountedDirectory("/work", src).
			WithWorkdir("/work")
	}
}

// Add the uv tool to the container.
func uvTool(workspace *dagger.Directory) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.
			WithDirectory(
				"/usr/local/bin",
				workspace.Directory("sdk/python/runtime/images/uv").DockerBuild().Rootfs(),
				dagger.ContainerWithDirectoryOpts{Include: []string{"uv*"}}).
			WithEnvVariable("UV_LINK_MODE", "copy").
			WithEnvVariable("UV_PROJECT_ENVIRONMENT", "/opt/venv")
	}
}

func uv(args ...string) []string {
	return append([]string{"uv"}, args...)
}

func uvRun(args ...string) []string {
	return append(uv("run"), args...)
}
