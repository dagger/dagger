package main

import (
	"runtime"
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
