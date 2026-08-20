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

func TestRunSDKClientList(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, workspace.ConfigFileName), []byte(`
[modules.dagger-go-sdk]
source = "github.com/dagger/go-sdk"

[sdks.go]
module = "dagger-go-sdk"

[sdks.go.claimed]
clients = [
  { path = "lib/z", module = "github.com/acme/api@abc123" },
  { path = "lib/a", module = ".dagger/modules/api" },
]
`), 0o600))

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	require.NoError(t, runSDKClientList(cmd, "go"))

	require.Equal(t, "PATH   MODULE\nlib/a  .dagger/modules/api\nlib/z  github.com/acme/api@abc123\n", buf.String())
}
