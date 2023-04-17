package main

import (
	"os"
	"runtime"
	"testing"

	"github.com/moby/buildkit/cmd/buildkitd/config"
	"github.com/moby/buildkit/session"
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

func TestEngineNameLabel(t *testing.T) {
	app := cli.NewApp()
	app.Flags = append(app.Flags, appFlags...)

	t.Run("default", func(t *testing.T) {
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
			sessionManager, err := session.NewManager()
			if err != nil {
				return err
			}

			wc, err := newWorkerController(c, workerInitializerOpt{
				config:         cfg,
				sessionManager: sessionManager,
			})
			if err != nil {
				return err
			}
			w, err := wc.GetDefault()
			if err != nil {
				return err
			}
			hostname, err := os.Hostname()
			if err != nil {
				return err
			}
			require.Equal(t, hostname, w.Labels()["engineName"])
			return nil
		}

		err := app.Run([]string{"buildkitd"})
		require.NoError(t, err)
	})
	t.Run("env", func(t *testing.T) {
		existingEnv, ok := os.LookupEnv("_EXPERIMENTAL_DAGGER_ENGINE_NAME")
		if ok {
			defer os.Setenv("_EXPERIMENTAL_DAGGER_ENGINE_NAME", existingEnv)
		} else {
			defer os.Unsetenv("_EXPERIMENTAL_DAGGER_ENGINE_NAME")
		}
		engineName := "wacky-engine"
		os.Setenv("_EXPERIMENTAL_DAGGER_ENGINE_NAME", engineName)
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
			sessionManager, err := session.NewManager()
			if err != nil {
				return err
			}

			wc, err := newWorkerController(c, workerInitializerOpt{
				config:         cfg,
				sessionManager: sessionManager,
			})
			if err != nil {
				return err
			}
			w, err := wc.GetDefault()
			if err != nil {
				return err
			}
			require.Equal(t, engineName, w.Labels()["engineName"])
			return nil
		}

		err := app.Run([]string{"buildkitd"})
		require.NoError(t, err)
	})
}
