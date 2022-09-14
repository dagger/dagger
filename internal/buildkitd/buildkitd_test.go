//go:build buildkitd

package buildkitd

import (
	"context"
	"fmt"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

func RemoveDaggerBuildkitdImage(ctx context.Context) error {
	cmd := exec.CommandContext(ctx,
		"docker",
		"rmi",
		"-f",
		image,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("remove error: %s\noutput:%s", err, output)
	}

	return nil
}

func StopDaggerBuildkitd(ctx context.Context) error {
	cmd := exec.CommandContext(ctx,
		"docker",
		"stop",
		containerName,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("remove error: %s\noutput:%s", err, output)
	}

	return nil
}

func TestCheckDaggerBuildkitd(t *testing.T) {
	ctx := context.Background()

	provisioner, err := initProvisioner(ctx)
	require.NoError(t, err)

	t.Run("no image", func(t *testing.T) {
		// Remove Dagger container
		err := provisioner.RemoveDaggerBuildkitd(ctx)
		require.NoError(t, err)

		// Remove Dagger image
		err = RemoveDaggerBuildkitdImage(ctx)
		require.NoError(t, err)

		fooVersion := "foo"
		got, err := checkDaggerBuildkitd(ctx, fooVersion)
		require.NoError(t, err)
		require.Equal(t, "docker-container://dagger-buildkitd", got)
	})

	t.Run("update version", func(t *testing.T) {
		newVersion := "bar"
		got, err := checkDaggerBuildkitd(ctx, newVersion)
		require.NoError(t, err)
		require.Equal(t, "docker-container://dagger-buildkitd", got)
	})

	t.Run("stopped dagger-buildkitd", func(t *testing.T) {
		err := StopDaggerBuildkitd(ctx)
		require.NoError(t, err)

		newVersion := "bar"
		got, err := checkDaggerBuildkitd(ctx, newVersion)
		require.NoError(t, err)
		require.Equal(t, "docker-container://dagger-buildkitd", got)
	})
}
