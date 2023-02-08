// NB(vito): be careful how you test services, in particular with a container
// as the client! It's easy to end up with a falsely passing test because
// various container APIs eagerly build and cache the client call, for example
// Container.Directory and Container.File. When the container exec is cached
// it's no longer possible to test whether its services are started
// just-in-time for a File or Directory accessed from it.
//
// So, in order to actually test that services convey to/from a Directory or
// File and are started just-in-time wherever they end up, a few of these tests
// instead use the Git and HTTP Dagger APIs since they will directly yield a
// Directory or File without eager evaluation.

package core

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"dagger.io/dagger"
	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"
)

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
		From("alpine:3.16.2").
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

func TestContainerBuildService(t *testing.T) {
	c, ctx := connect(t)
	defer c.Close()

	t.Run("building with service dependency", func(t *testing.T) {
		content := identity.NewID()
		srv := httpService(c, content)
		httpURL, err := srv.Endpoint(ctx, dagger.ContainerEndpointOpts{
			Scheme: "http",
		})
		require.NoError(t, err)

		src := c.Directory().
			WithNewFile("Dockerfile",
				`FROM alpine:3.16.2
WORKDIR /src
RUN wget `+httpURL+`
CMD cat index.html
`)

		fileContent, err := c.Container().
			WithServiceDependency(srv).
			Build(src).
			WithExec(nil).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, content, fileContent)
	})

	t.Run("building a directory that depends on a service (Container.Build)", func(t *testing.T) {
		content := identity.NewID()
		srv := httpService(c, content)
		httpURL, err := srv.Endpoint(ctx, dagger.ContainerEndpointOpts{
			Scheme: "http",
		})
		require.NoError(t, err)

		src := c.Directory().
			WithNewFile("Dockerfile",
				`FROM alpine:3.16.2
WORKDIR /src
RUN wget `+httpURL+`
CMD cat index.html
`)

		gitDaemon := gitService(c, src)
		gitHost, err := gitDaemon.Hostname(ctx)
		require.NoError(t, err)
		repoURL := fmt.Sprintf("git://%s/repo.git", gitHost)

		gitDir := c.Git(repoURL).
			WithServiceDependency(gitDaemon).
			Branch("main").
			Tree()

		fileContent, err := c.Container().
			WithServiceDependency(srv).
			Build(gitDir).
			WithExec(nil).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, content, fileContent)
	})

	t.Run("building a directory that depends on a service (Directory.DockerBuild)", func(t *testing.T) {
		content := identity.NewID()
		srv := httpService(c, content)
		httpURL, err := srv.Endpoint(ctx, dagger.ContainerEndpointOpts{
			Scheme: "http",
		})
		require.NoError(t, err)

		src := c.Directory().
			WithNewFile("Dockerfile",
				`FROM alpine:3.16.2
WORKDIR /src
RUN wget `+httpURL+`
CMD cat index.html
`)

		gitDaemon := gitService(c, src)
		gitHost, err := gitDaemon.Hostname(ctx)
		require.NoError(t, err)
		repoURL := fmt.Sprintf("git://%s/repo.git", gitHost)

		gitDir := c.Git(repoURL).
			WithServiceDependency(gitDaemon).
			Branch("main").
			Tree()

		fileContent, err := gitDir.
			DockerBuild().
			WithServiceDependency(srv).
			WithExec(nil).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, content, fileContent)
	})
}

func TestContainerExportServices(t *testing.T) {
	c, ctx := connect(t)
	defer c.Close()

	content := identity.NewID()
	srv := httpService(c, content)
	httpURL, err := srv.Endpoint(ctx, dagger.ContainerEndpointOpts{
		Scheme: "http",
	})
	require.NoError(t, err)

	client := c.Container().
		From("alpine:3.16.2").
		WithServiceDependency(srv).
		WithExec([]string{"wget", httpURL})

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
			From("alpine:3.16.2").
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
		From("alpine:3.16.2").
		WithServiceDependency(srv).
		WithWorkdir("/sub/out").
		WithExec([]string{"wget", url}).
		Rootfs().
		File("/sub/out/index.html").
		Contents(ctx)

	require.NoError(t, err)
	require.Equal(t, content, fileContent)
}

func TestContainerWithRootFSServices(t *testing.T) {
	c, ctx := connect(t)
	defer c.Close()

	content := identity.NewID()
	srv := httpService(c, content)
	url, err := srv.Endpoint(ctx, dagger.ContainerEndpointOpts{
		Scheme: "http",
	})
	require.NoError(t, err)

	gitDaemon := gitService(c,
		// this little maneuver commits the entire rootfs into a git repo
		c.Container().
			From("alpine:3.16.2").
			WithServiceDependency(srv).
			WithWorkdir("/sub/out").
			WithExec([]string{"wget", url}).
			// NB(vito): related to the package-level comment: Rootfs is not eager,
			// so this is actually OK. File and Directory are eager because they need
			// to check that the path exists (and is a file/dir), but Rootfs always
			// exists, and is always a directory.
			Rootfs())
	gitHost, err := gitDaemon.Hostname(ctx)
	require.NoError(t, err)
	repoURL := fmt.Sprintf("git://%s/repo.git", gitHost)

	gitDir := c.Git(repoURL).
		WithServiceDependency(gitDaemon).
		Branch("main").
		Tree()

	fileContent, err := c.Container().
		WithRootfs(gitDir).
		WithExec([]string{"cat", "/sub/out/index.html"}).
		Stdout(ctx)
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
		From("alpine:3.16.2").
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
		From("alpine:3.16.2").
		WithServiceDependency(srv).
		WithWorkdir("/out").
		WithExec([]string{"wget", url})

	fileContent, err := client.File("index.html").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, content, fileContent)
}

func TestContainerWithMountedDirectoryFileServices(t *testing.T) {
	c, ctx := connect(t)
	defer c.Close()

	response := identity.NewID()
	srv := httpService(c, response)

	httpURL, err := srv.Endpoint(ctx, dagger.ContainerEndpointOpts{
		Scheme: "http",
	})
	require.NoError(t, err)

	httpFile := c.HTTP(httpURL, dagger.HTTPOpts{
		ServiceDependency: srv,
	})

	gitDaemon := gitService(c, c.Directory().WithNewFile("README.md", response))

	gitHost, err := gitDaemon.Hostname(ctx)
	require.NoError(t, err)

	repoURL := fmt.Sprintf("git://%s/repo.git", gitHost)

	gitDir := c.Git(repoURL).
		WithServiceDependency(gitDaemon).
		Branch("main").
		Tree()

	useBoth := c.Container().
		From("alpine:3.16.2").
		WithMountedDirectory("/mnt/repo", gitDir).
		WithMountedFile("/mnt/index.html", httpFile)

	httpContent, err := useBoth.File("/mnt/index.html").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, response, httpContent)

	gitContent, err := useBoth.File("/mnt/repo/README.md").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, response, gitContent)
}

func TestContainerWithDirectoryFileServices(t *testing.T) {
	c, ctx := connect(t)
	defer c.Close()

	response := identity.NewID()
	srv := httpService(c, response)

	httpURL, err := srv.Endpoint(ctx, dagger.ContainerEndpointOpts{
		Scheme: "http",
	})
	require.NoError(t, err)

	httpFile := c.HTTP(httpURL, dagger.HTTPOpts{
		ServiceDependency: srv,
	})

	gitDaemon := gitService(c, c.Directory().WithNewFile("README.md", response))

	gitHost, err := gitDaemon.Hostname(ctx)
	require.NoError(t, err)

	repoURL := fmt.Sprintf("git://%s/repo.git", gitHost)

	gitDir := c.Git(repoURL).
		WithServiceDependency(gitDaemon).
		Branch("main").
		Tree()

	useBoth := c.Container().
		From("alpine:3.16.2").
		WithDirectory("/mnt/repo", gitDir).
		WithFile("/mnt/index.html", httpFile)

	httpContent, err := useBoth.File("/mnt/index.html").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, response, httpContent)

	gitContent, err := useBoth.File("/mnt/repo/README.md").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, response, gitContent)
}

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

func TestDirectoryServiceTimestamp(t *testing.T) {
	c, ctx := connect(t)
	defer c.Close()

	content := identity.NewID()
	gitDaemon := gitService(c, c.Directory().WithNewFile("README.md", content))
	gitHost, err := gitDaemon.Hostname(ctx)
	require.NoError(t, err)
	repoURL := fmt.Sprintf("git://%s/repo.git", gitHost)

	ts := time.Date(1991, 6, 3, 0, 0, 0, 0, time.UTC)
	stamped := c.Git(repoURL).
		WithServiceDependency(gitDaemon).
		Branch("main").
		Tree().
		WithTimestamps(int(ts.Unix()))

	stdout, err := c.Container().From("alpine:3.16.2").
		WithDirectory("/repo", stamped).
		WithExec([]string{"stat", "/repo/README.md"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, stdout, "1991-06-03")
}

func TestDirectoryWithDirectoryFileServices(t *testing.T) {
	c, ctx := connect(t)
	defer c.Close()

	content := identity.NewID()

	gitSrv := gitService(c, c.Directory().WithNewFile("README.md", content))
	gitHost, err := gitSrv.Hostname(ctx)
	require.NoError(t, err)
	repoURL := fmt.Sprintf("git://%s/repo.git", gitHost)

	httpSrv := httpService(c, content)
	httpURL, err := httpSrv.Endpoint(ctx, dagger.ContainerEndpointOpts{Scheme: "http"})
	require.NoError(t, err)

	useBoth := c.Directory().
		WithDirectory("/repo", c.Git(repoURL).WithServiceDependency(gitSrv).Branch("main").Tree()).
		WithFile("/index.html", c.HTTP(httpURL, dagger.HTTPOpts{ServiceDependency: httpSrv}))

	entries, err := useBoth.Directory("/repo").Entries(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"README.md"}, entries)

	fileContent, err := useBoth.File("/index.html").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, content, fileContent)
}

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

func TestFileServiceTimestamp(t *testing.T) {
	c, ctx := connect(t)
	defer c.Close()

	content := identity.NewID()

	httpSrv := httpService(c, content)
	httpURL, err := httpSrv.Endpoint(ctx, dagger.ContainerEndpointOpts{Scheme: "http"})
	require.NoError(t, err)

	ts := time.Date(1991, 6, 3, 0, 0, 0, 0, time.UTC)
	stamped := c.HTTP(httpURL, dagger.HTTPOpts{ServiceDependency: httpSrv}).
		WithTimestamps(int(ts.Unix()))

	stdout, err := c.Container().From("alpine:3.16.2").
		WithFile("/index.html", stamped).
		WithExec([]string{"stat", "/index.html"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, stdout, "1991-06-03")
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
