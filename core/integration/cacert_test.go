package core

// These tests cover custom CA certificates made available to containers and
// network clients so TLS connections trust expected certificate authorities.
//
// See also:
// - http_test.go: HTTP and HTTPS resource fetching.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"dagger.io/dagger"
	"github.com/creack/pty"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (ContainerSuite) TestSystemCACerts(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	customCACertTests(ctx, t, c, "",
		caCertsTest{"alpine basic", func(ctx context.Context, t *testctx.T, c *dagger.Client, f caCertsTestFixtures) {
			ctr := c.Container().From(alpineImage).
				WithExec([]string{"apk", "add", "ca-certificates", "curl"})
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

		caCertsTest{"alpine empty diff", func(ctx context.Context, t *testctx.T, c *dagger.Client, f caCertsTestFixtures) {
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

		caCertsTest{"alpine non-root user", func(ctx context.Context, t *testctx.T, c *dagger.Client, f caCertsTestFixtures) {
			ctr := c.Container().From(alpineImage).
				WithExec([]string{"apk", "add", "ca-certificates", "curl"})
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

		caCertsTest{"alpine install ca-certificates and curl at once", func(ctx context.Context, t *testctx.T, c *dagger.Client, f caCertsTestFixtures) {
			ctr, err := c.Container().From(alpineImage).
				WithExec([]string{"sh", "-c", "apk add ca-certificates curl && curl https://server"}).
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

		caCertsTest{"alpine ca-certificates not installed", func(ctx context.Context, t *testctx.T, c *dagger.Client, f caCertsTestFixtures) {
			ctr := c.Container().From(golangImage).
				WithExec([]string{"apk", "update"}).
				WithExec([]string{"apk", "del", "ca-certificates"})

			// verify no system CAs are leftover
			_, err := ctr.Directory("/usr/local/share/ca-certificates").Entries(ctx)
			requireErrOut(t, err, "no such file or directory")

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
			requireErrOut(t, err, "no such file or directory")

			bundleContents, err = ctr.File("/etc/ssl/certs/ca-certificates.crt").Contents(ctx)
			require.NoError(t, err)
			require.NotEmpty(t, bundleContents)
			require.NotContains(t, bundleContents, f.caCertContents)
		}},

		caCertsTest{"wolfi basic", func(ctx context.Context, t *testctx.T, c *dagger.Client, f caCertsTestFixtures) {
			ctr := c.Container().From(wolfiImage).
				WithExec([]string{"apk", "add", "ca-certificates", "curl"})
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

		caCertsTest{"debian basic", func(ctx context.Context, t *testctx.T, c *dagger.Client, f caCertsTestFixtures) {
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

		caCertsTest{"debian empty diff", func(ctx context.Context, t *testctx.T, c *dagger.Client, f caCertsTestFixtures) {
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

		caCertsTest{"debian non-root user", func(ctx context.Context, t *testctx.T, c *dagger.Client, f caCertsTestFixtures) {
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

		caCertsTest{"debian install ca-certificates and curl at once", func(ctx context.Context, t *testctx.T, c *dagger.Client, f caCertsTestFixtures) {
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

		caCertsTest{"debian ca-certificates not installed", func(ctx context.Context, t *testctx.T, c *dagger.Client, f caCertsTestFixtures) {
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
			requireErrOut(t, err, "no such file or directory")

			_, err = ctr.File("/etc/ssl/certs/ca-certificates.crt").Contents(ctx)
			requireErrOut(t, err, "no such file or directory")
		}},

		caCertsTest{"rhel basic", func(ctx context.Context, t *testctx.T, c *dagger.Client, f caCertsTestFixtures) {
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

		caCertsTest{"rhel empty diff", func(ctx context.Context, t *testctx.T, c *dagger.Client, f caCertsTestFixtures) {
			ctr := c.Container().From(rhelImage)
			diff := ctr.Rootfs().Diff(ctr.WithExec([]string{"true"}).Rootfs())
			ents, err := diff.Glob(ctx, "**/*")
			require.NoError(t, err)
			require.Empty(t, ents)
		}},

		caCertsTest{"rhel non-root user", func(ctx context.Context, t *testctx.T, c *dagger.Client, f caCertsTestFixtures) {
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

		caCertsTest{"nixos-like basic", func(ctx context.Context, t *testctx.T, c *dagger.Client, f caCertsTestFixtures) {
			ctr := nixosLikeContainer(c, c.Container().From(alpineImage).
				WithExec([]string{"apk", "add", "ca-certificates", "curl"}))
			initialBundleContents, err := ctr.File("/etc/ssl/certs/ca-certificates.crt").Contents(ctx)
			require.NoError(t, err)
			require.NotEmpty(t, initialBundleContents)

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

		caCertsTest{"nixos-like ca-certificates not installed", func(ctx context.Context, t *testctx.T, c *dagger.Client, f caCertsTestFixtures) {
			// golangImage is alpine-based — strip its ca-certificates+update-ca-certificates so
			// the no-update-cmd fallback path is the only one available, mirroring "alpine
			// ca-certificates not installed".
			ctr := nixosLikeContainer(c, c.Container().From(golangImage).
				WithExec([]string{"apk", "update"}).
				WithExec([]string{"apk", "del", "ca-certificates"}))

			initialBundleContents, err := ctr.File("/etc/ssl/certs/ca-certificates.crt").Contents(ctx)
			require.NoError(t, err)
			require.NotEmpty(t, initialBundleContents)
			require.NotContains(t, initialBundleContents, f.caCertContents)

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
			requireErrOut(t, err, "no such file or directory")

			// Bundle path should resolve through the restored symlink to the original
			// content (no test CA leaked). Equal-with-initial implicitly validates
			// that nixosLike.Uninstall restored the symlink to the same target; if
			// the swap path had been bypassed and a different installer had written
			// through to a regular file, the contents wouldn't match initial state.
			bundleContents, err := ctr.File("/etc/ssl/certs/ca-certificates.crt").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, initialBundleContents, bundleContents)
			require.NotContains(t, bundleContents, f.caCertContents)
		}},

		caCertsTest{"nixos-like non-root user", func(ctx context.Context, t *testctx.T, c *dagger.Client, f caCertsTestFixtures) {
			ctr := nixosLikeContainer(c, c.Container().From(alpineImage).
				WithExec([]string{"apk", "add", "ca-certificates", "curl"}))
			initialBundleContents, err := ctr.File("/etc/ssl/certs/ca-certificates.crt").Contents(ctx)
			require.NoError(t, err)

			ctr, err = ctr.
				WithUser("nobody").
				WithExec([]string{"/usr/bin/curl", "https://server"}).
				Sync(ctx)
			require.NoError(t, err)

			bundleContents, err := ctr.File("/etc/ssl/certs/ca-certificates.crt").Contents(ctx)
			require.NoError(t, err)
			require.NotContains(t, bundleContents, f.caCertContents)
			require.Equal(t, initialBundleContents, bundleContents)
		}},

		caCertsTest{"nixos-like SSL_CERT_FILE override", func(ctx context.Context, t *testctx.T, c *dagger.Client, f caCertsTestFixtures) {
			// SSL_CERT_FILE honoring works via commonInstaller's no-update-cmd
			// fallback path — that path appends directly to bundlePath. The
			// update-cmd path can't honor SSL_CERT_FILE because update-ca-certificates
			// writes to its own hard-coded output path. So this test deliberately
			// uses an image without `ca-certificates` installed, putting the bundle
			// at a non-canonical path the image points at via SSL_CERT_FILE.
			bundleSrc := c.Container().From(alpineImage).
				WithExec([]string{"apk", "add", "ca-certificates"}).
				File("/etc/ssl/certs/ca-certificates.crt")

			ctr := c.Container().From(alpineImage).
				WithExec([]string{"apk", "add", "curl"}).
				WithoutFile("/etc/alpine-release").
				WithNewFile("/etc/NIXOS", "").
				WithNewFile("/etc/os-release", "ID=nixos\n").
				WithSymlink("/opt/ro-bundle/ca-bundle.crt", "/opt/custom/bundle.crt").
				WithFile("/opt/ro-bundle/ca-bundle.crt", bundleSrc).
				WithEnvVariable("SSL_CERT_FILE", "/opt/custom/bundle.crt")

			// Alpine ships /etc/ssl/certs/ca-certificates.crt via libcrypto/openssl
			// even when ca-certificates isn't installed, so the canonical path
			// exists baseline. The honoring-SSL_CERT_FILE invariant is that the
			// installer (running via the no-update-cmd fallback) only touches
			// bundlePath = $SSL_CERT_FILE — the canonical bundle's bytes must
			// be identical before and after the exec.
			canonicalBundleBefore, err := ctr.File("/etc/ssl/certs/ca-certificates.crt").Contents(ctx)
			require.NoError(t, err)
			require.NotEmpty(t, canonicalBundleBefore)
			require.NotContains(t, canonicalBundleBefore, f.caCertContents,
				"fixture invariant: canonical bundle must not contain the test CA up front")

			ctr, err = ctr.WithExec([]string{"curl", "https://server"}).Sync(ctx)
			require.NoError(t, err)

			canonicalBundleAfter, err := ctr.File("/etc/ssl/certs/ca-certificates.crt").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, canonicalBundleBefore, canonicalBundleAfter,
				"installer wrote to the canonical bundle path instead of honoring SSL_CERT_FILE")
			require.NotContains(t, canonicalBundleAfter, f.caCertContents,
				"installer leaked the test CA into the canonical bundle")
		}},

		caCertsTest{"go module", func(ctx context.Context, t *testctx.T, c *dagger.Client, f caCertsTestFixtures) {
			out, err := moduleFixture(t, c, "go/https-client").
				With(daggerCallAt(".", "get-http")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hello", strings.TrimSpace(out))
		}},

		caCertsTest{"python module", func(ctx context.Context, t *testctx.T, c *dagger.Client, f caCertsTestFixtures) {
			out, err := moduleFixture(t, c, "python/https-client").
				With(daggerCallAt(".", "get-http")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hello", strings.TrimSpace(out))
		}},

		caCertsTest{"typescript module", func(ctx context.Context, t *testctx.T, c *dagger.Client, f caCertsTestFixtures) {
			out, err := moduleFixture(t, c, "typescript/https-client").
				With(daggerCallAt(".", "get-http")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hello", strings.TrimSpace(out))
		}},

		caCertsTest{"terminal", func(ctx context.Context, t *testctx.T, c *dagger.Client, f caCertsTestFixtures) {
			modDir := t.TempDir()
			copyTestdataFixture(ctx, t, modDir, "modules", "go", "cacert-terminal")

			// cache the module load itself so there's less to wait for in the shell invocation below
			functionsCmd := hostDaggerCommand(ctx, t, modDir, "api", "functions", "-m", ".")
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

			cmd := hostDaggerCommand(ctx, t, modDir, "call", "-m", ".", "ctr", "terminal")
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
	customCACertTests(ctx, t, c, "orbstack-root.crt",
		caCertsTest{"orbstack ignored", func(ctx context.Context, t *testctx.T, c *dagger.Client, f caCertsTestFixtures) {
			_, err := c.Container().From(alpineImage).
				WithExec([]string{"stat", "/usr/local/share/ca-certificates/orbstack-root.crt"}).
				Sync(ctx)
			requireErrOut(t, err, "No such file or directory")
		}},
	)
}

type caCertsTest struct {
	name string
	run  func(context.Context, *testctx.T, *dagger.Client, caCertsTestFixtures)
}

type caCertsTestFixtures struct {
	caCertContents string
	engineEndpoint string
}

// nixosLikeContainer turns an existing container into one that detects as
// NixOS-style and exposes /etc/ssl/certs/ca-certificates.crt as a symlink to
// a baked-in bundle file. All ops are layer-level (no exec) so the
// synthesis doesn't itself trigger the cacerts install path mid-setup.
// WithFile (not WithMountedFile) for the bundle target — mounts aren't
// visible to client-side File reads or to the engine-side cacerts
// installer's rootfs view, so a mounted target makes the symlink unresolvable.
func nixosLikeContainer(c *dagger.Client, base *dagger.Container) *dagger.Container {
	bundleSrc := c.Container().From(alpineImage).
		WithExec([]string{"apk", "add", "ca-certificates"}).
		File("/etc/ssl/certs/ca-certificates.crt")
	return base.
		WithoutFile("/etc/alpine-release").
		WithNewFile("/etc/NIXOS", "").
		WithNewFile("/etc/os-release", "ID=nixos\nID_LIKE=nixos\n").
		WithoutFile("/etc/ssl/certs/ca-certificates.crt").
		WithSymlink("/opt/ro-bundle/ca-bundle.crt", "/etc/ssl/certs/ca-certificates.crt").
		WithFile("/opt/ro-bundle/ca-bundle.crt", bundleSrc)
}

func customCACertTests(
	ctx context.Context,
	t *testctx.T,
	c *dagger.Client,
	caCertFileName string, // if empty, a default is used
	tests ...caCertsTest,
) {
	t.Helper()

	certGen := newGeneratedCerts(c, "ca")
	serverCert, serverKey := certGen.newServerCerts("server")

	serverCtr := nginxWithCerts(c, nginxWithCertsOpts{
		serverCert:          serverCert,
		serverKey:           serverKey,
		dnsName:             "server",
		msg:                 "hello",
		redirectHTTPToHTTPS: true,
	})

	if caCertFileName == "" {
		caCertFileName = "dagger-test-custom-ca.crt"
	}
	devEngine := devEngineContainer(c, func(ctr *dagger.Container) *dagger.Container {
		return ctr.
			WithMountedFile("/usr/local/share/ca-certificates/"+caCertFileName, certGen.caRootCert).
			WithServiceBinding("server", serverCtr.AsService())
	})
	engineSvc, err := c.Host().Tunnel(devEngineContainerAsService(devEngine)).Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = engineSvc.Stop(ctx) })
	endpoint, err := engineSvc.Endpoint(ctx, dagger.ServiceEndpointOpts{Scheme: "tcp"})
	require.NoError(t, err)
	c2, err := dagger.Connect(
		ctx,
		dagger.WithRunnerHost(endpoint),
		dagger.WithLogOutput(testutil.NewTWriter(t)),
	)
	require.NoError(t, err)
	t.Cleanup(func() { c2.Close() })

	caCertContents, err := certGen.caRootCert.Contents(ctx)
	require.NoError(t, err)

	for _, test := range tests {
		t.Run(test.name, func(ctx context.Context, t *testctx.T) {
			test.run(ctx, t, c2, caCertsTestFixtures{
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

	password string
	// executable shell script that just prints password, needed for
	// squid currently
	printPasswordScript *dagger.File
}

func newGeneratedCerts(c *dagger.Client, caHostname string) *generatedCerts {
	const password = "hunter4"
	ctr := c.Container().From(alpineImage).
		WithExec([]string{"apk", "add", "openssl"}).
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
	dnsName             string
	msg                 string
	redirectHTTPToHTTPS bool
}

func nginxWithCerts(c *dagger.Client, opts nginxWithCertsOpts) *dagger.Container {
	// TODO: pin image
	ctr := c.Container().From("nginx:latest").
		WithMountedFile("/etc/ssl/certs/server.crt", opts.serverCert).
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
		WithDefaultArgs([]string{"nginx", "-g", "daemon off;"})
}
