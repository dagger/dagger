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
	caCertsDir *dagger.Directory
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
		caCertsCacheVolume := c.CacheVolume("ca-certs-" + identity.NewID())

		base := c.Container().From(alpineImage)
		squid := base.
			WithExec([]string{"apk", "add", "squid", "openssl", "ca-certificates"}).
			WithNewFile("/usr/local/bin/printpass", dagger.ContainerWithNewFileOpts{Contents: `#!/bin/sh
echo -n hunter4
`, Permissions: 0755}).
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

# Uncomment and adjust the following to add a disk cache directory.
#cache_dir ufs /var/cache/squid 100 16 256

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
			// This is a bit annoying: we need to generate certs with the real hostname, but it's a content
			// hash of the container itself, so we can't know it ahead of time. We can't use aliases since
			// they are not actual hostnames and the final client container won't know about them in this case.
			// Pure "*" wildcards in certs didn't work either. So we have to generate everything in the container,
			// when we actually know the hostname.
			// TODO: I'm sure there's some better way of dealing with this... My lack of TLS-fu is limiting.
			WithMountedDirectory("/etc/ssl/origcerts", base.Directory("/etc/ssl/certs")).
			WithMountedCache("/etc/ssl/certs", caCertsCacheVolume).
			WithExec([]string{"sh", "-c", `
set -ex

HOSTNAME=$(hostname)'.*'
ENDPOINT=$(echo $HOSTNAME | cut -d. -f1)

cp -r /etc/ssl/origcerts/* /etc/ssl/certs/

openssl dhparam -out /etc/squid/dhparam.pem 2048

openssl genrsa -des3 -out /etc/squid/ca.key -passout pass:hunter4 2048

openssl req -new -key /etc/squid/ca.key -out /etc/squid/ca.csr -passin pass:hunter4 -subj "/C=US/ST=CA/L=San Francisco/O=Example/CN=${ENDPOINT}"

cat > /etc/squid/ca.ext <<EOF
basicConstraints=critical,CA:TRUE,pathlen:0
keyUsage=critical,keyCertSign,cRLSign
subjectKeyIdentifier=hash
authorityKeyIdentifier=keyid:always,issuer:always
subjectAltName=@alt_names

[alt_names]
DNS.1 = ${ENDPOINT}
EOF

openssl x509 -req -in /etc/squid/ca.csr -signkey /etc/squid/ca.key -out /etc/ssl/certs/squidCA.pem -days 99999 -sha256 -extfile /etc/squid/ca.ext -passin pass:hunter4

update-ca-certificates

openssl genrsa -out /etc/squid/serverkey.pem 2048

openssl req -new -key /etc/squid/serverkey.pem -out /etc/squid/server.csr -passin pass:hunter4 -subj "/C=US/ST=CA/L=San Francisco/O=Example/CN=${ENDPOINT}"

cat > /etc/squid/server.ext <<EOF
authorityKeyIdentifier=keyid,issuer
basicConstraints=CA:FALSE
keyUsage = digitalSignature, nonRepudiation, keyEncipherment, dataEncipherment
subjectAltName = @alt_names

[alt_names]
DNS.1 = ${ENDPOINT}
EOF

openssl x509 -req -in /etc/squid/server.csr -CA /etc/ssl/certs/squidCA.pem -CAkey /etc/squid/ca.key -CAcreateserial -out /etc/squid/server.pem -days 99999 -sha256 -extfile /etc/squid/server.ext -passin pass:hunter4

exec squid --foreground
`}).
			AsService()

		squidHTTPEndpoint, err := squid.Endpoint(ctx, dagger.ServiceEndpointOpts{
			Port:   3128,
			Scheme: "http",
		})
		require.NoError(t, err)

		squidHTTPSEndpoint, err := squid.Endpoint(ctx, dagger.ServiceEndpointOpts{
			Port:   3129,
			Scheme: "https",
		})
		require.NoError(t, err)

		devEngine := devEngineContainer(c, netID, func(ctr *dagger.Container) *dagger.Container {
			return ctr.
				WithServiceBinding("squid", squid).
				WithMountedCache("/etc/ssl/certs", caCertsCacheVolume).
				WithEnvVariable("HTTPS_PROXY", squidHTTPSEndpoint).
				WithEnvVariable("HTTP_PROXY", squidHTTPEndpoint)
		})

		thisRepoPath, err := filepath.Abs("../..")
		require.NoError(t, err)
		thisRepo := c.Host().Directory(thisRepoPath)

		_, err = c.Container().From(golangImage).
			With(goCache(c)).
			WithMountedDirectory("/src", thisRepo).
			WithWorkdir("/src").
			WithServiceBinding("engine", devEngine.AsService()).
			WithMountedCache("/etc/ssl/certs", caCertsCacheVolume).
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
				caCertsDir: c.Host().Directory("/etc/ssl/certs"),
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
				WithExec([]string{"apk", "add", "curl"}).
				WithMountedDirectory("/etc/ssl/certs", f.caCertsDir).
				WithExec([]string{"update-ca-certificates"}).
				WithExec([]string{"curl", "-v", "https://www.example.com"}).
				Stderr(ctx)
			require.NoError(t, err)
			require.Regexp(t, `.*< HTTP/1\.1 200 Connection established.*`, out)
			require.Regexp(t, `.*Establish HTTP proxy tunnel to www.example.com:443.*`, out)
		}},
	)
}
