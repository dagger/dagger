package telemetry

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCloudEmitStatusForCloudToken(t *testing.T) {
	t.Setenv("DAGGER_CLOUD_TOKEN", "dag_acme_secret")
	require.Equal(t, CloudEmitStatus{
		Emitting:   true,
		Credential: "DAGGER_CLOUD_TOKEN",
		Org:        "acme",
	}, CloudEmitStatusFor(t.Context()))

	// A token in another format names no org, but still sends telemetry.
	t.Setenv("DAGGER_CLOUD_TOKEN", "legacy-token")
	require.Equal(t, CloudEmitStatus{
		Emitting:   true,
		Credential: "DAGGER_CLOUD_TOKEN",
	}, CloudEmitStatusFor(t.Context()))
}

func TestCloudEmitStatusForInvalidCloudURL(t *testing.T) {
	t.Setenv("DAGGER_CLOUD_TOKEN", "dag_acme_secret")
	t.Setenv("DAGGER_CLOUD_URL", "://bad")
	status := CloudEmitStatusFor(t.Context())
	require.False(t, status.Emitting)
	require.ErrorIs(t, status.Err, ErrInvalidCloudURL)
}
