package main

import (
	"os"
	"runtime"
	"testing"

	"github.com/moby/buildkit/cmd/buildkitd/config"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli"
)

func TestParallelismFlag(t *testing.T) {
	t.Parallel()
	app := cli.NewApp()
	addFlags(app)

	cfg := &config.Config{}
	app.Action = func(c *cli.Context) error {
		err := applyMainFlags(c, cfg)
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
	t.Run("int", func(t *testing.T) {
		err := app.Run([]string{"buildkitd", "--oci-max-parallelism", "5"})
		require.NoError(t, err)
		require.Equal(t, 5, cfg.Workers.OCI.MaxParallelism)
	})
	t.Run("num-cpu", func(t *testing.T) {
		err := app.Run([]string{"buildkitd", "--oci-max-parallelism", "num-cpu"})
		require.NoError(t, err)
		require.Equal(t, runtime.NumCPU(), cfg.Workers.OCI.MaxParallelism)
	})
	t.Run("invalid", func(t *testing.T) {
		err := app.Run([]string{"buildkitd", "--oci-max-parallelism", "foo"})
		require.Error(t, err)
	})
}

func TestEngineNameLabel(t *testing.T) {
	app := cli.NewApp()
	addFlags(app)

	t.Run("default to hostname", func(t *testing.T) {
		enableRunc := true
		cfg := &config.Config{}
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
