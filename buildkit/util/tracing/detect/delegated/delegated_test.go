package delegated_test

import (
	"testing"

	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/util/tracing/detect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectPreservesDelegateInterface(t *testing.T) {
	exp, _, err := detect.Exporter()
	require.NoError(t, err)

	_, ok := exp.(client.TracerDelegate)
	assert.True(t, ok, "delegated tracer expected to fulfill client.TracerDelegate interface")
}
