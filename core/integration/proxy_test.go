package core

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/goproxy/goproxy"
	"github.com/stretchr/testify/require"

	"dagger.io/dagger"
)

type proxyTest struct {
	name string
	run  func(*testing.T, *dagger.Client, proxyTestFixtures)
}

type proxyTestFixtures struct {
	caCert *dagger.File

	httpProxyURL  url.URL
	httpsProxyURL url.URL

	httpServerURL  url.URL
	httpsServerURL url.URL

	noproxyHTTPServerURL url.URL
}

func customProxyTests(
	ctx context.Context,
	t *testing.T,
	c *dagger.Client,
	netID uint8,
	useAuth bool,
	tests ...proxyTest,
) {
	t.Helper()

	const httpServerAlias = "whatup"
	const noproxyHTTPServerAlias = "whatupnoproxy"
	const squidAlias = "squid"

	executeTestEnvName := fmt.Sprintf("DAGGER_TEST_%s", strings.ToUpper(t.Name()))

	certGen := newGeneratedCerts(c, squidAlias)

	httpServerCert, httpServerKey := certGen.newServerCerts(httpServerAlias)
	httpServer := nginxWithCerts(c, nginxWithCertsOpts{
		serverCert: httpServerCert,
		serverKey:  httpServerKey,
		dhParam:    certGen.dhParam,
		netID:      netID,
		dnsName:    httpServerAlias,
		msg:        "whatup",
	})
	httpServerURL := url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort(httpServerAlias, "80"),
	}
	httpsServerURL := url.URL{
		Scheme: "https",
		Host:   net.JoinHostPort(httpServerAlias, "443"),
	}

	noproxyHTTPServerCert, noproxyHTTPServerKey := certGen.newServerCerts(noproxyHTTPServerAlias)
	noproxyHTTPServer := nginxWithCerts(c, nginxWithCertsOpts{
		serverCert: noproxyHTTPServerCert,
		serverKey:  noproxyHTTPServerKey,
		dhParam:    certGen.dhParam,
		netID:      netID,
		dnsName:    noproxyHTTPServerAlias,
		msg:        "whatup",
	})
	noproxyHTTPServerURL := url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort(noproxyHTTPServerAlias, "80"),
	}

	squidConf := `
acl localnet src 0.0.0.1-0.255.255.255  # RFC 1122 "this" network (LAN)
acl localnet src 10.0.0.0/8             # RFC 1918 local private network (LAN)
acl localnet src 100.64.0.0/10          # RFC 6598 shared address space (CGN)
acl localnet src 169.254.0.0/16         # RFC 3927 link-local (directly plugged) machines
acl localnet src 172.16.0.0/12          # RFC 1918 local private network (LAN)
acl localnet src 192.168.0.0/16         # RFC 1918 local private network (LAN)
acl localnet src fc00::/7               # RFC 4193 local private network range
acl localnet src fe80::/10              # RFC 4291 link-local (directly plugged) machines

acl SSL_ports port 443
acl Safe_ports port 80          # http
acl Safe_ports port 443         # https

sslpassword_program /usr/local/bin/printpass
auth_param basic program /usr/lib/squid/basic_getpwnam_auth
auth_param basic children 1

coredump_dir /var/cache/squid
# access_log stdio:/var/log/squidaccess/access.log

#
# Add any of your own refresh_pattern entries above these.
#
refresh_pattern ^ftp:           1440    20%     10080
refresh_pattern ^gopher:        1440    0%      1440
refresh_pattern -i (/cgi-bin/|\?) 0     0%      0
refresh_pattern .               0       20%     4320

http_port 3128
ssl_bump bump all
https_port 3129 generate-host-certificates=on tls-cert=/etc/squid/server.pem tls-key=/etc/squid/serverkey.pem tls-dh=/etc/squid/dhparam.pem

http_access deny !Safe_ports
http_access deny CONNECT !SSL_ports
http_access allow localhost manager
http_access deny manager
http_access allow localhost
`

	squidCert, squidKey := certGen.newServerCerts(squidAlias)
	squid := c.Container().From(alpineImage).
		WithExec([]string{"apk", "add", "squid", "ca-certificates"}).
		WithMountedFile("/usr/local/bin/printpass", certGen.printPasswordScript).
		WithMountedFile("/etc/ssl/certs/myCA.pem", certGen.caRootCert).
		WithExec([]string{"update-ca-certificates"}).
		WithMountedFile("/etc/squid/server.pem", squidCert).
		WithMountedFile("/etc/squid/serverkey.pem", squidKey).
		WithMountedFile("/etc/squid/dhparam.pem", certGen.dhParam).
		WithExec([]string{"chmod", "u+s", "/usr/lib/squid/basic_getpwnam_auth"}).
		WithExposedPort(3128).
		WithExposedPort(3129)

	squidHTTPURL := url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort(squidAlias, "3128"),
	}
	squidHTTPSURL := url.URL{
		Scheme: "https",
		Host:   net.JoinHostPort(squidAlias, "3129"),
	}

	if useAuth {
		const username = "cooluser"
		const password = "hunter2"
		squid = squid.WithExec([]string{"adduser", username}, dagger.ContainerWithExecOpts{
			Stdin: password + "\n" + password + "\n",
		})

		userPass := url.UserPassword(username, password)
		squidHTTPURL.User = userPass
		squidHTTPSURL.User = userPass

		squidConf += "acl auth proxy_auth REQUIRED\n"
		squidConf += "http_access allow localnet auth\n"
	} else {
		squidConf += "http_access allow localnet\n"
	}

	squidConf += "http_access deny all\n"

	squid = squid.
		WithNewFile("/etc/squid/squid.conf", dagger.ContainerWithNewFileOpts{Contents: squidConf}).
		WithServiceBinding(httpServerAlias, httpServer.AsService()).
		WithServiceBinding(noproxyHTTPServerAlias, noproxyHTTPServer.AsService()).
		WithExec([]string{"squid", "--foreground"})

	if os.Getenv(executeTestEnvName) == "" {
		devEngine := devEngineContainer(c, netID, func(ctr *dagger.Container) *dagger.Container {
			return ctr.
				// go right to /etc/ssl/certs to avoid testing the custom CA cert support (covered elsewhere)
				WithMountedFile("/etc/ssl/certs/myCA.pem", certGen.caRootCert).
				WithExec([]string{"update-ca-certificates"}, dagger.ContainerWithExecOpts{SkipEntrypoint: true}).
				WithEnvVariable("HTTP_PROXY", squidHTTPURL.String()).
				WithEnvVariable("HTTPS_PROXY", squidHTTPSURL.String()).
				WithEnvVariable("NO_PROXY", noproxyHTTPServerAlias).
				WithServiceBinding(httpServerAlias, httpServer.AsService()).
				WithServiceBinding(noproxyHTTPServerAlias, noproxyHTTPServer.AsService()).
				WithServiceBinding(squidAlias, squid.AsService())
		})

		thisRepoPath, err := filepath.Abs("../..")
		require.NoError(t, err)
		thisRepo := c.Host().Directory(thisRepoPath)

		_, err = c.Container().From(golangImage).
			With(goCache(c)).
			WithMountedDirectory("/src", thisRepo).
			WithWorkdir("/src").
			WithMountedFile("/ca.pem", certGen.caRootCert).
			WithServiceBinding("engine", devEngine.AsService()).
			WithMountedFile("/bin/dagger", daggerCliFile(t, c)).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", "/bin/dagger").
			WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "tcp://engine:1234").
			WithEnvVariable(executeTestEnvName, "ya").
			WithExec([]string{"go", "test",
				"-v",
				"-timeout", "20m",
				"-count", "1",
				"-run", fmt.Sprintf("^%s$", t.Name()),
				"./core/integration",
			}).Sync(ctx)
		require.NoError(t, err)
		return
	}

	// we're in the container depending on the custom engine, run the actual tests

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			test.run(t, c, proxyTestFixtures{
				caCert: c.Host().File("/ca.pem"),

				httpProxyURL:  squidHTTPURL,
				httpsProxyURL: squidHTTPSURL,

				httpServerURL:  httpServerURL,
				httpsServerURL: httpsServerURL,

				noproxyHTTPServerURL: noproxyHTTPServerURL,
			})
		})
	}
}

func TestContainerSystemProxies(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	t.Run("basic", func(t *testing.T) {
		t.Parallel()
		customProxyTests(ctx, t, c, 101, false,
			proxyTest{"http", func(t *testing.T, c *dagger.Client, f proxyTestFixtures) {
				out, err := c.Container().From(alpineImage).
					WithExec([]string{"apk", "add", "curl"}).
					WithExec([]string{"curl", "-v", f.httpServerURL.String()}).
					Stderr(ctx)
				require.NoError(t, err)
				require.Regexp(t, `.*< HTTP/1\.1 200 OK.*`, out)
				require.Regexp(t, `.*< Via: .* \(squid/5.9\).*`, out)
			}},

			proxyTest{"https", func(t *testing.T, c *dagger.Client, f proxyTestFixtures) {
				out, err := c.Container().From(alpineImage).
					WithExec([]string{"apk", "add", "curl", "ca-certificates"}).
					WithMountedFile("/etc/ssl/certs/myCA.pem", f.caCert).
					WithExec([]string{"update-ca-certificates"}).
					WithExec([]string{"curl", "-v", f.httpsServerURL.String()}).
					Stderr(ctx)
				require.NoError(t, err)
				require.Regexp(t, `.*< HTTP/1\.1 200 Connection established.*`, out)
				require.Regexp(t, fmt.Sprintf(`.*Establish HTTP proxy tunnel to %s.*`, f.httpsServerURL.Host), out)
			}},

			proxyTest{"noproxy http", func(t *testing.T, c *dagger.Client, f proxyTestFixtures) {
				out, err := c.Container().From(alpineImage).
					WithExec([]string{"apk", "add", "curl"}).
					WithExec([]string{"curl", "-v", f.noproxyHTTPServerURL.String()}).
					Stderr(ctx)
				require.NoError(t, err)
				require.Regexp(t, `.*< HTTP/1\.1 200 OK.*`, out)
				require.NotRegexp(t, `.*< Via: .*`, out)
			}},
		)
	})

	t.Run("auth", func(t *testing.T) {
		t.Parallel()
		customProxyTests(ctx, t, c, 102, true,
			proxyTest{"http", func(t *testing.T, c *dagger.Client, f proxyTestFixtures) {
				base := c.Container().From(alpineImage).
					WithExec([]string{"apk", "add", "curl"})

				out, err := base.
					WithExec([]string{"curl", "-v", f.httpServerURL.String()}).
					Stderr(ctx)
				require.NoError(t, err)
				require.Regexp(t, `.*< HTTP/1\.1 200 OK.*`, out)
				require.Regexp(t, `.*< Via: .* \(squid/5.9\).*`, out)

				// verify we fail if we override the proxy with a bad password
				u := f.httpProxyURL
				u.User = url.UserPassword("cooluser", "badpass")
				out, err = base.
					WithEnvVariable("HTTP_PROXY", u.String()).
					WithExec([]string{"curl", "-v", f.httpServerURL.String()}).
					Stderr(ctx)
				// curl will exit 0 if it gets a 407 on plain HTTP, so don't expect an error
				require.NoError(t, err)
				require.Contains(t, out, "< HTTP/1.1 407 Proxy Authentication Required")
			}},

			proxyTest{"https", func(t *testing.T, c *dagger.Client, f proxyTestFixtures) {
				base := c.Container().From(alpineImage).
					WithExec([]string{"apk", "add", "curl", "ca-certificates"}).
					WithMountedFile("/etc/ssl/certs/myCA.pem", f.caCert).
					WithExec([]string{"update-ca-certificates"})

				out, err := base.
					WithExec([]string{"curl", "-v", f.httpsServerURL.String()}).
					Stderr(ctx)
				require.NoError(t, err)
				require.Regexp(t, `.*< HTTP/1\.1 200 Connection established.*`, out)
				require.Regexp(t, fmt.Sprintf(`.*Establish HTTP proxy tunnel to %s.*`, f.httpsServerURL.Host), out)

				// verify we fail if we override the proxy with a bad password
				u := f.httpsProxyURL
				u.User = url.UserPassword("cooluser", "badpass")
				_, err = base.
					WithEnvVariable("HTTPS_PROXY", u.String()).
					WithExec([]string{"curl", "-v", f.httpsServerURL.String()}).
					Stderr(ctx)
				// curl WON'T exit 0 if it gets a 407 when using TLS, so DO expect an error
				require.ErrorContains(t, err, "< HTTP/1.1 407 Proxy Authentication Required")
			}},
		)
	})
}

func TestContainerSystemGoProxy(t *testing.T) {
	t.Parallel()
	c, ctx := connect(t)

	// just a subset of modules we expect to be downloaded since trying to go one to one would
	// be too fragile whenever the SDK changes
	expectedGoModDownloads := []string{
		"github.com/99designs/gqlgen",
		"github.com/Khan/genqlient",
		"go.opentelemetry.io/otel/exporters/otlp/otlptrace",
		"golang.org/x/sync",
	}

	executeTestEnvName := fmt.Sprintf("DAGGER_TEST_%s", strings.ToUpper(t.Name()))
	if os.Getenv(executeTestEnvName) == "" {
		const netID = 103
		const goProxyAlias = "goproxy"
		const goProxyPort = 8080
		goProxySetting := fmt.Sprintf("http://%s:%d", goProxyAlias, goProxyPort)

		fetcher := &goProxyFetcher{dlPaths: make(map[string]struct{})}
		proxy := &goproxy.Goproxy{Fetcher: fetcher}

		l, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		t.Cleanup(func() {
			l.Close()
		})
		port := l.Addr().(*net.TCPAddr).Port

		goProxyCtx, cancelGoProxy := context.WithCancel(ctx)
		t.Cleanup(cancelGoProxy)
		srv := http.Server{
			Handler:           proxy,
			ReadHeaderTimeout: 30 * time.Second,
			BaseContext: func(net.Listener) context.Context {
				return goProxyCtx
			},
		}
		t.Cleanup(func() {
			srv.Shutdown(context.Background())
		})

		goProxyDone := make(chan error, 1)
		go func() {
			goProxyDone <- srv.Serve(l)
		}()

		goProxySvc := c.Host().Service([]dagger.PortForward{{
			Backend:  port,
			Frontend: goProxyPort,
		}})

		devEngine := devEngineContainer(c, netID, func(ctr *dagger.Container) *dagger.Container {
			return ctr.
				WithServiceBinding(goProxyAlias, goProxySvc).
				WithEnvVariable("_DAGGER_ENGINE_SYSTEMENV_GOPROXY", goProxySetting)
		})

		thisRepoPath, err := filepath.Abs("../..")
		require.NoError(t, err)
		thisRepo := c.Host().Directory(thisRepoPath)

		_, err = c.Container().From(golangImage).
			With(goCache(c)).
			WithMountedDirectory("/src", thisRepo).
			WithWorkdir("/src").
			WithServiceBinding("engine", devEngine.AsService()).
			WithMountedFile("/bin/dagger", daggerCliFile(t, c)).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", "/bin/dagger").
			WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "tcp://engine:1234").
			WithEnvVariable(executeTestEnvName, "ya").
			WithExec([]string{"go", "test",
				"-v",
				"-timeout", "20m",
				"-count", "1",
				"-run", fmt.Sprintf("^%s$", t.Name()),
				"./core/integration",
			}).Sync(ctx)
		require.NoError(t, err)

		select {
		case err := <-goProxyDone:
			require.NoError(t, err)
		default:
		}

		fetcher.mu.Lock()
		defer fetcher.mu.Unlock()
		require.NotEmpty(t, fetcher.dlPaths)
		for _, expectedPath := range expectedGoModDownloads {
			require.Contains(t, fetcher.dlPaths, expectedPath)
		}

		return
	}

	// we're in the container depending on the custom engine, run the actual tests
	ctr := goGitBase(t, c).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

	out, err := ctr.
		With(daggerCallAt(testGitModuleRef("top-level"), "fn")).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "hi from top level hi from dep hi from dep2", strings.TrimSpace(out))
}

type goProxyFetcher struct {
	goproxy.GoFetcher
	mu      sync.Mutex
	dlPaths map[string]struct{}
}

func (f *goProxyFetcher) Download(ctx context.Context, path, version string) (info, mod, zip io.ReadSeekCloser, err error) {
	f.mu.Lock()
	f.dlPaths[path] = struct{}{}
	f.mu.Unlock()
	return f.GoFetcher.Download(ctx, path, version)
}
