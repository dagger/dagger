package main

import (
	"os"
	"testing"

	bkconfig "github.com/dagger/dagger/internal/buildkit/cmd/buildkitd/config"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli"
)

func TestEngineNameLabel(t *testing.T) {
	app := cli.NewApp()
	addFlags(app)

	t.Run("default to hostname", func(t *testing.T) {
		enableRunc := true
		cfg := &bkconfig.Config{}
		cfg.Root = t.TempDir()
		cfg.Workers.OCI.Enabled = &enableRunc
		cfg.Workers.OCI.Binary = "/proc/self/exe"
		app.Action = func(c *cli.Context) error {
			err := applyMainFlags(c, cfg)
			if err != nil {
				return err
			}
			hostname, err := os.Hostname()
			if err != nil {
				return err
			}
			require.Equal(t, hostname, engineName)
			return nil
		}

		err := app.Run([]string{"buildkitd"})
		require.NoError(t, err)
	})
}
