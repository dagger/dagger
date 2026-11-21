package drivers

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine"
)

func TestDefaultImageRef(t *testing.T) {
	orig := engine.Tag
	t.Cleanup(func() { engine.Tag = orig })

	engine.Tag = "v1.2.3"
	// --engine=image://
	require.Equal(t, "registry.dagger.io/engine:v1.2.3", defaultImageRef(""))
	// --engine=image://my-mirror.io/dagger-engine
	require.Equal(t, "my-mirror.io/dagger-engine:v1.2.3", defaultImageRef("my-mirror.io/dagger-engine"))
	require.Equal(t, "registry.dagger.io/engine:v1.2.3", defaultImageRef("registry.dagger.io/engine"))
	require.Equal(t, "localhost:5000/engine:v1.2.3", defaultImageRef("localhost:5000/engine"))
	require.Equal(t, "registry.dagger.io/engine:v0.21.7", defaultImageRef("registry.dagger.io/engine:v0.21.7"))
	require.Equal(t, "registry.dagger.io/engine:latest", defaultImageRef("registry.dagger.io/engine:latest"))
	require.Equal(t, "example.com/engine@sha256:abc", defaultImageRef("example.com/engine@sha256:abc"))

	id, err := resolveImageID(defaultImageRef(""))
	require.NoError(t, err)
	require.Equal(t, "v1.2.3", id)

	// naive dev builds have no tag
	engine.Tag = ""
	require.Equal(t, "registry.dagger.io/engine", defaultImageRef(""))
	require.Equal(t, "my-mirror.io/dagger-engine", defaultImageRef("my-mirror.io/dagger-engine"))
	require.Equal(t, "example.com/x:1", defaultImageRef("example.com/x:1"))

	id, err = resolveImageID(defaultImageRef(""))
	require.NoError(t, err)
	require.Equal(t, "latest", id)
}
