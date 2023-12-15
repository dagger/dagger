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
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/dagger/network"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

func TestServiceHostnamesAreStable(t *testing.T) {
	t.Parallel()

	hostname := func(ctx context.Context, c *dagger.Client) string {
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
			WithExec([]string{"python", "-m", "http.server"}).
			AsService()

		hostname, err := srv.Hostname(ctx)
		require.NoError(t, err)

		return hostname
	}

	t.Run("hostnames are different for different services", func(t *testing.T) {
		c, ctx := connect(t)

		srv1 := c.Container().
			From("python").
			WithExposedPort(8000).
			WithExec([]string{"python", "-m", "http.server"}).
			AsService()

		srv2 := c.Container().
			From("python").
			WithExposedPort(8001).
			WithExec([]string{"python", "-m", "http.server", "8081"}).
			AsService()

		hn1, err := srv1.Hostname(ctx)
		require.NoError(t, err)
		hn2, err := srv2.Hostname(ctx)
		require.NoError(t, err)

		require.NotEqual(t, hn1, hn2)
	})

	t.Run("hostnames are stable within a session", func(t *testing.T) {
		c, ctx := connect(t)

		hosts := map[string]int{}
		for i := 0; i < 10; i++ {
			hosts[hostname(ctx, c)]++
		}

		require.Len(t, hosts, 1)
	})

	t.Run("hostnames are stable across sessions", func(t *testing.T) {
		hosts := map[string]int{}

		for i := 0; i < 5; i++ {
			c, ctx := connect(t)
			hosts[hostname(ctx, c)]++
		}

		require.Len(t, hosts, 1)
	})
}

func TestServiceHostnameEndpoint(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	t.Run("hostname is same as endpoint", func(t *testing.T) {
		srv := c.Container().
			From("python").
			WithExposedPort(8000).
			WithExec([]string{"python", "-m", "http.server"}).
			AsService()

		hn, err := srv.Hostname(ctx)
		require.NoError(t, err)

		ep, err := srv.Endpoint(ctx)
		require.NoError(t, err)

		require.Equal(t, hn+":8000", ep)
	})

	t.Run("endpoint can specify arbitrary port", func(t *testing.T) {
		srv := c.Container().
			From("python").
			WithExec([]string{"python", "-m", "http.server"}).
			AsService()

		hn, err := srv.Hostname(ctx)
		require.NoError(t, err)

		ep, err := srv.Endpoint(ctx, dagger.ServiceEndpointOpts{
			Port: 1234,
		})
		require.NoError(t, err)

		require.Equal(t, hn+":1234", ep)
	})

	t.Run("endpoint with no port errors if no exposed port", func(t *testing.T) {
		srv := c.Container().
			From("python").
			WithExec([]string{"python", "-m", "http.server"}).
			AsService()

		_, err := srv.Endpoint(ctx)
		require.Error(t, err)
	})
}

func TestServicePorts(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	srv := c.Container().
		From("python").
		WithExposedPort(8000, dagger.ContainerWithExposedPortOpts{
			Description: "eight thousand",
		}).
		WithExposedPort(9000, dagger.ContainerWithExposedPortOpts{
			Description: "nine thousand",
			Protocol:    dagger.Udp,
		}).
		WithExec([]string{"python", "-m", "http.server"}).
		AsService()

	portCfgs, err := srv.Ports(ctx)
	require.NoError(t, err)

	for i, cfg := range portCfgs {
		port, err := cfg.Port(ctx)
		require.NoError(t, err)

		desc, err := cfg.Description(ctx)
		require.NoError(t, err)

		proto, err := cfg.Protocol(ctx)
		require.NoError(t, err)

		switch i {
		case 0:
			require.Equal(t, 8000, port)
			require.Equal(t, "eight thousand", desc)
			require.Equal(t, dagger.Tcp, proto)
		case 1:
			require.Equal(t, 9000, port)
			require.Equal(t, "nine thousand", desc)
			require.Equal(t, dagger.Udp, proto)
		}
	}
}

func TestContainerPortLifecycle(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

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

	c, ctx := connect(t)

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

func TestContainerExecServicesSimple(t *testing.T) {
	t.Parallel()
	c, ctx := connect(t)

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

	c, ctx := connect(t)

	srv := c.Container().
		From(alpineImage).
		WithExposedPort(8080).
		WithExec([]string{"sh", "-c", "echo nope; exit 42"}).
		AsService()

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

	c, ctx := connect(t)

	srv := c.Container().
		From(alpineImage).
		WithExposedPort(8080).
		// using error to compare hostname after WithServiceBinding
		WithDefaultArgs([]string{"sh", "-c", "echo nope; exit 42"}).
		AsService()

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

	c, ctx := connect(t)

	srv := c.Container().
		From(golangImage).
		WithMountedFile("/src/main.go",
			c.Directory().WithNewFile("main.go", udpSrc).File("main.go")).
		WithExposedPort(4321, dagger.ContainerWithExposedPortOpts{
			Protocol: dagger.Udp,
		}).
		// use TCP :4322 for health-check to avoid test flakiness, since UDP dial
		// health-checks aren't really a thing
		WithExposedPort(4322).
		WithExec([]string{"go", "run", "/src/main.go"}).
		AsService()

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

	c, ctx := connect(t)

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

	c, ctx := connect(t)

	srv := c.Container().
		From(golangImage).
		WithMountedFile("/src/main.go",
			c.Directory().WithNewFile("main.go", pipeSrc).File("main.go")).
		WithExposedPort(8080).
		WithExec([]string{"go", "run", "/src/main.go"}).
		AsService()

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

	c, ctx := connect(t)

	srv, _ := httpService(ctx, t, c, "0\n")

	for i := 1; i < 10; i++ {
		httpURL, err := srv.Endpoint(ctx, dagger.ServiceEndpointOpts{
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
			WithExec([]string{"python", "-m", "http.server"}).
			AsService()
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

func TestContainerExecServicesNestedExec(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	nestingLimit := calculateNestingLimit(ctx, c, t)

	thisRepoPath, err := filepath.Abs("../..")
	require.NoError(t, err)

	code := c.Host().Directory(thisRepoPath, dagger.HostDirectoryOpts{
		Include: []string{"core/integration/testdata/nested-c2c/", "sdk/go/", "go.mod", "go.sum"},
	})

	content := identity.NewID()
	srv, svcURL := httpService(ctx, t, c, content)

	fileContent, err := c.Container().
		From(golangImage).
		With(goCache(c)).
		WithServiceBinding("www", srv).
		WithMountedDirectory("/src", code).
		WithWorkdir("/src").
		WithExec([]string{
			"go", "run", "./core/integration/testdata/nested-c2c/",
			"exec", strconv.Itoa(nestingLimit), svcURL,
		}, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, content, fileContent)
}

func TestContainerExecServicesNestedHTTP(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	nestingLimit := calculateNestingLimit(ctx, c, t)

	thisRepoPath, err := filepath.Abs("../..")
	require.NoError(t, err)

	code := c.Host().Directory(thisRepoPath, dagger.HostDirectoryOpts{
		Include: []string{"core/integration/testdata/nested-c2c/", "sdk/go/", "go.mod", "go.sum"},
	})

	content := identity.NewID()
	srv, svcURL := httpService(ctx, t, c, content)

	fileContent, err := c.Container().
		From(golangImage).
		WithServiceBinding("www", srv).
		WithMountedDirectory("/src", code).
		WithWorkdir("/src").
		WithMountedCache("/go/pkg/mod", c.CacheVolume("go-mod")).
		WithEnvVariable("GOMODCACHE", "/go/pkg/mod").
		WithMountedCache("/go/build-cache", c.CacheVolume("go-build")).
		WithEnvVariable("GOCACHE", "/go/build-cache").
		WithExec([]string{
			"go", "run", "./core/integration/testdata/nested-c2c/",
			"http", strconv.Itoa(nestingLimit), svcURL,
		}, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, content, fileContent)
}

func TestContainerExecServicesNestedGit(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	nestingLimit := calculateNestingLimit(ctx, c, t)

	thisRepoPath, err := filepath.Abs("../..")
	require.NoError(t, err)

	code := c.Host().Directory(thisRepoPath, dagger.HostDirectoryOpts{
		Include: []string{"core/integration/testdata/nested-c2c/", "sdk/go/", "go.mod", "go.sum"},
	})

	content := identity.NewID()
	srv, svcURL := gitService(ctx, t, c, c.Directory().WithNewFile("/index.html", content))

	fileContent, err := c.Container().
		From(golangImage).
		WithServiceBinding("www", srv).
		WithMountedDirectory("/src", code).
		WithWorkdir("/src").
		WithMountedCache("/go/pkg/mod", c.CacheVolume("go-mod")).
		WithEnvVariable("GOMODCACHE", "/go/pkg/mod").
		WithMountedCache("/go/build-cache", c.CacheVolume("go-build")).
		WithEnvVariable("GOCACHE", "/go/build-cache").
		WithExec([]string{
			"go", "run", "./core/integration/testdata/nested-c2c/",
			"git", strconv.Itoa(nestingLimit), svcURL,
		}, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, content, fileContent)
}

func TestContainerExportServices(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

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

	c, ctx := connect(t)

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

	c, ctx := connect(t)

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

	c, ctx := connect(t)

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

	c, ctx := connect(t)

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

	c, ctx := connect(t)

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

	c, ctx := connect(t)

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

	c, ctx := connect(t)

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

	c, ctx := connect(t)

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

	t.Run("triggers error", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

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

	c, ctx := connect(t)

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

	c, ctx := connect(t)

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

	c, ctx := connect(t)

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

	c, ctx := connect(t)

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

	t.Run("triggers error", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

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

	c, ctx := connect(t)

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

	c, ctx := connect(t)

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

// TestServiceStartStop tests that a service can be started and stopped. While
// started it can be reached by containers that do not explicitly bind it.
//
// TODO(vito): test that the service stops when the client closes... somehow
func TestServiceStartStop(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	content := identity.NewID()

	httpSrv, httpURL := httpService(ctx, t, c, content)

	fetch := func() (string, error) {
		return c.Container().
			From(alpineImage).
			WithEnvVariable("BUST", identity.NewID()).
			WithExec([]string{"wget", "-O-", httpURL}).
			Stdout(ctx)
	}

	out, err := fetch()
	require.Error(t, err)
	require.Empty(t, out)

	_, err = httpSrv.Start(ctx)
	require.NoError(t, err)

	out, err = fetch()
	require.NoError(t, err)
	require.Equal(t, out, content)

	_, err = httpSrv.Stop(ctx)
	require.NoError(t, err)

	out, err = fetch()
	require.Error(t, err)
	require.Empty(t, out)
}

// TestServiceNoCrossTalk shows that services spawned in one client cannot be
// reached by another client.
func TestServiceNoCrossTalk(t *testing.T) {
	t.Parallel()

	c1, ctx1 := connect(t)
	defer c1.Close()

	c2, ctx2 := connect(t)
	defer c2.Close()

	content1 := identity.NewID()

	httpSrv1, httpURL1 := httpService(ctx1, t, c1, content1)

	fetch := func(ctx context.Context, c *dagger.Client) (string, error) {
		return c.Container().
			From(alpineImage).
			WithEnvVariable("BUST", identity.NewID()).
			WithExec([]string{"wget", "-O-", httpURL1}).
			Stdout(ctx)
	}

	_, err := httpSrv1.Start(ctx1)
	require.NoError(t, err)

	out, err := fetch(ctx1, c1)
	require.NoError(t, err)
	require.Equal(t, out, content1)

	out, err = fetch(ctx2, c2)
	require.Error(t, err)
	require.Empty(t, out)
}

func TestServiceHostToContainer(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	content := identity.NewID()

	t.Run("no options means bind to random", func(t *testing.T) {
		t.Parallel()

		srv := c.Container().
			From("python").
			WithMountedDirectory(
				"/srv/www",
				c.Directory().WithNewFile("index.html", content),
			).
			WithWorkdir("/srv/www").
			WithExposedPort(8000).
			WithExec([]string{"python", "-m", "http.server"}).
			AsService()

		tunnel, err := c.Host().Tunnel(srv).Start(ctx)
		require.NoError(t, err)

		defer func() {
			_, err := tunnel.Stop(ctx)
			require.NoError(t, err)
		}()

		srvURL, err := tunnel.Endpoint(ctx)
		require.NoError(t, err)

		for i := 0; i < 10; i++ {
			res, err := http.Get("http://" + srvURL)
			require.NoError(t, err)
			defer res.Body.Close()

			body, err := io.ReadAll(res.Body)
			require.NoError(t, err)

			require.Equal(t, content, string(body))
		}
	})

	t.Run("multiple ports", func(t *testing.T) {
		t.Parallel()

		srv := c.Container().
			From("python").
			WithMountedDirectory("/srv/www1",
				c.Directory().WithNewFile("index.html", content+"-1")).
			WithMountedDirectory("/srv/www2",
				c.Directory().WithNewFile("index.html", content+"-2")).
			WithExec([]string{"sh", "-c",
				`( cd /srv/www1 && python -m http.server 8000 ) &
				 ( cd /srv/www2 && python -m http.server 9000 ) &
				 wait`,
			}).
			WithExposedPort(8000).
			WithExposedPort(9000).
			AsService()

		tunnel, err := c.Host().Tunnel(srv).Start(ctx)
		require.NoError(t, err)

		defer func() {
			_, err := tunnel.Stop(ctx)
			require.NoError(t, err)
		}()

		hn, err := tunnel.Hostname(ctx)
		require.NoError(t, err)

		ports, err := tunnel.Ports(ctx)
		require.NoError(t, err)

		for i, port := range ports {
			port, err := port.Port(ctx)
			require.NoError(t, err)

			res, err := http.Get(fmt.Sprintf("http://%s:%d", hn, port))
			require.NoError(t, err)
			defer res.Body.Close()

			body, err := io.ReadAll(res.Body)
			require.NoError(t, err)

			require.Equal(t, fmt.Sprintf("%s-%d", content, i+1), string(body))
		}
	})

	t.Run("native mapping", func(t *testing.T) {
		t.Parallel()

		srv := c.Container().
			From("python").
			WithMountedDirectory("/srv/www1",
				c.Directory().WithNewFile("index.html", content+"-1")).
			WithMountedDirectory("/srv/www2",
				c.Directory().WithNewFile("index.html", content+"-2")).
			WithExec([]string{"sh", "-c",
				`( cd /srv/www1 && python -m http.server 32767 ) &
				 ( cd /srv/www2 && python -m http.server 32766 ) &
				 wait`,
			}).
			WithExposedPort(32767). // NB: trying to avoid conflicts...
			WithExposedPort(32766).
			AsService()

		tunnel, err := c.Host().Tunnel(srv, dagger.HostTunnelOpts{
			Native: true,
		}).Start(ctx)
		require.NoError(t, err)

		defer func() {
			_, err := tunnel.Stop(ctx)
			require.NoError(t, err)
		}()

		portCfgs, err := tunnel.Ports(ctx)
		require.NoError(t, err)

		ports := make([]int, len(portCfgs))
		for i, cfg := range portCfgs {
			port, err := cfg.Port(ctx)
			require.NoError(t, err)
			ports[i] = port
		}
		require.Equal(t, []int{32767, 32766}, ports)

		for i, port := range ports {
			res, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d", port))
			require.NoError(t, err)
			defer res.Body.Close()

			body, err := io.ReadAll(res.Body)
			require.NoError(t, err)

			require.Equal(t, fmt.Sprintf("%s-%d", content, i+1), string(body))
		}
	})

	t.Run("native mapping + extra ports", func(t *testing.T) {
		t.Parallel()

		srv := c.Container().
			From("python").
			WithMountedDirectory("/srv/www1",
				c.Directory().WithNewFile("index.html", content+"-1")).
			WithMountedDirectory("/srv/www2",
				c.Directory().WithNewFile("index.html", content+"-2")).
			WithExec([]string{"sh", "-c",
				`( cd /srv/www1 && python -m http.server 32765 ) &
				 ( cd /srv/www2 && python -m http.server 32764 ) &
				 wait`,
			}).
			WithExposedPort(32765). // NB: trying to avoid conflicts...
			AsService()

		tunnel, err := c.Host().Tunnel(srv, dagger.HostTunnelOpts{
			Native: true,
			Ports: []dagger.PortForward{
				{Backend: 32764, Frontend: 32764},
			},
		}).Start(ctx)
		require.NoError(t, err)

		defer func() {
			_, err := tunnel.Stop(ctx)
			require.NoError(t, err)
		}()

		portCfgs, err := tunnel.Ports(ctx)
		require.NoError(t, err)

		ports := make([]int, len(portCfgs))
		for i, cfg := range portCfgs {
			port, err := cfg.Port(ctx)
			require.NoError(t, err)
			ports[i] = port
		}
		require.Equal(t, []int{32765, 32764}, ports)

		for i, port := range ports {
			res, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d", port))
			require.NoError(t, err)
			defer res.Body.Close()

			body, err := io.ReadAll(res.Body)
			require.NoError(t, err)

			require.Equal(t, fmt.Sprintf("%s-%d", content, i+1), string(body))
		}
	})

	t.Run("no ports to forward", func(t *testing.T) {
		t.Parallel()

		srv := c.Container().
			From("python").
			WithMountedDirectory(
				"/srv/www",
				c.Directory().WithNewFile("index.html", content),
			).
			WithWorkdir("/srv/www").
			// WithExposedPort(8000). // INTENTIONAL
			WithExec([]string{"python", "-m", "http.server"}).
			AsService()

		_, err := c.Host().Tunnel(srv).ID(ctx)
		require.Error(t, err)
	})
}

func TestServiceContainerToHost(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	t.Cleanup(func() { _ = l.Close() })

	go http.Serve(l, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, r.URL.Query().Get("content"))
	}))

	_, portStr, err := net.SplitHostPort(l.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	t.Run("simple", func(t *testing.T) {
		t.Parallel()

		host := c.Host().Service([]dagger.PortForward{
			{Frontend: 80, Backend: port},
		})

		for _, content := range []string{"yes", "no", "maybe", "so"} {
			out, err := c.Container().
				From(alpineImage).
				WithServiceBinding("www", host).
				WithExec([]string{"wget", "-O-", "http://www/?content=" + content}).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, content+"\n", out)
		}
	})

	t.Run("using hostname", func(t *testing.T) {
		t.Parallel()

		host := c.Host().Service([]dagger.PortForward{
			{Frontend: 80, Backend: port},
		})

		hn, err := host.Hostname(ctx)
		require.NoError(t, err)

		out, err := c.Container().
			From(alpineImage).
			WithServiceBinding("www", host).
			WithExec([]string{"wget", "-O-", "http://" + hn + "/?content=hello"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello\n", out)
	})

	t.Run("using endpoint", func(t *testing.T) {
		t.Parallel()

		host := c.Host().Service([]dagger.PortForward{
			{Frontend: 80, Backend: port},
		})

		svcURL, err := host.Endpoint(ctx, dagger.ServiceEndpointOpts{
			Scheme: "http",
		})
		require.NoError(t, err)

		out, err := c.Container().
			From(alpineImage).
			WithServiceBinding("www", host).
			WithExec([]string{"wget", "-O-", svcURL + "/?content=hello"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello\n", out)
	})

	t.Run("multiple ports", func(t *testing.T) {
		t.Parallel()

		l2, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)

		defer l2.Close()

		go http.Serve(l2, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintln(w, r.URL.Query().Get("content")+"-2")
		}))

		_, port2Str, err := net.SplitHostPort(l2.Addr().String())
		require.NoError(t, err)
		port2, err := strconv.Atoi(port2Str)
		require.NoError(t, err)

		host := c.Host().Service([]dagger.PortForward{
			{Frontend: 80, Backend: port},
			{Frontend: 8000, Backend: port2},
		})

		out, err := c.Container().
			From(alpineImage).
			WithServiceBinding("www", host).
			WithExec([]string{"sh", "-c", `
				a=$(wget -O- http://www/?content=hey)
				b=$(wget -O- http://www:8000/?content=hey)
				echo -n $a $b
			`}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hey hey-2", out)
	})

	t.Run("no ports given", func(t *testing.T) {
		t.Parallel()

		_, err := c.Host().Service(nil).ID(ctx)
		require.Error(t, err)
	})
}

func httpService(ctx context.Context, t *testing.T, c *dagger.Client, content string) (*dagger.Service, string) {
	t.Helper()

	srv := c.Container().
		From("python").
		WithMountedDirectory(
			"/srv/www",
			c.Directory().WithNewFile("index.html", content),
		).
		WithWorkdir("/srv/www").
		WithExposedPort(8000).
		WithExec([]string{"python", "-m", "http.server"}).
		AsService()

	httpURL, err := srv.Endpoint(ctx, dagger.ServiceEndpointOpts{
		Scheme: "http",
	})
	require.NoError(t, err)

	return srv, httpURL
}

func gitService(ctx context.Context, t testing.TB, c *dagger.Client, content *dagger.Directory) (*dagger.Service, string) {
	t.Helper()
	return gitServiceWithBranch(ctx, t, c, content, "main")
}

func gitServiceWithBranch(ctx context.Context, t testing.TB, c *dagger.Client, content *dagger.Directory, branchName string) (*dagger.Service, string) {
	t.Helper()

	const gitPort = 9418
	gitDaemon := c.Container().
		From(alpineImage).
		WithExec([]string{"apk", "add", "git", "git-daemon"}).
		WithDirectory("/root/repo", content).
		WithMountedFile("/root/start.sh",
			c.Directory().
				WithNewFile("start.sh", fmt.Sprintf(`#!/bin/sh

set -e -u -x

cd /root

git config --global user.email "root@localhost"
git config --global user.name "Test User"

mkdir srv

cd repo
	git init
	git branch -m %s
	git add * || true
	git commit -m "init"
cd ..

cd srv
	git clone --bare ../repo repo.git
cd ..

git daemon --verbose --export-all --base-path=/root/srv
`, branchName)).
				File("start.sh")).
		WithExposedPort(gitPort).
		WithExec([]string{"sh", "/root/start.sh"}).
		AsService()

	gitHost, err := gitDaemon.Hostname(ctx)
	require.NoError(t, err)

	repoURL := fmt.Sprintf("git://%s/repo.git", gitHost)

	return gitDaemon, repoURL
}

var nestingLimitOnce = &sync.Once{}
var calculatedNestingLimit int

// search domains cap out at 256 chars, and these tests have to run in
// environments that may have some pre-configured (k8s) or may already be
// nested (dagger-in-dagger), so we need to calculate how deeply we can nest.
func calculateNestingLimit(ctx context.Context, c *dagger.Client, t *testing.T) int {
	nestingLimitOnce.Do(func() {
		baseSearch, err := c.Container().
			From(alpineImage).
			WithExec([]string{"grep", `^search\s`, "/etc/resolv.conf"}).
			Stdout(ctx)
		require.NoError(t, err)

		t.Logf("initial search domains list: %s", strings.TrimSpace(baseSearch))

		// first, lop off the prefix and strip any remaining whitespace
		baseSearchLen := len(strings.TrimSpace(strings.TrimPrefix(baseSearch, "search")))

		// next, calculate the length each additional domain will consume
		domainLen := len(network.ClientDomain("dummy")) + 1 // account for space

		// finally, divide the available space by the amount needed for each domain
		calculatedNestingLimit = (256 - baseSearchLen) / domainLen

		t.Logf("nesting limit: %d (256-%d)/%d",
			calculatedNestingLimit,
			baseSearchLen,
			domainLen)
	})

	if calculatedNestingLimit < 1 {
		t.Skipf("too many search domains; unable to test nesting")
	}

	return calculatedNestingLimit
}
