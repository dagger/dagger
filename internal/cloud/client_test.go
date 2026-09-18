package cloud

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRequestedEngineImage(t *testing.T) {
	require.Equal(t, "registry.dagger.io/engine:v1.2.3", requestedEngineImage("v1.2.3"))

	t.Setenv("_EXPERIMENTAL_DAGGER_CLOUD_ENGINE_IMAGE", "example.com/engine:demo")
	require.Equal(t, "example.com/engine:demo", requestedEngineImage("v1.2.3"))
}
