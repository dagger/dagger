package daggercmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/core/workspace"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestRunSDKClientClaimed(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, workspace.ConfigFileName), []byte(`
[modules.dagger-go-sdk]
source = "github.com/dagger/go-sdk"

[modules.dagger-go-sdk.as-sdk]
name = "go"

[[modules.dagger-go-sdk.as-sdk.clients]]
path = "lib/z"
module = "github.com/acme/api"
pin = "abc123"

[[modules.dagger-go-sdk.as-sdk.clients]]
path = "lib/a"
module = ".dagger/modules/api"
`), 0o600))

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	require.NoError(t, runSDKClientClaimed(cmd, "go"))

	out := buf.String()
	require.Contains(t, out, "PATH")
	require.Contains(t, out, "MODULE")
	require.Contains(t, out, "PIN")
	require.Less(t, bytes.Index([]byte(out), []byte("lib/a")), bytes.Index([]byte(out), []byte("lib/z")))
	require.Contains(t, out, "github.com/acme/api")
	require.Contains(t, out, "abc123")
}

func TestWorkspaceClients(t *testing.T) {
	clients := workspaceClients(configuredSDK{entry: workspace.ModuleEntry{
		AsSDK: &workspace.ModuleAsSDK{Clients: []workspace.SDKManagedClient{
			{Path: "lib/go", Module: ".dagger/modules/api", Pin: "abc123"},
		}},
	}})

	require.Equal(t, []workspaceClient{{
		path: "lib/go", module: ".dagger/modules/api", pin: "abc123",
	}}, clients)
}
