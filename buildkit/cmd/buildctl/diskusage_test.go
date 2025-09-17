package main

import (
	"testing"

	"github.com/dagger/dagger/buildkit/util/testutil/integration"
	"github.com/stretchr/testify/require"
)

func testDiskUsage(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	cmd := sb.Cmd("du")
	err := cmd.Run()
	require.NoError(t, err)
}
