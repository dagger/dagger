package drivers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/dagger/dagger/engine/distconsts"
	"github.com/stretchr/testify/require"
)

func TestIsExpectedIncusDockerRemote(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		remote incusRemote
		want   bool
	}{
		{
			name: "matching",
			remote: incusRemote{
				Addr:     incusDockerRemoteAddr,
				Protocol: incusDockerRemoteProtocol,
			},
			want: true,
		},
		{
			name: "matching short url",
			remote: incusRemote{
				Addr:     "docker.io",
				Protocol: incusDockerRemoteProtocol,
			},
			want: true,
		},
		{
			name: "protocol mismatch",
			remote: incusRemote{
				Protocol: "simplestreams",
				Addr:     incusDockerRemoteAddr,
			},
			want: false,
		},
		{
			name: "url mismatch",
			remote: incusRemote{
				Protocol: incusDockerRemoteProtocol,
				Addr:     "https://example.com",
			},
			want: false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isExpectedIncusDockerRemote(tc.remote); got != tc.want {
				t.Fatalf("isExpectedIncusDockerRemote() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIncusRemoteListJSON(t *testing.T) {
	t.Parallel()

	remotes, err := func() (map[string]incusRemote, error) {
		var parsed map[string]incusRemote
		err := json.Unmarshal([]byte(`{
			"docker": {
				"addr": "https://docker.io",
				"protocol": "oci"
			},
			"images": {
				"addr": "https://images.linuxcontainers.org",
				"protocol": "simplestreams"
			}
		}`), &parsed)
		return parsed, err
	}()
	require.NoError(t, err)
	require.Equal(t, incusRemote{Addr: "https://docker.io", Protocol: incusDockerRemoteProtocol}, remotes["docker"])
	require.Equal(t, incusRemote{Addr: "https://images.linuxcontainers.org", Protocol: "simplestreams"}, remotes["images"])
}

func TestIncusImageAlias(t *testing.T) {
	t.Parallel()

	image := "registry.dagger.io/engine:v0.20.6"
	sum := sha256.Sum256([]byte(image))
	want := "dagger-" + hex.EncodeToString(sum[:8])

	require.Equal(t, want, incusImageAlias(image))
	require.NotEqual(t, incusImageAlias(image), incusImageAlias(image+"-other"))
}

func TestIncusRemoteImageRef(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		image           string
		wantSource      string
		wantNeedsRemote bool
	}{
		{
			name:            "bare image gets docker remote",
			image:           "registry.dagger.io/engine:dev",
			wantSource:      "docker:registry.dagger.io/engine:dev",
			wantNeedsRemote: true,
		},
		{
			name:            "existing scheme is preserved",
			image:           "local:test",
			wantSource:      "local:test",
			wantNeedsRemote: false,
		},
		{
			name:            "docker scheme is preserved",
			image:           "docker:alpine:latest",
			wantSource:      "docker:alpine:latest",
			wantNeedsRemote: false,
		},
		{
			name:            "images scheme is preserved",
			image:           "images:ubuntu/25.10",
			wantSource:      "images:ubuntu/25.10",
			wantNeedsRemote: false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gotSource, gotNeedsRemote := incusRemoteImageRef(tc.image)
			require.Equal(t, tc.wantSource, gotSource)
			require.Equal(t, tc.wantNeedsRemote, gotNeedsRemote)
		})
	}
}

func TestIncusOutputClassifiers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		output   string
		already  bool
		notFound bool
	}{
		{
			name:     "already exists",
			output:   "Error: instance already exists",
			already:  true,
			notFound: false,
		},
		{
			name:     "not found",
			output:   "Error: instance not found in project default",
			already:  false,
			notFound: true,
		},
		{
			name:     "unrelated output",
			output:   "permission denied",
			already:  false,
			notFound: false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.already, isIncusAlreadyExistsOutput(tc.output))
			require.Equal(t, tc.notFound, isIncusNotFoundOutput(tc.output))
		})
	}
}

func TestIncusDaemonUnavailableOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		output string
		want   bool
	}{
		{
			name:   "connection refused",
			output: "Error: Get \"https://127.0.0.1:8443/1.0\": Unable to connect to: 127.0.0.1:8443 ([dial tcp 127.0.0.1:8443: connect: connection refused])",
			want:   true,
		},
		{
			name:   "launchd message",
			output: "apiserver is not running and not registered with launchd",
			want:   true,
		},
		{
			name:   "permission denied",
			output: "permission denied",
			want:   true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, isIncusDaemonUnavailableOutput(tc.output))
		})
	}
}

func TestIncusLaunchArgs(t *testing.T) {
	t.Parallel()

	opts := runOpts{
		image:      "registry.dagger.io/engine:dev",
		env:        []string{"FOO=bar", "EMPTY"},
		ports:      []string{"8080:80", "9000"},
		privileged: true,
		cpus:       "4",
		memory:     "8G",
		args:       []string{"--debug"},
	}

	args, err := incusLaunchArgs("dagger-engine", opts, "/var/lib/dagger/incus/volumes/dagger-engine", "/home/test/.config/dagger", true)
	require.NoError(t, err)
	require.Equal(t, []string{
		"launch",
		"local:" + incusImageAlias(opts.image),
		"dagger-engine",
		"-c", "security.nesting=true",
		"-c", "security.privileged=true",
		"-c", "limits.cpu=4",
		"-c", "limits.memory=8G",
		"-d", "dagger-state,type=disk,source=/var/lib/dagger/incus/volumes/dagger-engine,path=" + distconsts.EngineDefaultStateDir,
		"-d", "dagger-config,type=disk,source=/home/test/.config/dagger,path=/root/.config/dagger",
		"-c", "environment.FOO=bar",
		"-c", "environment.EMPTY=",
		"-d", "dagger-port-8080-80-tcp,type=proxy,listen=tcp:127.0.0.1:8080,connect=tcp:127.0.0.1:80",
		"-d", "dagger-port-9000-9000-tcp,type=proxy,listen=tcp:127.0.0.1:9000,connect=tcp:127.0.0.1:9000",
		"--",
		"--debug",
	}, args)
}

func TestParseIncusPortMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		input         string
		host, backend string
		protocol      string
	}{
		{
			name:     "default tcp",
			input:    "8080:80",
			host:     "8080",
			backend:  "80",
			protocol: "tcp",
		},
		{
			name:     "udp protocol",
			input:    "8080:80/udp",
			host:     "8080",
			backend:  "80",
			protocol: "udp",
		},
		{
			name:     "single port with protocol",
			input:    "9000/tcp",
			host:     "9000",
			backend:  "9000",
			protocol: "tcp",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			host, backend, protocol, err := parseIncusPortMapping(tc.input)
			require.NoError(t, err)
			require.Equal(t, tc.host, host)
			require.Equal(t, tc.backend, backend)
			require.Equal(t, tc.protocol, protocol)
		})
	}
}

func TestParseIncusPortMappingRejectsUnknownProtocol(t *testing.T) {
	t.Parallel()

	host, backend, protocol, err := parseIncusPortMapping("8080:80/sctp")
	require.Error(t, err)
	require.Empty(t, host)
	require.Empty(t, backend)
	require.Empty(t, protocol)
}

func TestIncusLaunchArgsRejectsGPU(t *testing.T) {
	t.Parallel()

	_, err := incusLaunchArgs("dagger-engine", runOpts{image: "registry.dagger.io/engine:dev", gpus: true}, "/state", "", false)
	require.EqualError(t, err, "incus backend does not currently support GPU passthrough")
}
