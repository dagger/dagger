package config

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLoad(t *testing.T) {
	const testConfig = `
root = "/foo/bar"
debug=true
trace=true
insecure-entitlements = ["security.insecure"]

[gc]
enabled=true

[grpc]
address=["buildkit.sock"]
debugAddress="debug.sock"
gid=1234
[grpc.tls]
cert="mycert.pem"

[otel]
socketPath="/tmp/otel-grpc.sock"

[worker.oci]
enabled=true
snapshotter="overlay"
rootless=true
gc=false
gckeepstorage=123456789
[worker.oci.labels]
foo="bar"
"aa.bb.cc"="baz"

[worker.containerd]
namespace="non-default"
platforms=["linux/amd64"]
address="containerd.sock"
[worker.containerd.runtime]
name="exotic"
path="/usr/bin/exotic"
options.foo="bar"
[[worker.containerd.gcpolicy]]
all=true
filters=["foo==bar"]
reservedSpace=20
keepDuration=3600
[[worker.containerd.gcpolicy]]
reservedSpace="40MB"
keepDuration=7200
[[worker.containerd.gcpolicy]]
reservedSpace="20%"
keepDuration="24h"
[[worker.containerd.gcpolicy]]
reservedSpace="10GB"
maxUsedSpace="80%"
minFreeSpace="10%"

[registry."docker.io"]
mirrors=["hub.docker.io"]
http=true
insecure=true
ca=["myca.pem"]
tlsconfigdir=["/etc/buildkitd/myregistry"]
[[registry."docker.io".keypair]]
key="key.pem"
cert="cert.pem"

[dns]
nameservers=["1.1.1.1","8.8.8.8"]
options=["edns0"]
searchDomains=["example.com"]
`

	cfg, err := Load(bytes.NewBuffer([]byte(testConfig)))
	require.NoError(t, err)

	require.Equal(t, "/foo/bar", cfg.Root)
	require.Equal(t, true, cfg.Debug)
	require.Equal(t, true, cfg.Trace)
	require.Equal(t, "security.insecure", cfg.Entitlements[0])

	require.Equal(t, "buildkit.sock", cfg.GRPC.Address[0])
	require.Equal(t, "debug.sock", cfg.GRPC.DebugAddress)
	require.Nil(t, cfg.GRPC.UID)
	require.NotNil(t, cfg.GRPC.GID)
	require.Equal(t, 1234, *cfg.GRPC.GID)
	require.Equal(t, "mycert.pem", cfg.GRPC.TLS.Cert)

	require.Equal(t, "/tmp/otel-grpc.sock", cfg.OTEL.SocketPath)

	require.NotNil(t, cfg.Workers.OCI.Enabled)
	require.Equal(t, int64(123456789), cfg.Workers.OCI.GCKeepStorage.Bytes)
	require.Equal(t, true, *cfg.Workers.OCI.Enabled)
	require.Equal(t, "overlay", cfg.Workers.OCI.Snapshotter)
	require.Equal(t, true, cfg.Workers.OCI.Rootless)
	require.Equal(t, false, *cfg.Workers.OCI.GC)

	require.Equal(t, "bar", cfg.Workers.OCI.Labels["foo"])
	require.Equal(t, "baz", cfg.Workers.OCI.Labels["aa.bb.cc"])

	require.Nil(t, cfg.Workers.Containerd.Enabled)
	require.Equal(t, 1, len(cfg.Workers.Containerd.Platforms))
	require.Equal(t, "containerd.sock", cfg.Workers.Containerd.Address)

	require.Equal(t, 0, len(cfg.Workers.OCI.GCPolicy))
	require.Equal(t, "non-default", cfg.Workers.Containerd.Namespace)
	require.Equal(t, "exotic", cfg.Workers.Containerd.Runtime.Name)
	require.Equal(t, "/usr/bin/exotic", cfg.Workers.Containerd.Runtime.Path)
	require.Equal(t, "bar", cfg.Workers.Containerd.Runtime.Options["foo"])
	require.Equal(t, 4, len(cfg.Workers.Containerd.GCPolicy))

	require.Nil(t, cfg.Workers.Containerd.GC)
	require.Equal(t, true, cfg.Workers.Containerd.GCPolicy[0].All)
	require.Equal(t, 1, len(cfg.Workers.Containerd.GCPolicy[0].Filters))
	require.Equal(t, int64(20), cfg.Workers.Containerd.GCPolicy[0].ReservedSpace.Bytes)
	require.Equal(t, time.Duration(3600), cfg.Workers.Containerd.GCPolicy[0].KeepDuration.Duration/time.Second)

	require.Equal(t, false, cfg.Workers.Containerd.GCPolicy[1].All)
	require.Equal(t, int64(40*1024*1024), cfg.Workers.Containerd.GCPolicy[1].ReservedSpace.Bytes)
	require.Equal(t, time.Duration(7200), cfg.Workers.Containerd.GCPolicy[1].KeepDuration.Duration/time.Second)
	require.Equal(t, 0, len(cfg.Workers.Containerd.GCPolicy[1].Filters))

	require.Equal(t, false, cfg.Workers.Containerd.GCPolicy[2].All)
	require.Equal(t, int64(20), cfg.Workers.Containerd.GCPolicy[2].ReservedSpace.Percentage)
	require.Equal(t, time.Duration(86400), cfg.Workers.Containerd.GCPolicy[2].KeepDuration.Duration/time.Second)

	require.Equal(t, false, cfg.Workers.Containerd.GCPolicy[3].All)
	require.Equal(t, int64(10*1024*1024*1024), cfg.Workers.Containerd.GCPolicy[3].ReservedSpace.Bytes)
	require.Equal(t, int64(80), cfg.Workers.Containerd.GCPolicy[3].MaxUsedSpace.Percentage)
	require.Equal(t, int64(10), cfg.Workers.Containerd.GCPolicy[3].MinFreeSpace.Percentage)

	require.Equal(t, true, *cfg.Registries["docker.io"].PlainHTTP)
	require.Equal(t, true, *cfg.Registries["docker.io"].Insecure)
	require.Equal(t, "hub.docker.io", cfg.Registries["docker.io"].Mirrors[0])
	require.Equal(t, []string{"myca.pem"}, cfg.Registries["docker.io"].RootCAs)
	require.Equal(t, []string{"/etc/buildkitd/myregistry"}, cfg.Registries["docker.io"].TLSConfigDir)
	require.Equal(t, "key.pem", cfg.Registries["docker.io"].KeyPairs[0].Key)
	require.Equal(t, "cert.pem", cfg.Registries["docker.io"].KeyPairs[0].Certificate)

	require.NotNil(t, cfg.DNS)
	require.Equal(t, []string{"1.1.1.1", "8.8.8.8"}, cfg.DNS.Nameservers)
	require.Equal(t, []string{"example.com"}, cfg.DNS.SearchDomains)
	require.Equal(t, []string{"edns0"}, cfg.DNS.Options)
}
