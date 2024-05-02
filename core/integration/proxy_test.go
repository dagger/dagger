package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/stretchr/testify/require"
)

// TODO: try testing with passwords too
// TODO: try testing with passwords too
// TODO: try testing with passwords too
// TODO: try testing with passwords too
// TODO: try testing with passwords too
// TODO: try testing with passwords too

type proxyTest struct {
	name string
	run  func(*testing.T, *dagger.Client, proxyTestFixtures)
}

type proxyTestFixtures struct {
	caCert *dagger.File
}

func customProxyTests(
	ctx context.Context,
	t *testing.T,
	c *dagger.Client,
	netID uint8,
	tests ...proxyTest,
) {
	t.Helper()

	executeTestEnvName := fmt.Sprintf("DAGGER_TEST_%s", strings.ToUpper(t.Name()))

	if os.Getenv(executeTestEnvName) == "" {
		const squidAlias = "squid"
		genCerts := generateCerts(c, squidAlias, squidAlias)

		squid := c.Container().From(alpineImage).
			WithExec([]string{"apk", "add", "squid", "ca-certificates"}).
			WithNewFile("/etc/squid/squid.conf", dagger.ContainerWithNewFileOpts{Contents: `
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

#
# Recommended minimum Access Permission configuration:
#
# Deny requests to certain unsafe ports
http_access deny !Safe_ports

# Deny CONNECT to other than secure SSL ports
http_access deny CONNECT !SSL_ports

# Only allow cachemgr access from localhost
http_access allow localhost manager
http_access deny manager

http_access allow localhost
http_access allow localnet

# And finally deny all other access to this proxy
http_access deny all

# Squid normally listens to port 3128
http_port 3128

ssl_bump bump all
https_port 3129 generate-host-certificates=on tls-cert=/etc/squid/server.pem tls-key=/etc/squid/serverkey.pem tls-dh=/etc/squid/dhparam.pem

sslpassword_program /usr/local/bin/printpass

# Leave coredumps in the first cache dir
coredump_dir /var/cache/squid

#
# Add any of your own refresh_pattern entries above these.
#
refresh_pattern ^ftp:           1440    20%     10080
refresh_pattern ^gopher:        1440    0%      1440
refresh_pattern -i (/cgi-bin/|\?) 0     0%      0
refresh_pattern .               0       20%     4320
`,
			}).
			WithExposedPort(3128).
			WithExposedPort(3129).
			WithMountedFile("/usr/local/bin/printpass", genCerts.printPasswordScript).
			WithMountedFile("/etc/ssl/certs/myCA.pem", genCerts.caRootCert).
			WithExec([]string{"update-ca-certificates"}).
			WithMountedFile("/etc/squid/server.pem", genCerts.serverCert).
			WithMountedFile("/etc/squid/serverkey.pem", genCerts.serverKey).
			WithMountedFile("/etc/squid/dhparam.pem", genCerts.dhParam).
			WithExec([]string{"squid", "--foreground"}).
			AsService()

		devEngine := devEngineContainer(c, netID, func(ctr *dagger.Container) *dagger.Container {
			return ctr.
				// go right to /etc/ssl/certs to avoid testing the custom CA cert support (covered elsewhere)
				WithMountedFile("/etc/ssl/certs/myCA.pem", genCerts.caRootCert).
				WithExec([]string{"update-ca-certificates"}, dagger.ContainerWithExecOpts{SkipEntrypoint: true}).
				WithEnvVariable("HTTPS_PROXY", fmt.Sprintf("https://%s:3129", squidAlias)).
				WithEnvVariable("HTTP_PROXY", fmt.Sprintf("http://%s:3128", squidAlias)).
				WithServiceBinding(squidAlias, squid)
		})

		thisRepoPath, err := filepath.Abs("../..")
		require.NoError(t, err)
		thisRepo := c.Host().Directory(thisRepoPath)

		_, err = c.Container().From(golangImage).
			With(goCache(c)).
			WithMountedDirectory("/src", thisRepo).
			WithWorkdir("/src").
			WithMountedFile("/ca.pem", genCerts.caRootCert).
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
			})
		})
	}
}

func TestContainerSystemProxies(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	customProxyTests(ctx, t, c, 101,
		proxyTest{"http", func(t *testing.T, c *dagger.Client, f proxyTestFixtures) {
			out, err := c.Container().From(alpineImage).
				WithExec([]string{"apk", "add", "curl"}).
				WithExec([]string{"curl", "-v", "http://www.example.com"}).
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
				WithExec([]string{"curl", "-v", "https://www.example.com"}).
				Stderr(ctx)
			require.NoError(t, err)
			require.Regexp(t, `.*< HTTP/1\.1 200 Connection established.*`, out)
			require.Regexp(t, `.*Establish HTTP proxy tunnel to www.example.com:443.*`, out)
		}},
	)
}
