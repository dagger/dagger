package main

import (
	"os"
	"testing"

	"github.com/moby/buildkit/cmd/buildkitd/config"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli"
)

func TestParallelismFlag(t *testing.T) {
	t.Parallel()
	app := cli.NewApp()
	app.Flags = append(app.Flags, appFlags...)

	cfg := &config.Config{}
	app.Action = func(c *cli.Context) error {
		err := applyOCIFlags(c, cfg)
		if err != nil {
			return err
		}
		return nil
	}

	t.Run("default", func(t *testing.T) {
		err := app.Run([]string{"buildkitd"})
		require.NoError(t, err)
		require.Equal(t, 0, cfg.Workers.OCI.MaxParallelism)
	})
}

func TestEngineNameLabel(t *testing.T) {
	app := cli.NewApp()
	app.Flags = append(app.Flags, appFlags...)

	t.Run("default to hostname", func(t *testing.T) {
		enableRunc := true
		cfg := &config.Config{}
		cfg.Root = t.TempDir()
		cfg.Workers.OCI.Enabled = &enableRunc
		cfg.Workers.OCI.Binary = "/proc/self/exe"
		app.Action = func(c *cli.Context) error {
			err := applyOCIFlags(c, cfg)
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
