package core

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"dagger.io/dagger"
	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"
)

func TestContainerExecServices(t *testing.T) {
	c, ctx := connect(t)
	defer c.Close()

	srv := httpService(c, "Hello, world!")

	hostname, err := srv.Hostname(ctx)
	require.NoError(t, err)

	url, err := srv.Endpoint(ctx, dagger.ContainerEndpointOpts{
		Scheme: "http",
	})
	require.NoError(t, err)
	require.Equal(t, "http://"+hostname+":8000", url)

	client := c.Container().
		From("alpine").
		WithServiceDependency(srv).
		WithExec([]string{"apk", "add", "curl"}).
		WithExec([]string{"curl", "-v", url})

	code, err := client.ExitCode(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, code)

	stdout, err := client.Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "Hello, world!", stdout)

	stderr, err := client.Stderr(ctx)
	require.NoError(t, err)
	require.Contains(t, stderr, "Host: "+hostname+":8000")
}

func TestContainerExportServices(t *testing.T) {
	c, ctx := connect(t)
	defer c.Close()

	content := identity.NewID()
	srv := httpService(c, content)

	hostname, err := srv.Hostname(ctx)
	require.NoError(t, err)

	url, err := srv.Endpoint(ctx, dagger.ContainerEndpointOpts{
		Scheme: "http",
	})
	require.NoError(t, err)
	require.Equal(t, "http://"+hostname+":8000", url)

	client := c.Container().
		From("alpine").
		WithServiceDependency(srv).
		WithExec([]string{"wget", url})

	filePath := filepath.Join(t.TempDir(), "image.tar")
	ok, err := client.Export(ctx, filePath)
	require.NoError(t, err)
	require.True(t, ok)
}

func TestContainerMultiPlatformExportServices(t *testing.T) {
	c, ctx := connect(t)
	defer c.Close()

	variants := make([]*dagger.Container, 0, len(platformToUname))
	for platform := range platformToUname {
		srv := httpService(c, string(platform))

		url, err := srv.Endpoint(ctx, dagger.ContainerEndpointOpts{
			Scheme: "http",
		})
		require.NoError(t, err)

		ctr := c.Container(dagger.ContainerOpts{Platform: platform}).
			From("alpine").
			WithServiceDependency(srv).
			WithExec([]string{"wget", url}).
			WithExec([]string{"uname", "-m"})

		variants = append(variants, ctr)
	}

	dest := filepath.Join(t.TempDir(), "image.tar")
	ok, err := c.Container().Export(ctx, dest, dagger.ContainerExportOpts{
		PlatformVariants: variants,
	})
	require.NoError(t, err)
	require.True(t, ok)
}

func TestServicesContainerPublish(t *testing.T) {
	c, ctx := connect(t)
	defer c.Close()

	startRegistry(t)

	content := identity.NewID()
	srv := httpService(c, content)

	url, err := srv.Endpoint(ctx, dagger.ContainerEndpointOpts{
		Scheme: "http",
	})
	require.NoError(t, err)

	testRef := "127.0.0.1:5000/testimagepush:latest"
	pushedRef, err := c.Container().
		From("alpine:3.16.2").
		WithServiceDependency(srv).
		WithExec([]string{"wget", url}).
		Publish(ctx, testRef)
	require.NoError(t, err)
	require.NotEqual(t, testRef, pushedRef)
	require.Contains(t, pushedRef, "@sha256:")

	fileContent, err := c.Container().
		From(pushedRef).Rootfs().File("/index.html").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, fileContent, content)
}

func TestContainerRootFSServices(t *testing.T) {
	c, ctx := connect(t)
	defer c.Close()

	content := identity.NewID()
	srv := httpService(c, content)

	hostname, err := srv.Hostname(ctx)
	require.NoError(t, err)

	url, err := srv.Endpoint(ctx, dagger.ContainerEndpointOpts{
		Scheme: "http",
	})
	require.NoError(t, err)
	require.Equal(t, "http://"+hostname+":8000", url)

	fileContent, err := c.Container().
		From("alpine").
		WithServiceDependency(srv).
		WithWorkdir("/sub/out").
		WithExec([]string{"wget", url}).
		Rootfs().
		File("/sub/out/index.html").
		Contents(ctx)

	require.NoError(t, err)
	require.Equal(t, content, fileContent)
}

func TestContainerDirectoryServices(t *testing.T) {
	c, ctx := connect(t)
	defer c.Close()

	content := identity.NewID()
	srv := httpService(c, content)

	hostname, err := srv.Hostname(ctx)
	require.NoError(t, err)

	url, err := srv.Endpoint(ctx, dagger.ContainerEndpointOpts{
		Scheme: "http",
	})
	require.NoError(t, err)
	require.Equal(t, "http://"+hostname+":8000", url)

	wget := c.Container().
		From("alpine").
		WithServiceDependency(srv).
		WithWorkdir("/sub/out").
		WithExec([]string{"wget", url})

	t.Run("runs services for Container.Directory.Entries", func(t *testing.T) {
		entries, err := wget.Directory(".").Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"index.html"}, entries)
	})

	t.Run("runs services for Container.Directory.Directory.Entries", func(t *testing.T) {
		entries, err := wget.Directory("/sub").Directory("out").Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"index.html"}, entries)
	})

	t.Run("runs services for Container.Directory.File.Contents", func(t *testing.T) {
		fileContent, err := wget.Directory(".").File("index.html").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, content, fileContent)
	})

	t.Run("runs services for Container.Directory.Export", func(t *testing.T) {
		dest := t.TempDir()

		ok, err := wget.Directory(".").Export(ctx, dest)
		require.NoError(t, err)
		require.True(t, ok)

		fileContent, err := os.ReadFile(filepath.Join(dest, "index.html"))
		require.NoError(t, err)
		require.Equal(t, content, string(fileContent))
	})
}

func TestContainerFileServices(t *testing.T) {
	c, ctx := connect(t)
	defer c.Close()

	content := identity.NewID()
	srv := httpService(c, content)

	hostname, err := srv.Hostname(ctx)
	require.NoError(t, err)

	url, err := srv.Endpoint(ctx, dagger.ContainerEndpointOpts{
		Scheme: "http",
	})
	require.NoError(t, err)
	require.Equal(t, "http://"+hostname+":8000", url)

	client := c.Container().
		From("alpine").
		WithServiceDependency(srv).
		WithWorkdir("/out").
		WithExec([]string{"wget", url})

	fileContent, err := client.File("index.html").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, content, fileContent)
}

// TestDirectoryServiceEntries tests that a directory starts its dependent
// services when listing entries.
//
// It uses the Git API to avoid using a container. Using the Container API
// would falsely pass because the container would be run and cached as soon as
// we access a directory from it, allowing the export to work regardless.
func TestDirectoryServiceEntries(t *testing.T) {
	c, ctx := connect(t)
	defer c.Close()

	content := identity.NewID()

	gitDaemon := gitService(c, c.Directory().WithNewFile("README.md", content))

	gitHost, err := gitDaemon.Hostname(ctx)
	require.NoError(t, err)

	repoURL := fmt.Sprintf("git://%s/repo.git", gitHost)

	entries, err := c.Git(repoURL).
		WithServiceDependency(gitDaemon).
		Branch("main").
		Tree().
		Entries(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"README.md"}, entries)
}

// TestDirectoryServiceExport tests that a directory starts its dependent
// services upon export.
//
// It uses the Git API to avoid using a container. Using the Container API
// would falsely pass because the container would be run and cached as soon as
// we access a directory from it, allowing the export to work regardless.
func TestDirectoryServiceExport(t *testing.T) {
	c, ctx := connect(t)
	defer c.Close()

	content := identity.NewID()

	gitDaemon := gitService(c, c.Directory().WithNewFile("README.md", content))

	gitHost, err := gitDaemon.Hostname(ctx)
	require.NoError(t, err)

	repoURL := fmt.Sprintf("git://%s/repo.git", gitHost)

	dest := t.TempDir()

	ok, err := c.Git(repoURL).
		WithServiceDependency(gitDaemon).
		Branch("main").
		Tree().
		Export(ctx, dest)
	require.NoError(t, err)
	require.True(t, ok)

	exportedContent, err := os.ReadFile(filepath.Join(dest, "README.md"))
	require.NoError(t, err)
	require.Equal(t, content, string(exportedContent))
}

// TestFileServiceContents tests that a file starts its dependent services when
// reading its content.
//
// It uses the Git API to avoid using a container. Using the Container API
// would falsely pass because the container would be run and cached as soon as
// we access a file from it, allowing the export to work regardless.
func TestFileServiceContents(t *testing.T) {
	c, ctx := connect(t)
	defer c.Close()

	content := identity.NewID()

	gitDaemon := gitService(c, c.Directory().WithNewFile("README.md", content))

	gitHost, err := gitDaemon.Hostname(ctx)
	require.NoError(t, err)

	repoURL := fmt.Sprintf("git://%s/repo.git", gitHost)

	fileContent, err := c.Git(repoURL).
		WithServiceDependency(gitDaemon).
		Branch("main").
		Tree().
		File("README.md").
		Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, content, fileContent)
}

// TestFileServiceExport tests that a file starts its dependent services upon
// export.
//
// It uses the Git API to avoid using a container. Using the Container API
// would falsely pass because the container would be run and cached as soon as
// we access a file from it, allowing the export to work regardless.
func TestFileServiceExport(t *testing.T) {
	c, ctx := connect(t)
	defer c.Close()

	content := identity.NewID()

	gitDaemon := gitService(c, c.Directory().WithNewFile("README.md", content))

	gitHost, err := gitDaemon.Hostname(ctx)
	require.NoError(t, err)

	repoURL := fmt.Sprintf("git://%s/repo.git", gitHost)

	dest := t.TempDir()
	filePath := filepath.Join(dest, "README.md")

	ok, err := c.Git(repoURL).
		WithServiceDependency(gitDaemon).
		Branch("main").
		Tree().
		File("README.md").
		Export(ctx, filePath)
	require.NoError(t, err)
	require.True(t, ok)

	exportedContent, err := os.ReadFile(filePath)
	require.NoError(t, err)
	require.Equal(t, content, string(exportedContent))
}

func TestServiceHostnamesAreStable(t *testing.T) {
	c, ctx := connect(t)
	defer c.Close()

	www := c.Directory().WithNewFile("index.html", "Hello, world!")

	srv := c.Container().
		From("python").
		WithMountedDirectory("/srv/www", www).
		WithWorkdir("/srv/www").
		// NB: chain a few things to make the container a bit more complicated.
		//
		// for example, ContainerIDs aren't totally deterministic as WithExec adds
		// some randomization to how LLB gets marshalled.
		WithExec([]string{"echo", "first"}).
		WithExec([]string{"echo", "second"}).
		WithExec([]string{"echo", "third"}).
		WithEnvVariable("FOO", "123").
		WithEnvVariable("BAR", "456").
		WithExposedPort(8000).
		WithExec([]string{"python", "-m", "http.server"})

	hosts := map[string]int{}

	for i := 0; i < 10; i++ {
		hostname, err := srv.Hostname(ctx)
		require.NoError(t, err)
		hosts[hostname]++
	}

	require.Len(t, hosts, 1)
}

func httpService(c *dagger.Client, content string) *dagger.Container {
	return c.Container().
		From("python").
		WithMountedDirectory(
			"/srv/www",
			c.Directory().WithNewFile("index.html", content),
		).
		WithWorkdir("/srv/www").
		WithExposedPort(8000).
		WithExec([]string{"python", "-m", "http.server"})
}

func gitService(c *dagger.Client, content *dagger.Directory) *dagger.Container {
	const gitPort = 9418
	return c.Container().
		From("alpine:3.16.2").
		WithExec([]string{"apk", "add", "git", "git-daemon"}).
		WithDirectory("/root/repo", content).
		WithMountedFile("/root/start.sh",
			c.Directory().
				WithNewFile("start.sh", `#!/bin/sh

set -e -u -x

cd /root

git config --global user.email "root@localhost"
git config --global user.name "Test User"

mkdir srv

cd repo
	git init
	git branch -m main
	git add *
	git commit -m "init"
cd ..

cd srv
	git clone --bare ../repo repo.git
cd ..

git daemon --verbose --export-all --base-path=/root/srv
`).
				File("start.sh")).
		WithExposedPort(gitPort).
		WithExec([]string{"sh", "/root/start.sh"})
}
