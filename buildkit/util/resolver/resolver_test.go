package resolver

import (
	"bytes"
	"path"
	"testing"

	"github.com/moby/buildkit/cmd/buildkitd/config"
	"github.com/stretchr/testify/require"
)

func TestNewMirrorRegistryHost(t *testing.T) {
	const testConfig = `
[registry."docker.io"]
mirrors = ["hub.docker.io", "yourmirror.local:5000/proxy.docker.io"]
[registry."quay.io"]
mirrors = ["yourmirror.local:5000/proxy.quay.io"]
[registry."fake.io"]
mirrors = ["https://url/", "https://url/path/"]
`

	tests := map[string]struct {
		description string
		host        string
		path        string
	}{
		"hub.docker.io": {
			description: "docker_io_mirror_without_path",
			host:        "hub.docker.io",
			path:        defaultPath,
		},
		"yourmirror.local:5000/proxy.docker.io": {
			description: "docker_io_mirror_with_path",
			host:        "yourmirror.local:5000",
			path:        path.Join(defaultPath, "proxy.docker.io"),
		},
		"yourmirror.local:5000/proxy.quay.io": {
			description: "docker_quay_mirror_with_path",
			host:        "yourmirror.local:5000",
			path:        path.Join(defaultPath, "proxy.quay.io"),
		},
		"https://url/": {
			description: "docker_fake_mirror_scheme_without_path",
			host:        "url",
			path:        defaultPath,
		},
		"https://url/path/": {
			description: "docker_fake_mirror_scheme_with_path",
			host:        "url",
			path:        path.Join(defaultPath, "path"),
		},
	}

	cfg, err := config.Load(bytes.NewBuffer([]byte(testConfig)))
	require.NoError(t, err)

	require.NotEqual(t, len(cfg.Registries), 0)
	for _, registry := range cfg.Registries {
		require.NotEqual(t, len(registry.Mirrors), 0)
		for _, m := range registry.Mirrors {
			test := tests[m]
			h := newMirrorRegistryHost(m)
			require.NotEqual(t, h, nil)
			require.Equal(t, h.Host, test.host)
			require.Equal(t, h.Path, test.path)
		}
	}
}
