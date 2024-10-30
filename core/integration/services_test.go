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
	"bytes"
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
	"text/template"
	"time"

	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/dagger/network"
	"github.com/dagger/dagger/testctx"
)

type ServiceSuite struct{}

func TestService(t *testing.T) {
	testctx.Run(testCtx, t, ServiceSuite{}, Middleware()...)
}

func (ServiceSuite) TestHostnamesAreStable(ctx context.Context, t *testctx.T) {
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

	t.Run("hostnames are different for different services", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

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

	t.Run("hostnames are stable within a session", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		hosts := map[string]int{}
		for i := 0; i < 10; i++ {
			hosts[hostname(ctx, c)]++
		}

		require.Len(t, hosts, 1)
	})

	t.Run("hostnames are stable across sessions", func(ctx context.Context, t *testctx.T) {
		hosts := map[string]int{}

		for i := 0; i < 5; i++ {
			c := connect(ctx, t)
			hosts[hostname(ctx, c)]++
		}

		require.Len(t, hosts, 1)
	})
}

func (ServiceSuite) TestHostnameEndpoint(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("hostname is same as endpoint", func(ctx context.Context, t *testctx.T) {
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

	t.Run("endpoint can specify arbitrary port", func(ctx context.Context, t *testctx.T) {
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

	t.Run("endpoint with no port errors if no exposed port", func(ctx context.Context, t *testctx.T) {
		srv := c.Container().
			From("python").
			WithExec([]string{"python", "-m", "http.server"}).
			AsService()

		_, err := srv.Endpoint(ctx)
		require.Error(t, err)
	})
}

func (ServiceSuite) TestWithHostname(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	srv := c.Container().
		From(busyboxImage).
		WithWorkdir("/srv").
		WithNewFile("index.html", "Hello, world!").
		WithExec([]string{"httpd", "-v", "-f"}).
		WithExposedPort(80).
		AsService().
		WithHostname("wwwhatsup")

	hn, err := srv.Hostname(ctx)
	require.NoError(t, err)
	require.Equal(t, "wwwhatsup", hn)

	ep, err := srv.Endpoint(ctx)
	require.NoError(t, err)

	require.Equal(t, hn+":80", ep)

	_, err = srv.Start(ctx)
	require.NoError(t, err)

	resp, err := c.Container().
		From(busyboxImage).
		WithExec([]string{"wget", "-O-", "http://wwwhatsup"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "Hello, world!", resp)
}

//go:embed testdata/counter/main.go
var counterMain string

func (ServiceSuite) TestContentAddressedModuleScoping(ctx context.Context, t *testctx.T) {
	t.Run("addressable within module", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		_, err := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/hoster").
			With(daggerExec("init", "--source=.", "--name=hoster", "--sdk=go")).
			WithNewFile("main.go", `package main

import (
	"context"
	"fmt"
)

type Hoster struct{}

func (m *Hoster) Run(ctx context.Context) error {
	srv := dag.Container().
		From("`+busyboxImage+`").
		WithWorkdir("/srv").
		WithNewFile("index.html", "I am the one who hosts.").
		WithExec([]string{"httpd", "-v", "-f"}).
		WithExposedPort(80).
		AsService()
	
	hn, err := srv.Hostname(ctx)
	if err != nil {
		return err
	}

	_, err = srv.Start(ctx)
	if err != nil {
		return err
	}

	resp, err := dag.Container().
		From("`+busyboxImage+`").
		WithExec([]string{"wget", "-O-", "http://"+hn}).
		Stdout(ctx)
	if err != nil {
		return err
	}
	if resp != "I am the one who hosts." {
		return fmt.Errorf("unexpected response: %q", resp)
	}

	return nil
}
`,
			).
			With(daggerCall("run")).
			Sync(ctx)
		require.NoError(t, err)
	})

	t.Run("addressable across modules", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		_, err := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/caller").
			With(daggerExec("init", "--source=.", "--name=caller", "--sdk=go")).
			WithNewFile("main.go", `package main

import (
	"context"
	"strconv"
	"time"

	"dagger/caller/internal/dagger"
)

type Caller struct{}

func (m *Caller) Count(ctx context.Context, service *dagger.Service, buster string) (int, error) {
	hn, err := service.Hostname(ctx)
	if err != nil {
		return 0, err
	}

	resp, err := dag.Container().
		From("`+busyboxImage+`").
		WithEnvVariable("NOW", time.Now().String()).
		WithExec([]string{"wget", "-O-", "http://"+hn}).
		Stdout(ctx)
	if err != nil {
		return 0, err
	}

	return strconv.Atoi(resp)
}
`,
			).
			WithWorkdir("/work/hoster").
			With(daggerExec("init", "--source=.", "--name=hoster", "--sdk=go")).
			With(daggerExec("install", "../caller")).
			WithNewFile("counter/main.go", counterMain).
			WithNewFile("main.go", `package main

import (
	"context"
	_ "embed"
	"fmt"
	"strconv"
	"time"
)

type Hoster struct{}

//go:embed counter/main.go
var counterMain string

func (m *Hoster) Run(ctx context.Context) error {
	counter := dag.Container().
		From("`+golangImage+`").
		WithWorkdir("/srv").
		WithNewFile("main.go", counterMain).
		WithExec([]string{"go", "run", "main.go"}).
		WithExposedPort(80).
		AsService()

	// explicitly start since we want to test that it's the same instance
	// across the following call and subsequent cross-module calls
	_, err := counter.Start(ctx)
	if err != nil {
		return err
	}

	// first query the service locally, to ensure the subsequent calls
	// start at 2
	resp, err := dag.Container().
		From("`+busyboxImage+`").
		WithEnvVariable("NOW", time.Now().String()).
		WithServiceBinding("counter", counter).
		WithExec([]string{"wget", "-O-", "http://counter"}).
		Stdout(ctx)
	if err != nil {
		return err
	}
	n, err := strconv.Atoi(resp)
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("expected %d, got %d", 1, n)
	}

	n, err = dag.Caller().Count(ctx, counter, time.Now().String())
	if err != nil {
		return err
	}
	if n != 2 {
		return fmt.Errorf("expected %d, got %d", 2, n)
	}

	n, err = dag.Caller().Count(ctx, counter, time.Now().String())
	if err != nil {
		return err
	}
	if n != 3 {
		return fmt.Errorf("expected %d, got %d", 3, n)
	}

	return nil
}
`,
			).
			With(daggerCall("run")).
			Sync(ctx)
		require.NoError(t, err)
	})
}

func (ServiceSuite) TestWithHostnameModuleScoping(ctx context.Context, t *testctx.T) {
	t.Run("works within module", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		_, err := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/hoster").
			With(daggerExec("init", "--source=.", "--name=hoster", "--sdk=go")).
			WithNewFile("main.go", `package main

import (
	"context"
	"fmt"
)

type Hoster struct{}

func (m *Hoster) Run(ctx context.Context) error {
	srv := dag.Container().
		From("`+busyboxImage+`").
		WithWorkdir("/srv").
		WithNewFile("index.html", "I am the one who hosts.").
		WithExec([]string{"httpd", "-v", "-f"}).
		WithExposedPort(80).
		AsService().
		WithHostname("wwwhatsup")
	
	_, err := srv.Start(ctx)
	if err != nil {
		return err
	}

	resp, err := dag.Container().
		From("`+busyboxImage+`").
		WithExec([]string{"wget", "-O-", "http://wwwhatsup"}).
		Stdout(ctx)
	if err != nil {
		return err
	}
	if resp != "I am the one who hosts." {
		return fmt.Errorf("unexpected response: %q", resp)
	}

	return nil
}
`,
			).
			With(daggerCall("run")).
			Sync(ctx)
		require.NoError(t, err)
	})

	t.Run("is not reachable across modules", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		_, err := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/caller").
			With(daggerExec("init", "--source=.", "--name=caller", "--sdk=go")).
			WithNewFile("main.go", `package main

import (
	"context"
	"fmt"
	"time"
)

type Caller struct{}

func (m *Caller) Run(ctx context.Context) error {
	resp, err := dag.Container().
		From("`+busyboxImage+`").
		WithEnvVariable("NOW", time.Now().String()).
		WithExec([]string{"wget", "-O-", "http://wwwhatsup"}).
		Stdout(ctx)
	if err == nil {
		return fmt.Errorf("should not have been able to reach service")
	}

	srv := dag.Container().
		From("`+busyboxImage+`").
		WithWorkdir("/srv").
		WithNewFile("index.html", "I am within the called module.").
		WithExec([]string{"httpd", "-v", "-f"}).
		WithExposedPort(80).
		AsService().
		WithHostname("wwwhatsup")
	
	_, err = srv.Start(ctx)
	if err != nil {
		return err
	}

	resp, err = dag.Container().
		From("`+busyboxImage+`").
		WithEnvVariable("NOW", time.Now().String()).
		WithExec([]string{"wget", "-O-", "http://wwwhatsup"}).
		Stdout(ctx)
	if err != nil {
		return fmt.Errorf("failed to reach service: %w", err)
	}
	if resp != "I am within the called module." {
		return fmt.Errorf("unexpected response: %q", resp)
	}
	return nil
}
`,
			).
			WithWorkdir("/work/hoster").
			With(daggerExec("init", "--source=.", "--name=hoster", "--sdk=go")).
			With(daggerExec("install", "../caller")).
			WithNewFile("main.go", `package main

import (
	"context"
	"fmt"
	"time"
)

type Hoster struct{}

func (m *Hoster) Run(ctx context.Context) error {
	srv := dag.Container().
		From("`+busyboxImage+`").
		WithWorkdir("/srv").
		WithNewFile("index.html", "I am the one who hosts.").
		WithExec([]string{"httpd", "-v", "-f"}).
		WithExposedPort(80).
		AsService().
		WithHostname("wwwhatsup")
	
	_, err := srv.Start(ctx)
	if err != nil {
		return err
	}

	err = dag.Caller().Run(ctx)
	if err != nil {
		return err
	}

	resp, err := dag.Container().
		From("`+busyboxImage+`").
		WithEnvVariable("NOW", time.Now().String()).
		WithExec([]string{"wget", "-O-", "http://wwwhatsup"}).
		Stdout(ctx)
	if err != nil {
		return fmt.Errorf("failed to reach service: %w", err)
	}
	if resp != "I am the one who hosts." {
		return fmt.Errorf("unexpected response: %q", resp)
	}

	return nil
}
`,
			).
			With(daggerCall("run")).
			Sync(ctx)
		require.NoError(t, err)
	})
}

//go:embed testdata/relay/main.go
var relayMain string

func (ServiceSuite) TestWithHostnameCircular(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	_, err := goGitBase(t, c).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work/relayer").
		With(daggerExec("init", "--source=.", "--name=relayer", "--sdk=go")).
		WithNewFile("relay/main.go", relayMain).
		WithNewFile("main.go", `package main

import (
	_ "embed"

	"dagger/relayer/internal/dagger"
)

type Relayer struct{}

//go:embed relay/main.go
var relayMain string

func (m *Relayer) Service() *dagger.Service {
	return dag.Container().
		From("`+golangImage+`").
		WithWorkdir("/srv").
		WithNewFile("main.go", relayMain).
		WithExec([]string{"go", "run", "main.go"}).
		WithExposedPort(80).
		AsService()
}
`,
		).
		WithWorkdir("/work/caller").
		With(daggerExec("init", "--source=.", "--name=caller", "--sdk=go")).
		With(daggerExec("install", "../relayer")).
		WithNewFile("main.go", `package main

import (
	"context"
	"fmt"
	"time"
	"net/url"

	"golang.org/x/sync/errgroup"

	"dagger/caller/internal/dagger"
)

type Caller struct{}

func (m *Caller) Run(ctx context.Context) error {
	foo := dag.Relayer().Service().WithHostname("foo")
	bar := dag.Relayer().Service().WithHostname("bar")
	baz := dag.Relayer().Service().WithHostname("baz")

	startGroup := new(errgroup.Group)
	for _, srv := range []*dagger.Service{foo, bar, baz} {
		startGroup.Go(func() error {
			_, err := srv.Start(ctx)
			return err
		})
	}
	if err := startGroup.Wait(); err != nil {
		return err
	}

	relayURL := &url.URL{
		Scheme: "http",
		Host:   "foo",
		Path:   "/",
		RawQuery: url.Values{
			"relay": {
				"http://bar",
				"http://baz",
				"http://foo",
			},
			"end": {"hello"},
		}.Encode(),
	}

	resp, err := dag.Container().
		From("`+busyboxImage+`").
		WithEnvVariable("NOW", time.Now().String()).
		WithExec([]string{"cat", "/etc/resolv.conf"}).
		WithExec([]string{"wget", "-O-", relayURL.String()}).
		Stdout(ctx)
	if err != nil {
		return fmt.Errorf("failed to reach service: %w", err)
	}
	if resp != "http://bar: http://baz: http://foo: hello" {
		return fmt.Errorf("unexpected response: %q", resp)
	}

	return nil
}
`,
		).
		With(daggerCall("run")).
		Sync(ctx)
	require.NoError(t, err)
}

func (ServiceSuite) TestPorts(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	srv := c.Container().
		From("python").
		WithExposedPort(8000, dagger.ContainerWithExposedPortOpts{
			Description: "eight thousand",
		}).
		WithExposedPort(9000, dagger.ContainerWithExposedPortOpts{
			Description: "nine thousand",
			Protocol:    dagger.NetworkProtocolUdp,
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
			require.Equal(t, dagger.NetworkProtocolTcp, proto)
		case 1:
			require.Equal(t, 9000, port)
			require.Equal(t, "nine thousand", desc)
			require.Equal(t, dagger.NetworkProtocolUdp, proto)
		}
	}
}

func (ServiceSuite) TestPortsSkipHealthCheck(ctx context.Context, t *testctx.T) {
	t.Run("Healthchecks pass when all ports are skipped", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		srv := c.Container().
			From("python").
			WithExposedPort(6214, dagger.ContainerWithExposedPortOpts{
				ExperimentalSkipHealthcheck: true,
			}).
			WithExposedPort(6215, dagger.ContainerWithExposedPortOpts{
				ExperimentalSkipHealthcheck: true,
			}).
			WithExec([]string{"python", "-m", "http.server"}).
			AsService()

		_, err := srv.Start(ctx)
		require.NoError(t, err)
	})

	t.Run("Healthchecks pass when some ports are skipped", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		srv := c.Container().
			From("python").
			WithExposedPort(6214, dagger.ContainerWithExposedPortOpts{
				ExperimentalSkipHealthcheck: true,
			}).
			WithExposedPort(8000).
			WithExposedPort(6215, dagger.ContainerWithExposedPortOpts{
				ExperimentalSkipHealthcheck: true,
			}).
			WithExec([]string{"python", "-m", "http.server", "8000"}).
			AsService()

		_, err := srv.Start(ctx)
		require.NoError(t, err)
	})
}

func (ContainerSuite) TestPortLifecycle(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	withPorts := c.Container().
		From("python").
		WithExposedPort(8000, dagger.ContainerWithExposedPortOpts{
			Description: "eight thousand tcp",
		}).
		WithExposedPort(8000, dagger.ContainerWithExposedPortOpts{
			Protocol:    dagger.NetworkProtocolUdp,
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
		} `json:"loadContainerFromID"`
	}{}

	getPorts := `query Test($id: ContainerID!) {
		loadContainerFromID(id: $id) {
			exposedPorts {
				port
				protocol
				description
			}
		}
	}`

	err = testutil.Query(t, getPorts, &res, &testutil.QueryOptions{
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
	err = testutil.Query(t, getPorts, &res, &testutil.QueryOptions{
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
		Protocol: dagger.NetworkProtocolUdp,
	})
	cid, err = withoutUDP.ID(ctx)
	require.NoError(t, err)
	err = testutil.Query(t, getPorts, &res, &testutil.QueryOptions{
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

func (ContainerSuite) TestPortOCIConfig(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	withPorts := c.Container().
		From("python").
		WithExposedPort(8000, dagger.ContainerWithExposedPortOpts{
			Description: "eight thousand tcp",
		}).
		WithExposedPort(8000, dagger.ContainerWithExposedPortOpts{
			Protocol:    dagger.NetworkProtocolUdp,
			Description: "eight thousand udp",
		}).
		WithExposedPort(5432).
		WithExposedPort(5432, dagger.ContainerWithExposedPortOpts{
			Protocol: dagger.NetworkProtocolUdp,
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
			Protocol: dagger.NetworkProtocolUdp,
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

func (ContainerSuite) TestExecServicesSimple(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

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

func (ContainerSuite) TestExecServicesError(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

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

func (ContainerSuite) TestServiceNoExec(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

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

func (ContainerSuite) TestExecUDPServices(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	srv := c.Container().
		From(golangImage).
		WithMountedFile("/src/main.go",
			c.Directory().WithNewFile("main.go", udpSrc).File("main.go")).
		WithExposedPort(4321, dagger.ContainerWithExposedPortOpts{
			Protocol: dagger.NetworkProtocolUdp,
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

func (ContainerSuite) TestExecServiceAlias(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

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

func (ContainerSuite) TestExecServicesDeduping(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

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

func (ContainerSuite) TestExecServicesChained(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

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

func (ContainerSuite) TestExecServicesNestedExec(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

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

func (ContainerSuite) TestExecServicesNestedHTTP(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

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

func (ContainerSuite) TestExecServicesNestedGit(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

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

func (ContainerSuite) TestExportServices(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	content := identity.NewID()
	srv, httpURL := httpService(ctx, t, c, content)

	client := c.Container().
		From(alpineImage).
		WithServiceBinding("www", srv).
		WithExec([]string{"wget", httpURL})

	filePath := filepath.Join(t.TempDir(), "image.tar")
	actual, err := client.Export(ctx, filePath)
	require.NoError(t, err)
	require.Equal(t, filePath, actual)
}

func (ContainerSuite) TestMultiPlatformExportServices(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

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
	actual, err := c.Container().Export(ctx, dest, dagger.ContainerExportOpts{
		PlatformVariants: variants,
	})
	require.NoError(t, err)
	require.Equal(t, dest, actual)
}

func (ServiceSuite) TestContainerPublish(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

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

func (ContainerSuite) TestRootFSServices(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

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

func (ContainerSuite) TestWithRootFSServices(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

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

func (ContainerSuite) TestDirectoryServices(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	content := identity.NewID()
	srv, url := httpService(ctx, t, c, content)

	wget := c.Container().
		From(alpineImage).
		WithServiceBinding("www", srv).
		WithWorkdir("/sub/out").
		WithExec([]string{"wget", url})

	t.Run("runs services for Container.Directory.Entries", func(ctx context.Context, t *testctx.T) {
		entries, err := wget.Directory(".").Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"index.html"}, entries)
	})

	t.Run("runs services for Container.Directory.Directory.Entries", func(ctx context.Context, t *testctx.T) {
		entries, err := wget.Directory("/sub").Directory("out").Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"index.html"}, entries)
	})

	t.Run("runs services for Container.Directory.File.Contents", func(ctx context.Context, t *testctx.T) {
		fileContent, err := wget.Directory(".").File("index.html").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, content, fileContent)
	})

	t.Run("runs services for Container.Directory.Export", func(ctx context.Context, t *testctx.T) {
		dest := t.TempDir()

		actual, err := wget.Directory(".").Export(ctx, dest)
		require.NoError(t, err)
		require.Equal(t, dest, actual)

		fileContent, err := os.ReadFile(filepath.Join(dest, "index.html"))
		require.NoError(t, err)
		require.Equal(t, content, string(fileContent))
	})
}

func (ContainerSuite) TestFileServices(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

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

func (ContainerSuite) TestWithServiceFileDirectory(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	response := identity.NewID()
	srv, httpURL := httpService(ctx, t, c, response)
	httpFile := c.HTTP(httpURL, dagger.HTTPOpts{
		ExperimentalServiceHost: srv,
	})

	gitDaemon, repoURL := gitService(ctx, t, c, c.Directory().WithNewFile("README.md", response))
	gitDir := c.Git(repoURL, dagger.GitOpts{ExperimentalServiceHost: gitDaemon}).
		Branch("main").
		Tree()

	t.Run("mounting", func(ctx context.Context, t *testctx.T) {
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

	t.Run("copying", func(ctx context.Context, t *testctx.T) {
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

func (ServiceSuite) TestDirectoryEntries(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	content := identity.NewID()

	gitDaemon, repoURL := gitService(ctx, t, c, c.Directory().WithNewFile("README.md", content))

	entries, err := c.Git(repoURL, dagger.GitOpts{ExperimentalServiceHost: gitDaemon}).
		Branch("main").
		Tree().
		Entries(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{".git", "README.md"}, entries)
}

func (ServiceSuite) TestDirectorySync(ctx context.Context, t *testctx.T) {
	t.Run("triggers error", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		content := identity.NewID()
		gitDaemon, repoURL := gitService(ctx, t, c, c.Directory().WithNewFile("README.md", content))
		_, err := c.Git(repoURL, dagger.GitOpts{ExperimentalServiceHost: gitDaemon}).
			Branch("foobar").
			Tree().
			Sync(ctx)
		require.Error(t, err)
	})

	t.Run("with chaining", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		content := identity.NewID()
		gitDaemon, repoURL := gitService(ctx, t, c, c.Directory().WithNewFile("README.md", content))
		repo, err := c.Git(repoURL, dagger.GitOpts{ExperimentalServiceHost: gitDaemon}).
			Branch("main").
			Tree().
			Sync(ctx)
		require.NoError(t, err)

		entries, err := repo.Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{".git", "README.md"}, entries)
	})
}

func (ServiceSuite) TestDirectoryTimestamp(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

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

func (ServiceSuite) TestWithDirectoryFileServices(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	content := identity.NewID()

	gitSrv, repoURL := gitService(ctx, t, c, c.Directory().WithNewFile("README.md", content))

	httpSrv, httpURL := httpService(ctx, t, c, content)

	useBoth := c.Directory().
		WithDirectory("/repo", c.Git(repoURL, dagger.GitOpts{ExperimentalServiceHost: gitSrv}).Branch("main").Tree()).
		WithFile("/index.html", c.HTTP(httpURL, dagger.HTTPOpts{ExperimentalServiceHost: httpSrv}))

	entries, err := useBoth.Directory("/repo").Entries(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{".git", "README.md"}, entries)

	fileContent, err := useBoth.File("/index.html").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, content, fileContent)
}

func (ServiceSuite) TestDirectoryExport(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	content := identity.NewID()

	gitDaemon, repoURL := gitService(ctx, t, c, c.Directory().WithNewFile("README.md", content))

	dest := t.TempDir()

	actual, err := c.Git(repoURL, dagger.GitOpts{ExperimentalServiceHost: gitDaemon}).
		Branch("main").
		Tree().
		Export(ctx, dest)
	require.NoError(t, err)
	require.Equal(t, dest, actual)

	exportedContent, err := os.ReadFile(filepath.Join(dest, "README.md"))
	require.NoError(t, err)
	require.Equal(t, content, string(exportedContent))
}

func (FileSuite) TestServiceContents(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

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

func (FileSuite) TestServiceSync(ctx context.Context, t *testctx.T) {
	t.Run("triggers error", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		content := identity.NewID()
		httpSrv, httpURL := httpService(ctx, t, c, content)

		_, err := c.HTTP(httpURL+"/foobar", dagger.HTTPOpts{
			ExperimentalServiceHost: httpSrv,
		}).Sync(ctx)

		require.Error(t, err)
		require.Contains(t, err.Error(), "status 404")
	})

	t.Run("with chaining", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

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

func (FileSuite) TestServiceExport(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	content := identity.NewID()

	gitDaemon, repoURL := gitService(ctx, t, c, c.Directory().WithNewFile("README.md", content))

	dest := t.TempDir()
	filePath := filepath.Join(dest, "README.md")

	actual, err := c.Git(repoURL, dagger.GitOpts{ExperimentalServiceHost: gitDaemon}).
		Branch("main").
		Tree().
		File("README.md").
		Export(ctx, filePath)
	require.NoError(t, err)
	require.Equal(t, filePath, actual)

	exportedContent, err := os.ReadFile(filePath)
	require.NoError(t, err)
	require.Equal(t, content, string(exportedContent))
}

func (FileSuite) TestServiceTimestamp(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

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
func (ServiceSuite) TestStartStop(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

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

// TestServiceStartStopKill tests that we send SIGTERM by default, instead of SIGKILL.
// Additionally, we check that we can attempt to SIGKILL a process that is not
// responding to SIGTERM.
func (ServiceSuite) TestStartStopKill(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	httpSrv, httpURL := signalService(ctx, t, c)

	fetch := func() (string, error) {
		return c.Container().
			From(alpineImage).
			WithEnvVariable("BUST", identity.NewID()).
			WithExec([]string{"wget", "-O-", httpURL + "/signals.txt"}).
			Stdout(ctx)
	}

	out, err := fetch()
	require.Error(t, err)
	require.Empty(t, out)

	_, err = httpSrv.Start(ctx)
	require.NoError(t, err)

	out, err = fetch()
	require.NoError(t, err)
	require.Empty(t, out)

	eg := errgroup.Group{}
	eg.Go(func() error {
		// attempt to SIGTERM the service
		// this won't stop the service though, since the process ignores this one - we'll
		// end up blocking here until the SIGKILL applies
		_, err := httpSrv.Stop(ctx)
		return err
	})

	// ensures that the subprocess gets SIGTERM
	require.Eventually(t, func() bool {
		out, err = fetch()
		require.NoError(t, err)
		return out == "Terminated\n"
	}, time.Minute, time.Second)

	eg.Go(func() error {
		// attempt to SIGKILL the serive (this will work)
		_, err := httpSrv.Stop(ctx, dagger.ServiceStopOpts{Kill: true})
		return err
	})

	// ensure everyone eventually stops
	eg.Wait()

	out, err = fetch()
	require.Error(t, err)
	require.Empty(t, out)
}

// TestServiceNoCrossTalk shows that services spawned in one client cannot be
// reached by another client.
func (ServiceSuite) TestNoCrossTalk(ctx context.Context, t *testctx.T) {
	c1 := connect(ctx, t)
	defer c1.Close()

	c2 := connect(ctx, t)
	defer c2.Close()

	content1 := identity.NewID()

	httpSrv1, httpURL1 := httpService(ctx, t, c1, content1)

	fetch := func(c *dagger.Client) (string, error) {
		return c.Container().
			From(alpineImage).
			WithEnvVariable("BUST", identity.NewID()).
			WithExec([]string{"wget", "-O-", httpURL1}).
			Stdout(ctx)
	}

	_, err := httpSrv1.Start(ctx)
	require.NoError(t, err)

	out, err := fetch(c1)
	require.NoError(t, err)
	require.Equal(t, out, content1)

	out, err = fetch(c2)
	require.Error(t, err)
	require.Empty(t, out)
}

func (ServiceSuite) TestHostToContainer(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	content := identity.NewID()

	t.Run("no options means bind to random", func(ctx context.Context, t *testctx.T) {
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

	t.Run("multiple ports", func(ctx context.Context, t *testctx.T) {
		srv := c.Container().
			From("python").
			WithMountedDirectory("/srv/www1",
				c.Directory().WithNewFile("index.html", content+"-1")).
			WithMountedDirectory("/srv/www2",
				c.Directory().WithNewFile("index.html", content+"-2")).
			WithExec([]string{
				"sh", "-c",
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

	t.Run("native mapping", func(ctx context.Context, t *testctx.T) {
		srv := c.Container().
			From("python").
			WithMountedDirectory("/srv/www1",
				c.Directory().WithNewFile("index.html", content+"-1")).
			WithMountedDirectory("/srv/www2",
				c.Directory().WithNewFile("index.html", content+"-2")).
			WithExec([]string{
				"sh", "-c",
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

	t.Run("native mapping + extra ports", func(ctx context.Context, t *testctx.T) {
		srv := c.Container().
			From("python").
			WithMountedDirectory("/srv/www1",
				c.Directory().WithNewFile("index.html", content+"-1")).
			WithMountedDirectory("/srv/www2",
				c.Directory().WithNewFile("index.html", content+"-2")).
			WithExec([]string{
				"sh", "-c",
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

	t.Run("no ports to forward", func(ctx context.Context, t *testctx.T) {
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

func (ServiceSuite) TestContainerToHost(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

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

	t.Run("simple", func(ctx context.Context, t *testctx.T) {
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

	t.Run("using hostname", func(ctx context.Context, t *testctx.T) {
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

	t.Run("using endpoint", func(ctx context.Context, t *testctx.T) {
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

	t.Run("multiple ports", func(ctx context.Context, t *testctx.T) {
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

	t.Run("no ports given", func(ctx context.Context, t *testctx.T) {
		_, err := c.Host().Service(nil).ID(ctx)
		require.Error(t, err)
	})
}

func (ServiceSuite) TestSearchDomainAlwaysSet(ctx context.Context, t *testctx.T) {
	// verify that even if the engine doesn't have any search domains to propagate to execs, we still
	// set search domains in those execs

	c, err := dagger.Connect(ctx, dagger.WithLogOutput(testutil.NewTWriter(t.T)))
	require.NoError(t, err)
	t.Cleanup(func() { c.Close() })

	resolvContents, err := c.Container().From(alpineImage).
		WithExec([]string{"cat", "/etc/resolv.conf"}).
		Stdout(ctx)
	require.NoError(t, err)

	var newResolvContents string
	for _, line := range strings.Split(resolvContents, "\n") {
		if strings.HasPrefix(line, "search") {
			continue
		}
		newResolvContents += line + "\n"
	}

	newResolvConf := c.Directory().
		WithNewFile("resolv.conf", newResolvContents, dagger.DirectoryWithNewFileOpts{Permissions: 0644}).
		File("resolv.conf")

	devEngine := devEngineContainer(c, func(ctr *dagger.Container) *dagger.Container {
		return ctr.WithMountedFile("/etc/resolv.conf", newResolvConf)
	}).AsService()
	t.Cleanup(func() {
		devEngine.Stop(ctx, dagger.ServiceStopOpts{Kill: true})
	})

	hostSvc, err := c.Host().Tunnel(devEngine, dagger.HostTunnelOpts{
		Ports: []dagger.PortForward{{
			Backend:  1234,
			Frontend: 32132,
		}},
	}).Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() {
		hostSvc.Stop(ctx, dagger.ServiceStopOpts{Kill: true})
	})

	c2, err := dagger.Connect(ctx,
		dagger.WithRunnerHost("tcp://127.0.0.1:32132"),
		dagger.WithLogOutput(testutil.NewTWriter(t.T)))
	require.NoError(t, err)
	t.Cleanup(func() { c2.Close() })

	resolvContents2, err := c2.Container().From(alpineImage).
		WithExec([]string{"cat", "/etc/resolv.conf"}).
		Stdout(ctx)
	require.NoError(t, err)
	var found bool
	for _, line := range strings.Split(resolvContents2, "\n") {
		if strings.HasPrefix(line, "search") {
			found = true
			require.Regexp(t, `search [a-z0-9]+\.dagger\.local`, line)
		}
	}
	require.True(t, found)
}

func httpService(ctx context.Context, t *testctx.T, c *dagger.Client, content string) (*dagger.Service, string) {
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

func gitService(ctx context.Context, t *testctx.T, c *dagger.Client, content *dagger.Directory) (*dagger.Service, string) {
	t.Helper()
	return gitServiceWithBranch(ctx, t, c, content, "main")
}

func gitServiceWithBranch(ctx context.Context, t *testctx.T, c *dagger.Client, content *dagger.Directory, branchName string) (*dagger.Service, string) {
	t.Helper()

	const gitPort = 9418
	gitDaemon := c.Container().
		From(alpineImage).
		WithExec([]string{"apk", "add", "git", "git-daemon"}).
		WithDirectory("/root/srv", makeGitDir(c, content, branchName)).
		WithExposedPort(gitPort).
		WithExec([]string{"sh", "-c", "git daemon --verbose --export-all --base-path=/root/srv"}).
		AsService()

	gitHost, err := gitDaemon.Hostname(ctx)
	require.NoError(t, err)

	repoURL := fmt.Sprintf("git://%s/repo.git", gitHost)

	return gitDaemon, repoURL
}

func gitServiceHTTPWithBranch(ctx context.Context, t testing.TB, c *dagger.Client, content *dagger.Directory, branchName string, token *dagger.Secret) (*dagger.Service, string) {
	t.Helper()

	var tokenPlaintext string
	if token != nil {
		tokenPlaintext, _ = token.Plaintext(ctx)
	}

	tmpl, err := template.New("").Parse(`
server {
	listen       80;
	server_name  localhost;

	location / {
		root   /usr/share/nginx/html;
		index  index.html index.htm;
	}

	{{ if .token }}
	auth_basic            "Restricted";
	auth_basic_user_file  /usr/share/nginx/htpasswd;
	{{ end }}
}
`)
	if err != nil {
		panic(err)
	}

	var config bytes.Buffer
	err = tmpl.Execute(&config, map[string]any{
		"token": tokenPlaintext,
	})
	if err != nil {
		panic(err)
	}

	gitDaemon := c.Container().
		From("nginx").
		WithNewFile("/etc/nginx/conf.d/default.conf", config.String()).
		WithMountedDirectory("/usr/share/nginx/html", makeGitDir(c, content, branchName)).
		WithMountedSecret("/usr/share/nginx/htpasswd", c.SetSecret("htpasswd", "x-access-token:{PLAIN}"+tokenPlaintext), dagger.ContainerWithMountedSecretOpts{
			Owner: "nginx",
		}).
		WithExposedPort(80).
		AsService()

	gitHost, err := gitDaemon.Hostname(ctx)
	require.NoError(t, err)

	repoURL := fmt.Sprintf("http://%s/repo.git", gitHost)

	return gitDaemon, repoURL
}

func makeGitDir(c *dagger.Client, content *dagger.Directory, branchName string) *dagger.Directory {
	return c.Container().
		From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		WithDirectory("/root/repo", content).
		WithNewFile("/root/create.sh", fmt.Sprintf(`#!/bin/sh

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
	cd repo.git
		git update-server-info
	cd ..
cd ..
`, branchName),
		).
		WithExec([]string{"sh", "/root/create.sh"}).
		Directory("/root/srv")
}

// signalService is a little helper service that writes assorted signals that
// it receives to /signals.txt.
func signalService(ctx context.Context, t *testctx.T, c *dagger.Client) (*dagger.Service, string) {
	t.Helper()

	srv := c.Container().
		From("python").
		WithNewFile("/signals.py", `
import http.server
import signal
import socketserver
import sys
import time

def print_signal(signum, frame):
        with open("./signals.txt", "a+") as f:
                print(signal.strsignal(signum), file=f)
for sig in [signal.SIGINT, signal.SIGTERM]:
	signal.signal(sig, print_signal)

with socketserver.TCPServer(("", 8000), http.server.SimpleHTTPRequestHandler) as httpd:
    print("serving at port 8000")
    httpd.serve_forever()
`).
		WithWorkdir("/srv/www").
		WithNewFile("signals.txt", "").
		WithExposedPort(8000).
		WithExec([]string{"python", "/signals.py"}).
		AsService()

	httpURL, err := srv.Endpoint(ctx, dagger.ServiceEndpointOpts{
		Scheme: "http",
	})
	require.NoError(t, err)

	return srv, httpURL
}

var (
	nestingLimitOnce       = &sync.Once{}
	calculatedNestingLimit int
)

// search domains cap out at 256 chars, and these tests have to run in
// environments that may have some pre-configured (k8s) or may already be
// nested (dagger-in-dagger), so we need to calculate how deeply we can nest.
func calculateNestingLimit(ctx context.Context, c *dagger.Client, t *testctx.T) int {
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
		domainLen := len(network.SessionDomain("dummy")) + 1 // account for space

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
