package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dagger.io/dagger"
	"github.com/creack/pty"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (ContainerSuite) TestSystemCACerts(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	customCACertTests(ctx, t, c,
		caCertsTest{"alpine basic", func(t *testctx.T, c *dagger.Client, f caCertsTestFixtures) {
			ctr := c.Container().From(alpineImage).
				WithExec([]string{"apk", "add", "curl"})
			initialBundleContents, err := ctr.File("/etc/ssl/certs/ca-certificates.crt").Contents(ctx)
			require.NoError(t, err)

			ctr, err = ctr.
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

		caCertsTest{"alpine empty diff", func(t *testctx.T, c *dagger.Client, f caCertsTestFixtures) {
			ctr := c.Container().From(alpineImage)
			diff := ctr.Rootfs().Diff(ctr.WithExec([]string{"true"}).Rootfs())
			ents, err := diff.Glob(ctx, "**/*")
			require.NoError(t, err)
			require.Empty(t, ents)

			ctr = ctr.WithExec([]string{"apk", "add", "ca-certificates"})
			diff = ctr.Rootfs().Diff(ctr.WithExec([]string{"true"}).Rootfs())
			ents, err = diff.Glob(ctx, "**/*")
			require.NoError(t, err)
			require.Empty(t, ents)
		}},

		caCertsTest{"alpine non-root user", func(t *testctx.T, c *dagger.Client, f caCertsTestFixtures) {
			ctr := c.Container().From(alpineImage).
				WithExec([]string{"apk", "add", "curl"})
			initialBundleContents, err := ctr.File("/etc/ssl/certs/ca-certificates.crt").Contents(ctx)
			require.NoError(t, err)

			ctr, err = ctr.
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

		caCertsTest{"alpine install ca-certificates and curl at once", func(t *testctx.T, c *dagger.Client, f caCertsTestFixtures) {
			ctr, err := c.Container().From(alpineImage).
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

		caCertsTest{"alpine ca-certificates not installed", func(t *testctx.T, c *dagger.Client, f caCertsTestFixtures) {
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
				WithNewFile("/src/main.go", `package main

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
                    `,
				).
				WithWorkdir("/src").
				WithExec([]string{"go", "mod", "init", "test"}).
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

		caCertsTest{"debian basic", func(t *testctx.T, c *dagger.Client, f caCertsTestFixtures) {
			ctr := c.Container().From(debianImage).
				WithExec([]string{"apt", "update"}).
				WithExec([]string{"apt", "install", "-y", "curl"})
			initialBundleContents, err := ctr.File("/etc/ssl/certs/ca-certificates.crt").Contents(ctx)
			require.NoError(t, err)

			ctr, err = ctr.
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

		caCertsTest{"debian empty diff", func(t *testctx.T, c *dagger.Client, f caCertsTestFixtures) {
			ctr := c.Container().From(debianImage)
			diff := ctr.Rootfs().Diff(ctr.WithExec([]string{"true"}).Rootfs())
			ents, err := diff.Glob(ctx, "**/*")
			require.NoError(t, err)
			require.Empty(t, ents)

			ctr = ctr.WithExec([]string{"sh", "-c", "apt update && apt install -y ca-certificates"})
			diff = ctr.Rootfs().Diff(ctr.WithExec([]string{"true"}).Rootfs())
			ents, err = diff.Glob(ctx, "**/*")
			require.NoError(t, err)
			require.Empty(t, ents)
		}},

		caCertsTest{"debian non-root user", func(t *testctx.T, c *dagger.Client, f caCertsTestFixtures) {
			ctr := c.Container().From(debianImage).
				WithExec([]string{"apt", "update"}).
				WithExec([]string{"apt", "install", "-y", "curl"})
			initialBundleContents, err := ctr.File("/etc/ssl/certs/ca-certificates.crt").Contents(ctx)
			require.NoError(t, err)

			ctr, err = ctr.
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

		caCertsTest{"debian install ca-certificates and curl at once", func(t *testctx.T, c *dagger.Client, f caCertsTestFixtures) {
			ctr, err := c.Container().From(debianImage).
				WithExec([]string{"apt", "update"}).
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

		caCertsTest{"debian ca-certificates not installed", func(t *testctx.T, c *dagger.Client, f caCertsTestFixtures) {
			ctr, err := c.Container().From(debianImage).
				WithExec([]string{"apt", "update"}).
				WithExec([]string{"apt", "install", "-y", "golang"}).
				WithNewFile("/src/main.go", `package main

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
						`,
				).
				WithWorkdir("/src").
				WithExec([]string{"go", "mod", "init", "test"}).
				WithExec([]string{"go", "run", "main.go"}).
				Sync(ctx)
			require.NoError(t, err)

			// verify no system CAs are leftover
			_, err = ctr.Directory("/usr/local/share/ca-certificates").Entries(ctx)
			require.ErrorContains(t, err, "no such file or directory")

			_, err = ctr.File("/etc/ssl/certs/ca-certificates.crt").Contents(ctx)
			require.ErrorContains(t, err, "no such file or directory")
		}},

		caCertsTest{"rhel basic", func(t *testctx.T, c *dagger.Client, f caCertsTestFixtures) {
			ctr := c.Container().From(rhelImage)
			initialBundleContents, err := ctr.File("/etc/pki/tls/certs/ca-bundle.crt").Contents(ctx)
			require.NoError(t, err)

			ctr, err = ctr.
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

		caCertsTest{"rhel empty diff", func(t *testctx.T, c *dagger.Client, f caCertsTestFixtures) {
			ctr := c.Container().From(rhelImage)
			diff := ctr.Rootfs().Diff(ctr.WithExec([]string{"true"}).Rootfs())
			ents, err := diff.Glob(ctx, "**/*")
			require.NoError(t, err)
			require.Empty(t, ents)
		}},

		caCertsTest{"rhel non-root user", func(t *testctx.T, c *dagger.Client, f caCertsTestFixtures) {
			ctr := c.Container().From(rhelImage)
			initialBundleContents, err := ctr.File("/etc/pki/tls/certs/ca-bundle.crt").Contents(ctx)
			require.NoError(t, err)

			ctr, err = ctr.
				WithUser("nobody").
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

		caCertsTest{"go module", func(t *testctx.T, c *dagger.Client, f caCertsTestFixtures) {
			out, err := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("init", "--name=test", "--sdk=go")).
				With(sdkSource("go", `package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

type Test struct {}

func (m *Test) GetHttp(ctx context.Context) (string, error) {
	resp, err := http.Get("https://server")
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}
	bs, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(bs), nil
}
`)).
				With(daggerCall("get-http")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hello", strings.TrimSpace(out))
		}},

		caCertsTest{"python module", func(t *testctx.T, c *dagger.Client, f caCertsTestFixtures) {
			out, err := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("init", "--name=test", "--sdk=python")).
				With(sdkSource("python", `
import urllib.request

import dagger

@dagger.object_type
class Test:
    @dagger.function
    def get_http(self) -> str:
            return urllib.request.urlopen("https://server").read().decode("utf-8")
`)).
				With(daggerCall("get-http")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hello", strings.TrimSpace(out))
		}},

		caCertsTest{"typescript module", func(t *testctx.T, c *dagger.Client, f caCertsTestFixtures) {
			out, err := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("init", "--name=test", "--sdk=typescript")).
				With(sdkSource("typescript", `
import { object, func } from "@dagger.io/dagger";
import * as https from "https";

@object()
class Test {
	@func()
    async getHttp(): Promise<string> {
        const url = "https://server";
				// thanks chatGPT for this, sorry to anyone else if this is awful
        return new Promise((resolve, reject) => {
            https.get(url, (res) => {
                let data = '';
                res.on('data', (chunk) => {
                    data += chunk;
                });
                res.on('end', () => {
                    if (res.statusCode === 200) {
                        resolve(data);
                    } else {
                        reject("Request failed with status code " + res.statusCode);
                    }
                });
            }).on('error', (err) => {
                reject("Error: " + err.message);
            });
        });
    }
}
`)).
				With(daggerCall("get-http")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hello", strings.TrimSpace(out))
		}},

		caCertsTest{"terminal", func(t *testctx.T, _ *dagger.Client, f caCertsTestFixtures) {
			modDir := t.TempDir()
			err := os.WriteFile(filepath.Join(modDir, "main.go"), []byte(fmt.Sprintf(`package main

	import (
		"context"
		"dagger/test/internal/dagger"
	)

	func New(ctx context.Context) *Test {
		return &Test{
			Ctr: dag.Container().
				From("%s").
				WithExec([]string{"apk", "add", "curl"}).
				WithDefaultTerminalCmd([]string{"/bin/sh"}),
		}
	}

	type Test struct {
		Ctr *dagger.Container
	}
	`, alpineImage)), 0644)
			require.NoError(t, err)

			initCmd := hostDaggerCommand(ctx, t, modDir, "init", "--source=.", "--name=test", "--sdk=go")
			copy(initCmd.Env, os.Environ())
			initCmd.Env = append(initCmd.Env, "_EXPERIMENTAL_DAGGER_RUNNER_HOST="+f.engineEndpoint)
			initOutput, err := initCmd.CombinedOutput()
			require.NoError(t, err, initOutput)

			// cache the module load itself so there's less to wait for in the shell invocation below
			functionsCmd := hostDaggerCommand(ctx, t, modDir, "functions")
			copy(functionsCmd.Env, os.Environ())
			functionsCmd.Env = append(functionsCmd.Env, "_EXPERIMENTAL_DAGGER_RUNNER_HOST="+f.engineEndpoint)
			functionsOutput, err := functionsCmd.CombinedOutput()
			require.NoError(t, err, functionsOutput)

			// timeout for waiting for each expected line is very generous in case CI is under heavy load or something
			console, err := newTUIConsole(t, 60*time.Second)
			require.NoError(t, err)
			defer console.Close()

			tty := console.Tty()

			// We want the size to be big enough to fit the output we're expecting, but increasing
			// the size also eventually slows down the tests due to more output being generated and
			// needing parsing.
			err = pty.Setsize(tty, &pty.Winsize{Rows: 6, Cols: 16})
			require.NoError(t, err)

			cmd := hostDaggerCommand(ctx, t, modDir, "call", "ctr", "terminal")
			copy(cmd.Env, os.Environ())
			cmd.Env = append(cmd.Env, "_EXPERIMENTAL_DAGGER_RUNNER_HOST="+f.engineEndpoint)
			cmd.Stdin = tty
			cmd.Stdout = tty
			cmd.Stderr = tty

			err = cmd.Start()
			require.NoError(t, err)

			_, err = console.ExpectString(" $ ")
			require.NoError(t, err)

			_, err = console.SendLine("set -e")
			require.NoError(t, err)

			_, err = console.ExpectString(" $ ")
			require.NoError(t, err)

			_, err = console.SendLine("curl https://server")
			require.NoError(t, err)

			_, err = console.ExpectString(" $ ")
			require.NoError(t, err)

			_, err = console.SendLine("exit")
			require.NoError(t, err)

			go console.ExpectEOF()

			err = cmd.Wait()
			require.NoError(t, err)
		}},
	)
}

type caCertsTest struct {
	name string
	run  func(*testctx.T, *dagger.Client, caCertsTestFixtures)
}

type caCertsTestFixtures struct {
	caCertContents string
	engineEndpoint string
}

func customCACertTests(
	ctx context.Context,
	t *testctx.T,
	c *dagger.Client,
	tests ...caCertsTest,
) {
	t.Helper()

	certGen := newGeneratedCerts(c, "ca")
	serverCert, serverKey := certGen.newServerCerts("server")

	serverCtr := nginxWithCerts(c, nginxWithCertsOpts{
		serverCert:          serverCert,
		serverKey:           serverKey,
		dhParam:             certGen.dhParam,
		dnsName:             "server",
		msg:                 "hello",
		redirectHTTPToHTTPS: true,
	})

	devEngine := devEngineContainer(c, func(ctr *dagger.Container) *dagger.Container {
		return ctr.
			WithMountedFile("/usr/local/share/ca-certificates/dagger-test-custom-ca.crt", certGen.caRootCert).
			WithServiceBinding("server", serverCtr.AsService())
	})
	engineSvc, err := c.Host().Tunnel(devEngine.AsService()).Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { engineSvc.Stop(ctx) })
	endpoint, err := engineSvc.Endpoint(ctx, dagger.ServiceEndpointOpts{Scheme: "tcp"})
	require.NoError(t, err)
	c2, err := dagger.Connect(ctx, dagger.WithRunnerHost(endpoint), dagger.WithLogOutput(testutil.NewTWriter(t)))
	require.NoError(t, err)
	t.Cleanup(func() { c2.Close() })

	caCertContents, err := certGen.caRootCert.Contents(ctx)
	require.NoError(t, err)

	for _, test := range tests {
		test := test
		t.Run(test.name, func(ctx context.Context, t *testctx.T) {
			test.run(t, c2, caCertsTestFixtures{
				caCertContents: caCertContents,
				engineEndpoint: endpoint,
			})
		})
	}
}

type generatedCerts struct {
	c   *dagger.Client
	ctr *dagger.Container

	caRootCert *dagger.File
	caRootKey  *dagger.File
	dhParam    *dagger.File

	password string
	// executable shell script that just prints password, needed for
	// squid currently
	printPasswordScript *dagger.File
}

func newGeneratedCerts(c *dagger.Client, caHostname string) *generatedCerts {
	const password = "hunter4"
	ctr := c.Container().From(alpineImage).
		WithExec([]string{"apk", "add", "openssl"}).
		WithExec([]string{"sh", "-c", strings.Join([]string{
			"openssl", "dhparam",
			"-out", "/dhparam.pem",
			"2048",
			// suppress extremely noisy+useless output
			"&> /dev/null",
		}, " ")}).
		WithExec([]string{
			"openssl", "genrsa",
			"-des3",
			"-out", "/ca.key",
			"-passout", "pass:" + password,
			"2048",
		}).
		WithExec([]string{
			"openssl", "req",
			"-new",
			"-key", "/ca.key",
			"-out", "/ca.csr",
			"-passin", "pass:" + password,
			"-subj", "/C=US/ST=CA/L=San Francisco/O=Example/CN=" + caHostname,
		}).
		WithNewFile("/ca.ext", fmt.Sprintf(`basicConstraints=critical,CA:TRUE,pathlen:0
keyUsage=critical,keyCertSign,cRLSign
subjectKeyIdentifier=hash
authorityKeyIdentifier=keyid:always,issuer:always
subjectAltName=@alt_names

[alt_names]
DNS.1 = %s
`, caHostname),
		).
		WithExec([]string{
			"openssl", "x509",
			"-req",
			"-in", "/ca.csr",
			"-signkey", "/ca.key",
			"-out", "/ca.pem",
			"-days", "99999",
			"-sha256",
			"-extfile", "/ca.ext",
			"-passin", "pass:" + password,
		})

	return &generatedCerts{
		c:          c,
		ctr:        ctr,
		caRootCert: ctr.File("/ca.pem"),
		caRootKey:  ctr.File("/ca.key"),
		dhParam:    ctr.File("/dhparam.pem"),
		password:   password,
		printPasswordScript: c.Directory().WithNewFile("printpass", fmt.Sprintf(`#!/bin/sh
echo -n %s
`, password), dagger.DirectoryWithNewFileOpts{Permissions: 0o755}).File("printpass"),
	}
}

// returns Files for cert and key
func (g *generatedCerts) newServerCerts(serverHostname string) (*dagger.File, *dagger.File) {
	ctr := g.ctr.
		WithExec([]string{
			"openssl", "genrsa",
			"-out", "/server.key",
			"2048",
		}).
		WithExec([]string{
			"openssl", "req",
			"-new",
			"-key", "/server.key",
			"-out", "/server.csr",
			"-passin", "pass:" + g.password,
			"-subj", "/C=US/ST=CA/L=San Francisco/O=Example/CN=" + serverHostname,
		}).
		WithNewFile("/server.ext", fmt.Sprintf(`authorityKeyIdentifier=keyid,issuer
basicConstraints=CA:FALSE
keyUsage = digitalSignature, nonRepudiation, keyEncipherment, dataEncipherment
subjectAltName = @alt_names

[alt_names]
DNS.1 = %s
`, serverHostname),
		).
		WithExec([]string{
			"openssl", "x509",
			"-req",
			"-in", "/server.csr",
			"-CA", "/ca.pem",
			"-CAkey", "/ca.key",
			"-CAcreateserial",
			"-out", "/server.pem",
			"-days", "99999",
			"-sha256",
			"-extfile", "/server.ext",
			"-passin", "pass:" + g.password,
		})

	return ctr.File("/server.pem"), ctr.File("/server.key")
}

type nginxWithCertsOpts struct {
	serverCert          *dagger.File
	serverKey           *dagger.File
	dhParam             *dagger.File
	dnsName             string
	msg                 string
	redirectHTTPToHTTPS bool
}

func nginxWithCerts(c *dagger.Client, opts nginxWithCertsOpts) *dagger.Container {
	// TODO: pin image
	ctr := c.Container().From("nginx:latest").
		WithMountedFile("/etc/ssl/certs/server.crt", opts.serverCert).
		WithMountedFile("/etc/ssl/certs/dhparam.pem", opts.dhParam).
		WithMountedFile("/etc/ssl/private/server.key", opts.serverKey).
		WithNewFile("/etc/nginx/snippets/self-signed.conf", `ssl_certificate /etc/ssl/certs/server.crt;
ssl_certificate_key /etc/ssl/private/server.key;
`,
		).
		WithNewFile("/etc/nginx/snippets/ssl-params.conf", `ssl_protocols TLSv1 TLSv1.1 TLSv1.2;
ssl_prefer_server_ciphers on;
ssl_ciphers 'EECDH+AESGCM:EDH+AESGCM:AES256+EECDH:AES256+EDH';
ssl_ecdh_curve secp384r1;
ssl_session_cache shared:SSL:10m;
ssl_session_tickets off;
ssl_stapling_verify on;
add_header Strict-Transport-Security "max-age=63072000; includeSubdomains; preload";
add_header X-Frame-Options DENY;
add_header X-Content-Type-Options nosniff;
ssl_dhparam /etc/ssl/certs/dhparam.pem;
`,
		)

	conf := fmt.Sprintf(`server {
	listen 443 ssl http2 default_server;
	listen [::]:443 ssl http2 default_server;
	include snippets/self-signed.conf;
	include snippets/ssl-params.conf;
	server_name %s;
	location / {
		return 200 "%s";
	}
}
`, opts.dnsName, opts.msg)

	if opts.redirectHTTPToHTTPS {
		conf += fmt.Sprintf(`server {
	listen 80 default_server;
	listen [::]:80 default_server;
	server_name %s;
	return 302 https://$server_name$request_uri;
}
`, opts.dnsName)
	} else {
		conf += fmt.Sprintf(`server {
	listen 80 default_server;
	listen [::]:80 default_server;
	server_name %s;
	location / {
		return 200 "%s";
	}
}
`, opts.dnsName, opts.msg)
	}

	return ctr.
		WithNewFile("/etc/nginx/conf.d/default.conf", conf).
		WithExec([]string{"nginx", "-t"}).
		WithExposedPort(80).
		WithExposedPort(443).
		WithExec([]string{"nginx", "-g", "daemon off;"})
}
