package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"
)

type caCertsTest struct {
	name string
	run  func(*testing.T, *dagger.Client, caCertsTestFixtures)
}

type caCertsTestFixtures struct {
	serverCtr      *dagger.Container
	caCertContents string
}

func customCACertTests(
	ctx context.Context,
	t *testing.T,
	c *dagger.Client,
	netID int,
	tests ...caCertsTest,
) {
	t.Helper()

	executeTestEnvName := fmt.Sprintf("DAGGER_TEST_%s", strings.ToUpper(t.Name()))

	if os.Getenv(executeTestEnvName) == "" {
		// TODO: this would be a good module
		const password = "hunter4"
		genCtr := c.Container().From(alpineImage).
			WithExec([]string{"apk", "add", "openssl"}).
			WithExec([]string{"openssl", "dhparam",
				"-out", "/dhparam.pem",
				"2048",
			}).
			WithExec([]string{"openssl", "genrsa",
				"-des3",
				"-out", "/ca.key",
				"-passout", "pass:" + password,
				"2048",
			}).
			WithExec([]string{"openssl", "req",
				"-x509",
				"-new",
				"-nodes",
				"-key", "/ca.key",
				"-sha256",
				"-days", "99999",
				"-out", "/ca.pem",
				"-passin", "pass:" + password,
				"-subj", "/C=US/ST=CA/L=San Francisco/O=Example/CN=example.com",
			}).
			WithExec([]string{"openssl", "genrsa",
				"-out", "/server.key",
				"2048",
			}).
			WithExec([]string{"openssl", "req",
				"-new",
				"-key", "/server.key",
				"-out", "/server.csr",
				"-passin", "pass:" + password,
				"-subj", "/C=US/ST=CA/L=San Francisco/O=Example/CN=example.com",
			}).
			WithNewFile("/server.ext", dagger.ContainerWithNewFileOpts{
				Contents: `authorityKeyIdentifier=keyid,issuer
basicConstraints=CA:FALSE
keyUsage = digitalSignature, nonRepudiation, keyEncipherment, dataEncipherment
subjectAltName = @alt_names

[alt_names]
DNS.1 = server
`,
			}).
			WithExec([]string{"openssl", "x509",
				"-req",
				"-in", "/server.csr",
				"-CA", "/ca.pem",
				"-CAkey", "/ca.key",
				"-CAcreateserial",
				"-out", "/server.crt",
				"-days", "99999",
				"-sha256",
				"-extfile", "/server.ext",
				"-passin", "pass:" + password,
			})

		caRootCert := genCtr.File("/ca.pem")
		serverCert := genCtr.File("/server.crt")
		serverKey := genCtr.File("/server.key")
		dhParam := genCtr.File("/dhparam.pem")

		devEngine := devEngineContainer(c).
			WithMountedFile("/usr/local/share/ca-certificates/dagger-test-custom-ca.crt", caRootCert).
			WithMountedCache("/var/lib/dagger", c.CacheVolume("dagger-dev-engine-state-"+identity.NewID())).
			WithExec([]string{
				"--addr", "tcp://0.0.0.0:1234",
				"--network-cidr", fmt.Sprintf("10.%d.0.0/16", netID), // avoid conflicts with other tests
				"--network-name", "testcacerts",
			}, dagger.ContainerWithExecOpts{
				InsecureRootCapabilities: true,
			})

		thisRepoPath, err := filepath.Abs("../..")
		require.NoError(t, err)
		thisRepo := c.Host().Directory(thisRepoPath)

		_, err = c.Container().From(golangImage).
			With(goCache(c)).
			WithMountedDirectory("/src", thisRepo).
			WithWorkdir("/src").
			WithMountedFile("/ca.crt", caRootCert).
			WithMountedFile("/server.crt", serverCert).
			WithMountedFile("/server.key", serverKey).
			WithMountedFile("/dhparam.pem", dhParam).
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
	caCert := c.Host().File("/ca.crt")
	serverCert := c.Host().File("/server.crt")
	serverKey := c.Host().File("/server.key")
	dhParam := c.Host().File("/dhparam.pem")

	// TODO: pin image
	serverCtr := c.Container().From("nginx:latest").
		WithMountedFile("/etc/ssl/certs/server.crt", serverCert).
		WithMountedFile("/etc/ssl/certs/dhparam.pem", dhParam).
		WithMountedFile("/etc/ssl/private/server.key", serverKey).
		WithNewFile("/etc/nginx/snippets/self-signed.conf", dagger.ContainerWithNewFileOpts{
			Contents: `ssl_certificate /etc/ssl/certs/server.crt;
ssl_certificate_key /etc/ssl/private/server.key;
`}).WithNewFile("/etc/nginx/snippets/ssl-params.conf", dagger.ContainerWithNewFileOpts{
		Contents: `ssl_protocols TLSv1 TLSv1.1 TLSv1.2;
ssl_prefer_server_ciphers on;
ssl_ciphers 'EECDH+AESGCM:EDH+AESGCM:AES256+EECDH:AES256+EDH';
ssl_ecdh_curve secp384r1;
ssl_session_cache shared:SSL:10m;
ssl_session_tickets off;
ssl_stapling_verify on;
resolver 10.90.0.1 valid=300s;
resolver_timeout 5s;
add_header Strict-Transport-Security "max-age=63072000; includeSubdomains; preload";
add_header X-Frame-Options DENY;
add_header X-Content-Type-Options nosniff;
ssl_dhparam /etc/ssl/certs/dhparam.pem;
`}).WithNewFile("/etc/nginx/conf.d/default.conf", dagger.ContainerWithNewFileOpts{
		Contents: `server {
	listen 80 default_server;
	listen [::]:80 default_server;
	server_name server;
	return 302 https://$server_name$request_uri;
}

server {
	listen 443 ssl http2 default_server;
	listen [::]:443 ssl http2 default_server;
	include snippets/self-signed.conf;
	include snippets/ssl-params.conf;
	server_name server;
	location / {
		return 200 "hello";
	}
}
`}).WithExec([]string{"nginx", "-t"}).
		WithExposedPort(443).
		WithExec([]string{"nginx"})

	caCertContents, err := caCert.Contents(ctx)
	require.NoError(t, err)

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			test.run(t, c, caCertsTestFixtures{
				serverCtr:      serverCtr,
				caCertContents: caCertContents,
			})
		})
	}
}
func TestContainerSystemCACerts(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	customCACertTests(ctx, t, c, 100,
		caCertsTest{"alpine basic", func(t *testing.T, c *dagger.Client, f caCertsTestFixtures) {
			ctr := c.Container().From(alpineImage).
				WithExec([]string{"apk", "add", "curl"})
			initialBundleContents, err := ctr.File("/etc/ssl/certs/ca-certificates.crt").Contents(ctx)
			require.NoError(t, err)

			ctr, err = ctr.
				WithServiceBinding("server", f.serverCtr.AsService()).
				WithExec([]string{"curl", "https://server"}).
				Sync(ctx)
			require.NoError(t, err)

			// verify no system CAs are leftover
			ents, err := ctr.Directory("/usr/local/share/ca-certificates").Entries(ctx)
			require.NoError(t, err)
			require.Empty(t, ents)

			bundleContents, err := ctr.File("/etc/ssl/certs/ca-certificates.crt").Contents(ctx)
			require.NoError(t, err)
			require.NotContains(t, bundleContents, f.caCertContents)
			require.Equal(t, initialBundleContents, bundleContents)
		}},

		caCertsTest{"alpine non-root user", func(t *testing.T, c *dagger.Client, f caCertsTestFixtures) {
			ctr := c.Container().From(alpineImage).
				WithExec([]string{"apk", "add", "curl"})
			initialBundleContents, err := ctr.File("/etc/ssl/certs/ca-certificates.crt").Contents(ctx)
			require.NoError(t, err)

			ctr, err = ctr.
				WithServiceBinding("server", f.serverCtr.AsService()).
				WithUser("nobody").
				WithExec([]string{"/usr/bin/curl", "https://server"}).
				Sync(ctx)
			require.NoError(t, err)

			// verify no system CAs are leftover
			ents, err := ctr.Directory("/usr/local/share/ca-certificates").Entries(ctx)
			require.NoError(t, err)
			require.Empty(t, ents)

			bundleContents, err := ctr.File("/etc/ssl/certs/ca-certificates.crt").Contents(ctx)
			require.NoError(t, err)
			require.NotContains(t, bundleContents, f.caCertContents)
			require.Equal(t, initialBundleContents, bundleContents)
		}},

		caCertsTest{"alpine install ca-certificates and curl at once", func(t *testing.T, c *dagger.Client, f caCertsTestFixtures) {
			ctr, err := c.Container().From(alpineImage).
				WithServiceBinding("server", f.serverCtr.AsService()).
				WithExec([]string{"sh", "-c", "apk add curl && curl https://server"}).
				Sync(ctx)
			require.NoError(t, err)

			// verify no system CAs are leftover
			ents, err := ctr.Directory("/usr/local/share/ca-certificates").Entries(ctx)
			require.NoError(t, err)
			require.Empty(t, ents)

			bundleContents, err := ctr.File("/etc/ssl/certs/ca-certificates.crt").Contents(ctx)
			require.NoError(t, err)
			require.NotEmpty(t, bundleContents)
			require.NotContains(t, bundleContents, f.caCertContents)
		}},

		caCertsTest{"alpine ca-certificates not installed", func(t *testing.T, c *dagger.Client, f caCertsTestFixtures) {
			ctr := c.Container().From(golangImage).
				WithExec([]string{"apk", "del", "ca-certificates"})

			// verify no system CAs are leftover
			_, err := ctr.Directory("/usr/local/share/ca-certificates").Entries(ctx)
			require.ErrorContains(t, err, "no such file or directory")

			bundleContents, err := ctr.File("/etc/ssl/certs/ca-certificates.crt").Contents(ctx)
			require.NoError(t, err)
			require.NotEmpty(t, bundleContents)
			require.NotContains(t, bundleContents, f.caCertContents)

			ctr, err = ctr.
				WithNewFile("/src/main.go", dagger.ContainerWithNewFileOpts{
					Contents: `package main

				import (
					"fmt"
					"net/http"
					"io"
				)

				func main() {
					resp, err := http.Get("https://server")
					if err != nil {
						panic(err)
					}
					if resp.StatusCode != 200 {
						panic(fmt.Sprintf("unexpected status code: %d", resp.StatusCode))
					}
					bs, err := io.ReadAll(resp.Body)
					if err != nil {
						panic(err)
					}
					if string(bs) != "hello" {
						panic("unexpected response: " + string(bs))
					}
				}
				`}).
				WithWorkdir("/src").
				WithExec([]string{"go", "mod", "init", "test"}).
				WithServiceBinding("server", f.serverCtr.AsService()).
				WithExec([]string{"go", "run", "main.go"}).
				Sync(ctx)
			require.NoError(t, err)

			// verify no system CAs are leftover
			_, err = ctr.Directory("/usr/local/share/ca-certificates").Entries(ctx)
			require.ErrorContains(t, err, "no such file or directory")

			bundleContents, err = ctr.File("/etc/ssl/certs/ca-certificates.crt").Contents(ctx)
			require.NoError(t, err)
			require.NotEmpty(t, bundleContents)
			require.NotContains(t, bundleContents, f.caCertContents)
		}},

		caCertsTest{"debian basic", func(t *testing.T, c *dagger.Client, f caCertsTestFixtures) {
			ctr := c.Container().From(debianImage).
				WithExec([]string{"apt", "update"}).
				WithExec([]string{"apt", "install", "-y", "curl"})
			initialBundleContents, err := ctr.File("/etc/ssl/certs/ca-certificates.crt").Contents(ctx)
			require.NoError(t, err)

			ctr, err = ctr.
				WithServiceBinding("server", f.serverCtr.AsService()).
				WithExec([]string{"curl", "https://server"}).
				Sync(ctx)
			require.NoError(t, err)

			// verify no system CAs are leftover
			ents, err := ctr.Directory("/usr/local/share/ca-certificates").Entries(ctx)
			require.NoError(t, err)
			require.Empty(t, ents)

			bundleContents, err := ctr.File("/etc/ssl/certs/ca-certificates.crt").Contents(ctx)
			require.NoError(t, err)
			require.NotContains(t, bundleContents, f.caCertContents)
			require.Equal(t, initialBundleContents, bundleContents)
		}},

		caCertsTest{"debian non-root user", func(t *testing.T, c *dagger.Client, f caCertsTestFixtures) {
			ctr := c.Container().From(debianImage).
				WithExec([]string{"apt", "update"}).
				WithExec([]string{"apt", "install", "-y", "curl"})
			initialBundleContents, err := ctr.File("/etc/ssl/certs/ca-certificates.crt").Contents(ctx)
			require.NoError(t, err)

			ctr, err = ctr.
				WithServiceBinding("server", f.serverCtr.AsService()).
				WithUser("nobody").
				WithExec([]string{"/usr/bin/curl", "https://server"}).
				Sync(ctx)
			require.NoError(t, err)

			// verify no system CAs are leftover
			ents, err := ctr.Directory("/usr/local/share/ca-certificates").Entries(ctx)
			require.NoError(t, err)
			require.Empty(t, ents)

			bundleContents, err := ctr.File("/etc/ssl/certs/ca-certificates.crt").Contents(ctx)
			require.NoError(t, err)
			require.NotContains(t, bundleContents, f.caCertContents)
			require.Equal(t, initialBundleContents, bundleContents)
		}},

		caCertsTest{"debian install ca-certificates and curl at once", func(t *testing.T, c *dagger.Client, f caCertsTestFixtures) {
			ctr, err := c.Container().From(debianImage).
				WithExec([]string{"apt", "update"}).
				WithServiceBinding("server", f.serverCtr.AsService()).
				WithExec([]string{"sh", "-c", "apt install -y curl && curl https://server"}).
				Sync(ctx)
			require.NoError(t, err)

			// verify no system CAs are leftover
			ents, err := ctr.Directory("/usr/local/share/ca-certificates").Entries(ctx)
			require.NoError(t, err)
			require.Empty(t, ents)

			bundleContents, err := ctr.File("/etc/ssl/certs/ca-certificates.crt").Contents(ctx)
			require.NoError(t, err)
			require.NotEmpty(t, bundleContents)
			require.NotContains(t, bundleContents, f.caCertContents)
		}},

		caCertsTest{"debian ca-certificates not installed", func(t *testing.T, c *dagger.Client, f caCertsTestFixtures) {
			ctr, err := c.Container().From(debianImage).
				WithExec([]string{"apt", "update"}).
				WithExec([]string{"apt", "install", "-y", "golang"}).
				WithNewFile("/src/main.go", dagger.ContainerWithNewFileOpts{
					Contents: `package main

					import (
						"fmt"
						"net/http"
						"io"
					)

					func main() {
						resp, err := http.Get("https://server")
						if err != nil {
							panic(err)
						}
						if resp.StatusCode != 200 {
							panic(fmt.Sprintf("unexpected status code: %d", resp.StatusCode))
						}
						bs, err := io.ReadAll(resp.Body)
						if err != nil {
							panic(err)
						}
						if string(bs) != "hello" {
							panic("unexpected response: " + string(bs))
						}
					}
					`}).
				WithWorkdir("/src").
				WithExec([]string{"go", "mod", "init", "test"}).
				WithServiceBinding("server", f.serverCtr.AsService()).
				WithExec([]string{"go", "run", "main.go"}).
				Sync(ctx)
			require.NoError(t, err)

			// verify no system CAs are leftover
			_, err = ctr.Directory("/usr/local/share/ca-certificates").Entries(ctx)
			require.ErrorContains(t, err, "no such file or directory")

			_, err = ctr.File("/etc/ssl/certs/ca-certificates.crt").Contents(ctx)
			require.ErrorContains(t, err, "no such file or directory")
		}},

		caCertsTest{"rhel basic", func(t *testing.T, c *dagger.Client, f caCertsTestFixtures) {
			ctr := c.Container().From(rhelImage)
			initialBundleContents, err := ctr.File("/etc/pki/tls/certs/ca-bundle.crt").Contents(ctx)
			require.NoError(t, err)

			ctr, err = ctr.
				WithServiceBinding("server", f.serverCtr.AsService()).
				WithExec([]string{"curl", "https://server"}).
				Sync(ctx)
			require.NoError(t, err)

			// verify no system CAs are leftover
			ents, err := ctr.Directory("/etc/pki/ca-trust/source/anchors").Entries(ctx)
			require.NoError(t, err)
			require.Empty(t, ents)

			bundleContents, err := ctr.File("/etc/pki/tls/certs/ca-bundle.crt").Contents(ctx)
			require.NoError(t, err)
			require.NotContains(t, bundleContents, f.caCertContents)
			require.Equal(t, initialBundleContents, bundleContents)
		}},

		caCertsTest{"rhel non-root user", func(t *testing.T, c *dagger.Client, f caCertsTestFixtures) {
			ctr := c.Container().From(rhelImage)
			initialBundleContents, err := ctr.File("/etc/pki/tls/certs/ca-bundle.crt").Contents(ctx)
			require.NoError(t, err)

			ctr, err = ctr.
				WithUser("nobody").
				WithServiceBinding("server", f.serverCtr.AsService()).
				WithExec([]string{"curl", "https://server"}).
				Sync(ctx)
			require.NoError(t, err)

			// verify no system CAs are leftover
			ents, err := ctr.Directory("/etc/pki/ca-trust/source/anchors").Entries(ctx)
			require.NoError(t, err)
			require.Empty(t, ents)

			bundleContents, err := ctr.File("/etc/pki/tls/certs/ca-bundle.crt").Contents(ctx)
			require.NoError(t, err)
			require.NotContains(t, bundleContents, f.caCertContents)
			require.Equal(t, initialBundleContents, bundleContents)
		}},
	)
}
