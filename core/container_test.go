package core_test

import (
	"context"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/pipeline"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

func TestWithMountedCacheSeen(t *testing.T) {
	t.Parallel()

	ctr, err := core.NewContainer(pipeline.Path{}, specs.Platform{})
	require.NoError(t, err)

	ctr, err = ctr.WithMountedCache(context.Background(), nil,
		"/target",
		core.NewCache("test-seen"),
		nil,
		"",
		"")
	require.NoError(t, err)

	_, ok := core.SeenCacheKeys.Load("test-seen")
	require.True(t, ok)
}
