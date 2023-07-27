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
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/engine"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

func TestServiceHostnamesAreStable(t *testing.T) {
	t.Parallel()

	checkNotDisabled(t, engine.ServicesDNSEnvName)

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

func TestContainerHostnameEndpoint(t *testing.T) {
	t.Parallel()

	checkNotDisabled(t, engine.ServicesDNSEnvName)

	c, ctx := connect(t)
	defer c.Close()

	t.Run("hostname is independent of exposed ports", func(t *testing.T) {
		a, err := c.Container().
			From("python").
			WithExposedPort(8000).
			WithExec([]string{"python", "-m", "http.server"}).
			Hostname(ctx)
		require.NoError(t, err)

		b, err := c.Container().
			From("python").
			WithExec([]string{"python", "-m", "http.server"}).
			Hostname(ctx)
		require.NoError(t, err)

		require.Equal(t, a, b)
	})

	t.Run("hostname is same as endpoint", func(t *testing.T) {
		srv := c.Container().
			From("python").
			WithExposedPort(8000).
			WithExec([]string{"python", "-m", "http.server"})

		hn, err := srv.Hostname(ctx)
		require.NoError(t, err)

		ep, err := srv.Endpoint(ctx)
		require.NoError(t, err)

		require.Equal(t, hn+":8000", ep)
	})

	t.Run("hostname and endpoint force default command", func(t *testing.T) {
		srv := c.Container().
			From("python").
			WithExposedPort(8000).
			WithDefaultArgs(dagger.ContainerWithDefaultArgsOpts{
				Args: []string{"python", "-m", "http.server"},
			})

		hn, err := srv.Hostname(ctx)
		require.NoError(t, err)

		ep, err := srv.Endpoint(ctx)
		require.NoError(t, err)
		require.Equal(t, hn+":8000", ep)

		exp, err := srv.WithExec(nil).Hostname(ctx)
		require.NoError(t, err)
		require.Equal(t, hn, exp)
	})

	t.Run("endpoint can specify arbitrary port", func(t *testing.T) {
		srv := c.Container().
			From("python").
			WithExec([]string{"python", "-m", "http.server"})

		hn, err := srv.Hostname(ctx)
		require.NoError(t, err)

		ep, err := srv.Endpoint(ctx, dagger.ContainerEndpointOpts{
			Port: 1234,
		})
		require.NoError(t, err)

		require.Equal(t, hn+":1234", ep)
	})

	t.Run("endpoint with no port errors if no exposed port", func(t *testing.T) {
		srv := c.Container().
			From("python").
			WithExec([]string{"python", "-m", "http.server"})

		_, err := srv.Endpoint(ctx)
		require.Error(t, err)
	})
}

func TestContainerPortLifecycle(t *testing.T) {
	t.Parallel()

	checkNotDisabled(t, engine.ServicesDNSEnvName)

	c, ctx := connect(t)
	defer c.Close()

	withPorts := c.Container().
		From("python").
		WithExposedPort(8000, dagger.ContainerWithExposedPortOpts{
			Description: "eight thousand tcp",
		}).
		WithExposedPort(8000, dagger.ContainerWithExposedPortOpts{
			Protocol:    dagger.Udp,
			Description: "eight thousand udp",
		}).
		WithExposedPort(5432)

	cid, err := withPorts.ID(ctx)
	require.NoError(t, err)

	res := struct {
		Container struct {
			ExposedPorts []struct {
				Port        int
				Protocol    dagger.NetworkProtocol
				Description *string
			}
		}
	}{}

	getPorts := `query Test($id: ContainerID!) {
		container(id: $id) {
			exposedPorts {
				port
				protocol
				description
			}
		}
	}`

	err = testutil.Query(getPorts, &res, &testutil.QueryOptions{
		Variables: map[string]interface{}{
			"id": cid,
		},
	})
	require.NoError(t, err)
	require.Len(t, res.Container.ExposedPorts, 3)

	ports := map[string]*string{}
	for _, p := range res.Container.ExposedPorts {
		ports[fmt.Sprintf("%d/%s", p.Port, p.Protocol)] = p.Description
	}

	desc, ok := ports["8000/TCP"]
	require.True(t, ok)
	require.NotNil(t, desc)
	require.Equal(t, "eight thousand tcp", *desc)

	desc, ok = ports["8000/UDP"]
	require.True(t, ok)
	require.NotNil(t, desc)
	require.Equal(t, "eight thousand udp", *desc)

	desc, ok = ports["5432/TCP"]
	require.True(t, ok)
	require.Nil(t, desc)

	withoutTCP := withPorts.WithoutExposedPort(8000)
	cid, err = withoutTCP.ID(ctx)
	require.NoError(t, err)
	err = testutil.Query(getPorts, &res, &testutil.QueryOptions{
		Variables: map[string]interface{}{
			"id": cid,
		},
	})
	require.NoError(t, err)
	require.Len(t, res.Container.ExposedPorts, 2)

	ports = map[string]*string{}
	for _, p := range res.Container.ExposedPorts {
		ports[fmt.Sprintf("%d/%s", p.Port, p.Protocol)] = p.Description
	}

	desc, ok = ports["8000/UDP"]
	require.True(t, ok)
	require.NotNil(t, desc)
	require.Equal(t, "eight thousand udp", *desc)

	desc, ok = ports["5432/TCP"]
	require.True(t, ok)
	require.Nil(t, desc)

	withoutUDP := withPorts.WithoutExposedPort(8000, dagger.ContainerWithoutExposedPortOpts{
		Protocol: dagger.Udp,
	})
	cid, err = withoutUDP.ID(ctx)
	require.NoError(t, err)
	err = testutil.Query(getPorts, &res, &testutil.QueryOptions{
		Variables: map[string]interface{}{
			"id": cid,
		},
	})
	require.NoError(t, err)
	require.Len(t, res.Container.ExposedPorts, 2)

	ports = map[string]*string{}
	for _, p := range res.Container.ExposedPorts {
		ports[fmt.Sprintf("%d/%s", p.Port, p.Protocol)] = p.Description
	}

	desc, ok = ports["8000/TCP"]
	require.True(t, ok)
	require.NotNil(t, desc)
	require.NotNil(t, desc)
	require.Equal(t, "eight thousand tcp", *desc)

	desc, ok = ports["5432/TCP"]
	require.True(t, ok)
	require.Nil(t, desc)
}

func TestContainerPortOCIConfig(t *testing.T) {
	t.Parallel()

	checkNotDisabled(t, engine.ServicesDNSEnvName)

	c, ctx := connect(t)
	defer c.Close()

	withPorts := c.Container().
		From("python").
		WithExposedPort(8000, dagger.ContainerWithExposedPortOpts{
			Description: "eight thousand tcp",
		}).
		WithExposedPort(8000, dagger.ContainerWithExposedPortOpts{
			Protocol:    dagger.Udp,
			Description: "eight thousand udp",
		}).
		WithExposedPort(5432).
		WithExposedPort(5432, dagger.ContainerWithExposedPortOpts{
			Protocol: dagger.Udp,
		})

	dest := t.TempDir()

	imageTar := filepath.Join(dest, "image.tar")

	_, err := withPorts.Export(ctx, imageTar)
	require.NoError(t, err)

	image, err := tarball.ImageFromPath(imageTar, nil)
	require.NoError(t, err)

	config, err := image.ConfigFile()
	require.NoError(t, err)
	ports := []string{}
	for k := range config.Config.ExposedPorts {
		ports = append(ports, k)
	}
	require.ElementsMatch(t, []string{"8000/tcp", "8000/udp", "5432/tcp", "5432/udp"}, ports)

	withoutPorts := withPorts.
		WithoutExposedPort(8000, dagger.ContainerWithoutExposedPortOpts{
			Protocol: dagger.Udp,
		}).
		WithoutExposedPort(5432)

	imageTar = filepath.Join(dest, "image-without.tar")

	_, err = withoutPorts.Export(ctx, imageTar)
	require.NoError(t, err)

	image, err = tarball.ImageFromPath(imageTar, nil)
	require.NoError(t, err)

	config, err = image.ConfigFile()
	require.NoError(t, err)
	ports = []string{}
	for k := range config.Config.ExposedPorts {
		ports = append(ports, k)
	}
	require.ElementsMatch(t, []string{"8000/tcp", "5432/udp"}, ports)
}

func TestContainerExecServices(t *testing.T) {
	t.Parallel()

	checkNotDisabled(t, engine.ServicesDNSEnvName)

	c, ctx := connect(t)
	defer c.Close()

	srv, url := httpService(ctx, t, c, "Hello, world!")

	hostname, err := srv.Hostname(ctx)
	require.NoError(t, err)

	client := c.Container().
		From(alpineImage).
		WithServiceBinding("www", srv).
		WithExec([]string{"apk", "add", "curl"}).
		WithExec([]string{"curl", "-v", url})

	_, err = client.Sync(ctx)
	require.NoError(t, err)

	stdout, err := client.Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "Hello, world!", stdout)

	stderr, err := client.Stderr(ctx)
	require.NoError(t, err)
	require.Contains(t, stderr, "Host: "+hostname+":8000")
}

func TestContainerExecServicesError(t *testing.T) {
	t.Parallel()

	checkNotDisabled(t, engine.ServicesDNSEnvName)

	c, ctx := connect(t)
	defer c.Close()

	srv := c.Container().
		From(alpineImage).
		WithExposedPort(8080).
		WithExec([]string{"sh", "-c", "echo nope; exit 42"})

	host, err := srv.Hostname(ctx)
	require.NoError(t, err)

	client := c.Container().
		From(alpineImage).
		WithServiceBinding("www", srv).
		WithExec([]string{"wget", "http://www:8080"})

	_, err = client.Sync(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "start "+host+" (aliased as www): exited:")
}

func TestContainerServiceNoExec(t *testing.T) {
	t.Parallel()

	checkNotDisabled(t, engine.ServicesDNSEnvName)

	c, ctx := connect(t)
	defer c.Close()

	srv := c.Container().
		From(alpineImage).
		WithExposedPort(8080).
		// using error to compare hostname after WithServiceBinding
		WithDefaultArgs(dagger.ContainerWithDefaultArgsOpts{
			Args: []string{"sh", "-c", "echo nope; exit 42"},
		})

	host, err := srv.Hostname(ctx)
	require.NoError(t, err)

	client := c.Container().
		From(alpineImage).
		WithServiceBinding("www", srv).
		WithExec([]string{"wget", "http://www:8080"})

	_, err = client.Sync(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "start "+host+" (aliased as www)")
}

//go:embed testdata/udp-service.go
var udpSrc string

func TestContainerExecUDPServices(t *testing.T) {
	t.Parallel()

	checkNotDisabled(t, engine.ServicesDNSEnvName)

	c, ctx := connect(t)
	defer c.Close()

	srv := c.Container().
		From("golang:1.18.2-alpine").
		WithMountedFile("/src/main.go",
			c.Directory().WithNewFile("main.go", udpSrc).File("main.go")).
		WithExposedPort(4321, dagger.ContainerWithExposedPortOpts{
			Protocol: dagger.Udp,
		}).
		WithExec([]string{"go", "run", "/src/main.go"})

	client := c.Container().
		From(alpineImage).
		WithExec([]string{"apk", "add", "socat"}).
		WithServiceBinding("echo", srv).
		WithExec([]string{"socat", "-", "udp:echo:4321"}, dagger.ContainerWithExecOpts{
			Stdin: "Hello, world!",
		})

	_, err := client.Sync(ctx)
	require.NoError(t, err)

	stdout, err := client.Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "Hello, world!", stdout)
}

func TestContainerExecServiceAlias(t *testing.T) {
	t.Parallel()

	checkNotDisabled(t, engine.ServicesDNSEnvName)

	c, ctx := connect(t)
	defer c.Close()

	srv, _ := httpService(ctx, t, c, "Hello, world!")

	client := c.Container().
		From(alpineImage).
		WithServiceBinding("hello", srv).
		WithExec([]string{"apk", "add", "curl"}).
		WithExec([]string{"curl", "-v", "http://hello:8000"})

	_, err := client.Sync(ctx)
	require.NoError(t, err)

	stdout, err := client.Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "Hello, world!", stdout)

	stderr, err := client.Stderr(ctx)
	require.NoError(t, err)
	require.Contains(t, stderr, "Host: hello:8000")
}

//go:embed testdata/pipe.go
var pipeSrc string

func TestContainerExecServicesDeduping(t *testing.T) {
	t.Parallel()

	checkNotDisabled(t, engine.ServicesDNSEnvName)

	c, ctx := connect(t)
	defer c.Close()

	srv := c.Container().
		From("golang:1.18.2-alpine").
		WithMountedFile("/src/main.go",
			c.Directory().WithNewFile("main.go", pipeSrc).File("main.go")).
		WithExposedPort(8080).
		WithExec([]string{"go", "run", "/src/main.go"})

	client := c.Container().
		From(alpineImage).
		WithExec([]string{"apk", "add", "curl"}).
		WithServiceBinding("www", srv).
		WithEnvVariable("CACHEBUST", identity.NewID())

	eg := new(errgroup.Group)
	eg.Go(func() error {
		_, err := client.
			WithExec([]string{"curl", "-s", "-X", "POST", "http://www:8080/write", "-d", "hello"}).
			Sync(ctx)
		return err
	})

	msg, err := client.
		WithExec([]string{"curl", "-s", "http://www:8080/read"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello", msg)

	require.NoError(t, eg.Wait())
}

func TestContainerExecServicesChained(t *testing.T) {
	t.Parallel()

	checkNotDisabled(t, engine.ServicesDNSEnvName)

	c, ctx := connect(t)
	defer c.Close()

	srv, _ := httpService(ctx, t, c, "0\n")

	for i := 1; i < 10; i++ {
		httpURL, err := srv.Endpoint(ctx, dagger.ContainerEndpointOpts{
			Scheme: "http",
		})
		require.NoError(t, err)

		srv = c.Container().
			From("python").
			WithFile(
				"/srv/www/index.html",
				c.HTTP(httpURL, dagger.HTTPOpts{
					ExperimentalServiceHost: srv,
				}),
			).
			WithExec([]string{"sh", "-c", "echo $0 >> /srv/www/index.html", strconv.Itoa(i)}).
			WithWorkdir("/srv/www").
			WithExposedPort(8000).
			WithExec([]string{"python", "-m", "http.server"})
	}

	fileContent, err := c.Container().
		From(alpineImage).
		WithServiceBinding("www", srv).
		WithExec([]string{"wget", "http://www:8000"}).
		WithExec([]string{"cat", "index.html"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "0\n1\n2\n3\n4\n5\n6\n7\n8\n9\n", fileContent)
}

func TestContainerBuildService(t *testing.T) {
	t.Parallel()

	checkNotDisabled(t, engine.ServicesDNSEnvName)

	c, ctx := connect(t)
	defer c.Close()

	t.Run("building with service dependency", func(t *testing.T) {
		content := identity.NewID()
		srv, httpURL := httpService(ctx, t, c, content)

		src := c.Directory().
			WithNewFile("Dockerfile",
				`FROM `+alpineImage+`
WORKDIR /src
RUN wget `+httpURL+`
CMD cat index.html
`)

		fileContent, err := c.Container().
			WithServiceBinding("www", srv).
			Build(src).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, content, fileContent)
	})

	t.Run("building a directory that depends on a service (Container.Build)", func(t *testing.T) {
		content := identity.NewID()
		srv, httpURL := httpService(ctx, t, c, content)

		src := c.Directory().
			WithNewFile("Dockerfile",
				`FROM `+alpineImage+`
WORKDIR /src
RUN wget `+httpURL+`
CMD cat index.html
`)

		gitDaemon, repoURL := gitService(ctx, t, c, src)

		gitDir := c.Git(repoURL, dagger.GitOpts{ExperimentalServiceHost: gitDaemon}).
			Branch("main").
			Tree()

		fileContent, err := c.Container().
			WithServiceBinding("www", srv).
			Build(gitDir).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, content, fileContent)
	})

	t.Run("building a directory that depends on a service (Directory.DockerBuild)", func(t *testing.T) {
		content := identity.NewID()
		srv, httpURL := httpService(ctx, t, c, content)

		src := c.Directory().
			WithNewFile("Dockerfile",
				`FROM `+alpineImage+`
WORKDIR /src
RUN wget `+httpURL+`
CMD cat index.html
`)

		gitDaemon, repoURL := gitService(ctx, t, c, src)

		gitDir := c.Git(repoURL, dagger.GitOpts{ExperimentalServiceHost: gitDaemon}).
			Branch("main").
			Tree()

		fileContent, err := gitDir.
			DockerBuild().
			WithServiceBinding("www", srv).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, content, fileContent)
	})
}

func TestContainerExportServices(t *testing.T) {
	t.Parallel()

	checkNotDisabled(t, engine.ServicesDNSEnvName)

	c, ctx := connect(t)
	defer c.Close()

	content := identity.NewID()
	srv, httpURL := httpService(ctx, t, c, content)

	client := c.Container().
		From(alpineImage).
		WithServiceBinding("www", srv).
		WithExec([]string{"wget", httpURL})

	filePath := filepath.Join(t.TempDir(), "image.tar")
	ok, err := client.Export(ctx, filePath)
	require.NoError(t, err)
	require.True(t, ok)
}

func TestContainerMultiPlatformExportServices(t *testing.T) {
	t.Parallel()

	checkNotDisabled(t, engine.ServicesDNSEnvName)

	c, ctx := connect(t)
	defer c.Close()

	variants := make([]*dagger.Container, 0, len(platformToUname))
	for platform := range platformToUname {
		srv, url := httpService(ctx, t, c, string(platform))

		ctr := c.Container(dagger.ContainerOpts{Platform: platform}).
			From(alpineImage).
			WithServiceBinding("www", srv).
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
	t.Parallel()

	checkNotDisabled(t, engine.ServicesDNSEnvName)

	c, ctx := connect(t)
	defer c.Close()

	content := identity.NewID()
	srv, url := httpService(ctx, t, c, content)

	testRef := registryRef("services-container-publish")
	pushedRef, err := c.Container().
		From(alpineImage).
		WithServiceBinding("www", srv).
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
	t.Parallel()

	checkNotDisabled(t, engine.ServicesDNSEnvName)

	c, ctx := connect(t)
	defer c.Close()

	content := identity.NewID()
	srv, url := httpService(ctx, t, c, content)

	fileContent, err := c.Container().
		From(alpineImage).
		WithServiceBinding("www", srv).
		WithWorkdir("/sub/out").
		WithExec([]string{"wget", url}).
		Rootfs().
		File("/sub/out/index.html").
		Contents(ctx)

	require.NoError(t, err)
	require.Equal(t, content, fileContent)
}

func TestContainerWithRootFSServices(t *testing.T) {
	t.Parallel()

	checkNotDisabled(t, engine.ServicesDNSEnvName)

	c, ctx := connect(t)
	defer c.Close()

	content := identity.NewID()
	srv, url := httpService(ctx, t, c, content)

	gitDaemon, repoURL := gitService(ctx, t, c,
		// this little maneuver commits the entire rootfs into a git repo
		c.Container().
			From(alpineImage).
			WithServiceBinding("www", srv).
			WithWorkdir("/sub/out").
			WithExec([]string{"wget", url}).
			// NB(vito): related to the package-level comment: Rootfs is not eager,
			// so this is actually OK. File and Directory are eager because they need
			// to check that the path exists (and is a file/dir), but Rootfs always
			// exists, and is always a directory.
			Rootfs())

	gitDir := c.Git(repoURL, dagger.GitOpts{ExperimentalServiceHost: gitDaemon}).
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
	t.Parallel()

	checkNotDisabled(t, engine.ServicesDNSEnvName)

	c, ctx := connect(t)
	defer c.Close()

	content := identity.NewID()
	srv, url := httpService(ctx, t, c, content)

	wget := c.Container().
		From(alpineImage).
		WithServiceBinding("www", srv).
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
	t.Parallel()

	checkNotDisabled(t, engine.ServicesDNSEnvName)

	c, ctx := connect(t)
	defer c.Close()

	content := identity.NewID()
	srv, url := httpService(ctx, t, c, content)

	client := c.Container().
		From(alpineImage).
		WithServiceBinding("www", srv).
		WithWorkdir("/out").
		WithExec([]string{"wget", url})

	fileContent, err := client.File("index.html").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, content, fileContent)
}

func TestContainerWithServiceFileDirectory(t *testing.T) {
	t.Parallel()

	checkNotDisabled(t, engine.ServicesDNSEnvName)

	c, ctx := connect(t)
	defer c.Close()

	response := identity.NewID()
	srv, httpURL := httpService(ctx, t, c, response)
	httpFile := c.HTTP(httpURL, dagger.HTTPOpts{
		ExperimentalServiceHost: srv,
	})

	gitDaemon, repoURL := gitService(ctx, t, c, c.Directory().WithNewFile("README.md", response))
	gitDir := c.Git(repoURL, dagger.GitOpts{ExperimentalServiceHost: gitDaemon}).
		Branch("main").
		Tree()

	t.Run("mounting", func(t *testing.T) {
		useBoth := c.Container().
			From(alpineImage).
			WithMountedDirectory("/mnt/repo", gitDir).
			WithMountedFile("/mnt/index.html", httpFile)

		httpContent, err := useBoth.File("/mnt/index.html").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, response, httpContent)

		gitContent, err := useBoth.File("/mnt/repo/README.md").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, response, gitContent)
	})

	t.Run("copying", func(t *testing.T) {
		useBoth := c.Container().
			From(alpineImage).
			WithDirectory("/mnt/repo", gitDir).
			WithFile("/mnt/index.html", httpFile)

		httpContent, err := useBoth.File("/mnt/index.html").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, response, httpContent)

		gitContent, err := useBoth.File("/mnt/repo/README.md").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, response, gitContent)
	})
}

func TestDirectoryServiceEntries(t *testing.T) {
	t.Parallel()

	checkNotDisabled(t, engine.ServicesDNSEnvName)

	c, ctx := connect(t)
	defer c.Close()

	content := identity.NewID()

	gitDaemon, repoURL := gitService(ctx, t, c, c.Directory().WithNewFile("README.md", content))

	entries, err := c.Git(repoURL, dagger.GitOpts{ExperimentalServiceHost: gitDaemon}).
		Branch("main").
		Tree().
		Entries(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"README.md"}, entries)
}

func TestDirectoryServiceSync(t *testing.T) {
	t.Parallel()

	checkNotDisabled(t, engine.ServicesDNSEnvName)

	t.Run("triggers error", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)
		defer c.Close()

		content := identity.NewID()
		gitDaemon, repoURL := gitService(ctx, t, c, c.Directory().WithNewFile("README.md", content))
		_, err := c.Git(repoURL, dagger.GitOpts{ExperimentalServiceHost: gitDaemon}).
			Branch("foobar").
			Tree().
			Sync(ctx)
		require.Error(t, err)
	})

	t.Run("with chaining", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)
		defer c.Close()

		content := identity.NewID()
		gitDaemon, repoURL := gitService(ctx, t, c, c.Directory().WithNewFile("README.md", content))
		repo, err := c.Git(repoURL, dagger.GitOpts{ExperimentalServiceHost: gitDaemon}).
			Branch("main").
			Tree().
			Sync(ctx)
		require.NoError(t, err)

		entries, err := repo.Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"README.md"}, entries)
	})
}

func TestDirectoryServiceTimestamp(t *testing.T) {
	t.Parallel()

	checkNotDisabled(t, engine.ServicesDNSEnvName)

	c, ctx := connect(t)
	defer c.Close()

	content := identity.NewID()
	gitDaemon, repoURL := gitService(ctx, t, c, c.Directory().WithNewFile("README.md", content))

	ts := time.Date(1991, 6, 3, 0, 0, 0, 0, time.UTC)
	stamped := c.Git(repoURL, dagger.GitOpts{ExperimentalServiceHost: gitDaemon}).
		Branch("main").
		Tree().
		WithTimestamps(int(ts.Unix()))

	stdout, err := c.Container().From(alpineImage).
		WithDirectory("/repo", stamped).
		WithExec([]string{"stat", "/repo/README.md"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, stdout, "1991-06-03")
}

func TestDirectoryWithDirectoryFileServices(t *testing.T) {
	t.Parallel()

	checkNotDisabled(t, engine.ServicesDNSEnvName)

	c, ctx := connect(t)
	defer c.Close()

	content := identity.NewID()

	gitSrv, repoURL := gitService(ctx, t, c, c.Directory().WithNewFile("README.md", content))

	httpSrv, httpURL := httpService(ctx, t, c, content)

	useBoth := c.Directory().
		WithDirectory("/repo", c.Git(repoURL, dagger.GitOpts{ExperimentalServiceHost: gitSrv}).Branch("main").Tree()).
		WithFile("/index.html", c.HTTP(httpURL, dagger.HTTPOpts{ExperimentalServiceHost: httpSrv}))

	entries, err := useBoth.Directory("/repo").Entries(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"README.md"}, entries)

	fileContent, err := useBoth.File("/index.html").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, content, fileContent)
}

func TestDirectoryServiceExport(t *testing.T) {
	t.Parallel()

	checkNotDisabled(t, engine.ServicesDNSEnvName)

	c, ctx := connect(t)
	defer c.Close()

	content := identity.NewID()

	gitDaemon, repoURL := gitService(ctx, t, c, c.Directory().WithNewFile("README.md", content))

	dest := t.TempDir()

	ok, err := c.Git(repoURL, dagger.GitOpts{ExperimentalServiceHost: gitDaemon}).
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
	t.Parallel()

	checkNotDisabled(t, engine.ServicesDNSEnvName)

	c, ctx := connect(t)
	defer c.Close()

	content := identity.NewID()

	gitDaemon, repoURL := gitService(ctx, t, c, c.Directory().WithNewFile("README.md", content))

	fileContent, err := c.Git(repoURL, dagger.GitOpts{ExperimentalServiceHost: gitDaemon}).
		Branch("main").
		Tree().
		File("README.md").
		Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, content, fileContent)
}

func TestFileServiceSync(t *testing.T) {
	t.Parallel()

	checkNotDisabled(t, engine.ServicesDNSEnvName)

	t.Run("triggers error", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)
		defer c.Close()

		content := identity.NewID()
		httpSrv, httpURL := httpService(ctx, t, c, content)

		_, err := c.HTTP(httpURL+"/foobar", dagger.HTTPOpts{
			ExperimentalServiceHost: httpSrv,
		}).Sync(ctx)

		require.Error(t, err)
		require.Contains(t, err.Error(), "status 404")
	})

	t.Run("with chaining", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)
		defer c.Close()

		content := identity.NewID()
		httpSrv, httpURL := httpService(ctx, t, c, content)

		file, err := c.HTTP(httpURL, dagger.HTTPOpts{
			ExperimentalServiceHost: httpSrv,
		}).Sync(ctx)
		require.NoError(t, err)

		fileContent, err := file.Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, content, fileContent)
	})
}

func TestFileServiceExport(t *testing.T) {
	t.Parallel()

	checkNotDisabled(t, engine.ServicesDNSEnvName)

	c, ctx := connect(t)
	defer c.Close()

	content := identity.NewID()

	gitDaemon, repoURL := gitService(ctx, t, c, c.Directory().WithNewFile("README.md", content))

	dest := t.TempDir()
	filePath := filepath.Join(dest, "README.md")

	ok, err := c.Git(repoURL, dagger.GitOpts{ExperimentalServiceHost: gitDaemon}).
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
	t.Parallel()

	checkNotDisabled(t, engine.ServicesDNSEnvName)

	c, ctx := connect(t)
	defer c.Close()

	content := identity.NewID()

	httpSrv, httpURL := httpService(ctx, t, c, content)

	ts := time.Date(1991, 6, 3, 0, 0, 0, 0, time.UTC)
	stamped := c.HTTP(httpURL, dagger.HTTPOpts{ExperimentalServiceHost: httpSrv}).
		WithTimestamps(int(ts.Unix()))

	stdout, err := c.Container().From(alpineImage).
		WithFile("/index.html", stamped).
		WithExec([]string{"stat", "/index.html"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, stdout, "1991-06-03")
}

func httpService(ctx context.Context, t *testing.T, c *dagger.Client, content string) (*dagger.Container, string) {
	t.Helper()

	srv := c.Container().
		From("python").
		WithMountedDirectory(
			"/srv/www",
			c.Directory().WithNewFile("index.html", content),
		).
		WithWorkdir("/srv/www").
		WithExposedPort(8000).
		WithExec([]string{"python", "-m", "http.server"})

	httpURL, err := srv.Endpoint(ctx, dagger.ContainerEndpointOpts{
		Scheme: "http",
	})
	require.NoError(t, err)

	return srv, httpURL
}

func gitService(ctx context.Context, t *testing.T, c *dagger.Client, content *dagger.Directory) (*dagger.Container, string) {
	t.Helper()

	const gitPort = 9418
	gitDaemon := c.Container().
		From(alpineImage).
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
	git add * || true
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

	gitHost, err := gitDaemon.Hostname(ctx)
	require.NoError(t, err)

	repoURL := fmt.Sprintf("git://%s/repo.git", gitHost)

	return gitDaemon, repoURL
}
