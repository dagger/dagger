package drivers

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine"
)

var allProvisionAccess = []string{"list pods", "create pods", "create pods/exec"}

func TestKubectlAvailable(t *testing.T) {
	t.Run("reachable and permitted", func(t *testing.T) {
		argsLog := fakeKubectl(t, allProvisionAccess, false)

		ok, err := kubectlImageDriver.Available(t.Context(), parse(t, "image+kubectl://engine?context=prod&namespace=dagger"))
		require.NoError(t, err)
		require.True(t, ok)

		args, err := os.ReadFile(argsLog)
		require.NoError(t, err)
		require.Contains(t, string(args), "--context=prod --namespace=dagger")
	})

	t.Run("kubeconfig selects the cluster", func(t *testing.T) {
		argsLog := fakeKubectl(t, nil, false)

		ok, err := kubectlImageDriver.Available(t.Context(), parse(t, "image+kubectl://engine?kubeconfig=/tmp/other.yaml&context=prod"))
		require.False(t, ok)
		var unavailable *UnavailableError
		require.ErrorAs(t, err, &unavailable)
		require.Contains(t, unavailable.Reason, `prod of kubeconfig "/tmp/other.yaml"`)

		args, err := os.ReadFile(argsLog)
		require.NoError(t, err)
		require.Contains(t, string(args), "--kubeconfig=/tmp/other.yaml --context=prod auth can-i")
	})

	t.Run("missing RBAC says what is missing", func(t *testing.T) {
		fakeKubectl(t, []string{"list pods"}, false)

		ok, err := kubectlImageDriver.Available(t.Context(), parse(t, "image+kubectl://engine?namespace=locked"))
		require.False(t, ok)
		var unavailable *UnavailableError
		require.ErrorAs(t, err, &unavailable)
		require.Contains(t, unavailable.Reason, "not permitted to create pods")
		require.Contains(t, unavailable.Reason, `namespace "locked"`)
	})

	t.Run("unreachable cluster", func(t *testing.T) {
		fakeKubectl(t, nil, true)

		ok, err := kubectlImageDriver.Available(t.Context(), parse(t, "image+kubectl://engine?context=dead"))
		require.False(t, ok)
		var unavailable *UnavailableError
		require.ErrorAs(t, err, &unavailable)
		require.Contains(t, unavailable.Reason, "not reachable")
		require.Contains(t, unavailable.Reason, "dead")
		require.Contains(t, unavailable.Reason, "connection refused")
	})

	t.Run("kubectl not installed", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())

		ok, err := kubectlImageDriver.Available(t.Context(), parse(t, "image://engine"))
		require.NoError(t, err)
		require.False(t, ok)
	})
}

func TestKubectlImageTarget(t *testing.T) {
	orig := engine.Tag
	t.Cleanup(func() { engine.Tag = orig })
	engine.Tag = "v1.2.3"
	t.Setenv("DAGGER_LEAVE_OLD_ENGINE", "false")

	target, err := parseImageTarget(parse(t, "image+kubectl://"), "pod")
	require.NoError(t, err)
	require.Equal(t, &imageTarget{
		Ref:     "registry.dagger.io/engine:v1.2.3",
		Name:    "dagger-engine-v1.2.3",
		Cleanup: true,
	}, target)

	target, err = parseImageTarget(parse(t, "image+kubectl://my-mirror.io/dagger-engine?pod=engine&env=A=1"), "pod")
	require.NoError(t, err)
	require.Equal(t, &imageTarget{
		Ref:     "my-mirror.io/dagger-engine:v1.2.3",
		Name:    "engine",
		Cleanup: true,
		Env:     []string{"A=1"},
	}, target)
}

// fakeKubectl puts a kubectl on PATH that answers `auth can-i VERB RESOURCE`
// from allowed ("VERB RESOURCE" entries), fails as an unreachable cluster if
// unreachable is set, and records its arguments in the returned file.
func fakeKubectl(t *testing.T, allowed []string, unreachable bool) (argsLog string) {
	t.Helper()

	dir := t.TempDir()
	argsLog = filepath.Join(dir, "args.log")

	script := "#!/bin/sh\necho \"$@\" >> " + argsLog + "\n"
	if unreachable {
		script += "echo 'Unable to connect to the server: dial tcp 127.0.0.1:1: connect: connection refused' >&2\nexit 1\n"
	} else {
		script += `case "$*" in
`
		for _, a := range allowed {
			script += "  *\"auth can-i " + a + "\"*) echo yes; exit 0;;\n"
		}
		script += `  *"auth can-i"*) echo no; exit 1;;
  *"get pod"*) case "$*" in *missing*) echo 'Error from server (NotFound)' >&2; exit 1;; *) echo pod/x; exit 0;; esac;;
esac
exit 0
`
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "kubectl"), []byte(script), 0o755))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return argsLog
}

func parse(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	return u
}
