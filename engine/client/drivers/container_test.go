package drivers

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// This file contains various sanity checks for containerBackend - these
// shouldn't be run as part of a regular test run, but are designed to ensure
// that our backends work when hacking on them.

var backends = []struct {
	name    string
	backend containerBackend
}{
	{
		name:    "docker",
		backend: docker{cmd: "docker"},
	},
	{
		name:    "podman",
		backend: docker{cmd: "podman"},
	},
	{
		name:    "nerdctl",
		backend: docker{cmd: "nerdctl"},
	},
	{
		name:    "apple",
		backend: apple{},
	},
}

var _, shouldRun = os.LookupEnv("DRIVER_TEST")

func TestBackendImagePullAndExists(t *testing.T) {
	if !shouldRun {
		t.Skip()
	}

	for _, tc := range backends {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()

			testImage := "alpine:3.18"
			_ = tc.backend.ImageRemove(ctx, testImage)

			existsBefore, err := tc.backend.ImageExists(ctx, testImage)
			require.NoError(t, err)
			require.False(t, existsBefore)

			err = tc.backend.ImagePull(ctx, testImage)
			require.NoError(t, err)
			t.Cleanup(func() {
				ctx := context.WithoutCancel(ctx)
				require.NoError(t, tc.backend.ImageRemove(ctx, testImage))
			})

			existsAfter, err := tc.backend.ImageExists(ctx, testImage)
			require.NoError(t, err)
			require.True(t, existsAfter)
		})
	}
}

func TestBackendImageLoadAndExists(t *testing.T) {
	if !shouldRun {
		t.Skip()
	}

	for _, tc := range backends {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()

			sourceImage := "alpine:3.18"
			_ = tc.backend.ImageRemove(ctx, sourceImage)
			loadedImageName := "test-loaded-alpine:custom"
			_ = tc.backend.ImageRemove(ctx, loadedImageName)

			pullCmd := exec.CommandContext(ctx, "docker", "pull", sourceImage)
			err := pullCmd.Run()
			require.NoError(t, err)
			t.Cleanup(func() {
				ctx := context.WithoutCancel(ctx)
				rmCmd := exec.CommandContext(ctx, "docker", "image", "rm", sourceImage)
				err := rmCmd.Run()
				require.NoError(t, err)
			})

			saveCmd := exec.CommandContext(ctx, "docker", "save", sourceImage)
			var tarballBuffer bytes.Buffer
			saveCmd.Stdout = &tarballBuffer
			err = saveCmd.Run()
			require.NoError(t, err)

			existsBefore, err := tc.backend.ImageExists(ctx, loadedImageName)
			require.NoError(t, err)
			require.False(t, existsBefore)

			loadBackend := tc.backend.ImageLoader(ctx)
			require.NotNil(t, loadBackend)
			loader, err := loadBackend.Loader(ctx)
			require.NoError(t, err)

			err = loader.TarballWriter(ctx, loadedImageName, bytes.NewReader(tarballBuffer.Bytes()))
			require.NoError(t, err)
			t.Cleanup(func() {
				ctx := context.WithoutCancel(ctx)
				require.NoError(t, tc.backend.ImageRemove(ctx, loadedImageName))
			})

			existsAfter, err := tc.backend.ImageExists(ctx, loadedImageName)
			require.NoError(t, err)
			require.True(t, existsAfter)
		})
	}
}

func TestBackendContainerRunExec(t *testing.T) {
	if !shouldRun {
		t.Skip()
	}

	for _, tc := range backends {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			containerName := "test-run-exec-container"
			testImage := "alpine:3.18"

			_ = tc.backend.ImageRemove(ctx, testImage)
			_ = tc.backend.ContainerRemove(ctx, containerName)
			err := tc.backend.ImagePull(ctx, testImage)
			require.NoError(t, err)

			t.Cleanup(func() {
				ctx := context.WithoutCancel(ctx)
				require.NoError(t, tc.backend.ImageRemove(ctx, testImage))
			})

			existsBefore, err := tc.backend.ContainerExists(ctx, containerName)
			require.NoError(t, err)
			require.False(t, existsBefore)

			runOpts := runOpts{
				image: testImage,
				args:  []string{"sleep", "30"},
			}
			err = tc.backend.ContainerRun(ctx, containerName, runOpts)
			require.NoError(t, err)
			t.Cleanup(func() {
				ctx := context.WithoutCancel(ctx)
				require.NoError(t, tc.backend.ContainerRemove(ctx, containerName))
			})

			existsAfter, err := tc.backend.ContainerExists(ctx, containerName)
			require.NoError(t, err)
			require.True(t, existsAfter)

			execArgs := []string{"echo", "hello world"}
			stdout, stderr, err := tc.backend.ContainerExec(ctx, containerName, execArgs)
			require.NoError(t, err)
			require.Equal(t, "hello world", stdout)
			require.Empty(t, stderr)

			execArgs2 := []string{"whoami"}
			stdout2, stderr2, err := tc.backend.ContainerExec(ctx, containerName, execArgs2)
			require.NoError(t, err)
			require.True(t, strings.Contains(stdout2, "root"))
			require.Empty(t, stderr2)
		})
	}
}

func TestBackendContainerLs(t *testing.T) {
	if !shouldRun {
		t.Skip()
	}

	for _, tc := range backends {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			testImage := "alpine:3.18"
			containers := []string{"test-ls-container-1", "test-ls-container-2"}

			_ = tc.backend.ImageRemove(ctx, testImage)
			for _, containerName := range containers {
				_ = tc.backend.ContainerRemove(ctx, containerName)
			}

			err := tc.backend.ImagePull(ctx, testImage)
			require.NoError(t, err)
			t.Cleanup(func() {
				ctx := context.WithoutCancel(ctx)
				require.NoError(t, tc.backend.ImageRemove(ctx, testImage))
			})

			initialList, err := tc.backend.ContainerLs(ctx)
			require.NoError(t, err)
			initialCount := len(initialList)

			// Create multiple containers
			for _, containerName := range containers {
				runOpts := runOpts{
					image: testImage,
					args:  []string{"sleep", "30"},
				}
				err = tc.backend.ContainerRun(ctx, containerName, runOpts)
				require.NoError(t, err)
			}
			t.Cleanup(func() {
				ctx := context.WithoutCancel(ctx)
				for _, containerName := range containers {
					require.NoError(t, tc.backend.ContainerRemove(ctx, containerName))
				}
			})

			finalList, err := tc.backend.ContainerLs(ctx)
			require.NoError(t, err)
			require.Equal(t, initialCount+len(containers), len(finalList))

			for _, containerName := range containers {
				found := false
				for _, listedContainer := range finalList {
					if strings.Contains(listedContainer.name, containerName) {
						found = true
						break
					}
				}
				require.True(t, found, "Container %s should be in the list", containerName)
			}
		})
	}
}

func TestBackendContainerRunWithOptions(t *testing.T) {
	if !shouldRun {
		t.Skip()
	}

	for _, tc := range backends {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			containerName := "test-run-options-container"
			testImage := "alpine:3.18"

			_ = tc.backend.ImageRemove(ctx, testImage)
			_ = tc.backend.ContainerRemove(ctx, containerName)

			err := tc.backend.ImagePull(ctx, testImage)
			require.NoError(t, err)
			t.Cleanup(func() {
				ctx := context.WithoutCancel(ctx)
				require.NoError(t, tc.backend.ImageRemove(ctx, testImage))
			})

			runOpts := runOpts{
				image: testImage,
				env:   []string{"TEST_VAR=hello", "ANOTHER_VAR=world"},
				args:  []string{"sleep", "30"},
			}
			err = tc.backend.ContainerRun(ctx, containerName, runOpts)
			require.NoError(t, err)
			t.Cleanup(func() {
				ctx := context.WithoutCancel(ctx)
				require.NoError(t, tc.backend.ContainerRemove(ctx, containerName))
			})

			stdout, _, err := tc.backend.ContainerExec(ctx, containerName, []string{"env"})
			require.NoError(t, err)
			require.Contains(t, stdout, "TEST_VAR=hello")
			require.Contains(t, stdout, "ANOTHER_VAR=world")
		})
	}
}
