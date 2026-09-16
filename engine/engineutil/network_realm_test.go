package engineutil

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/realm"
)

func TestExecRealmDoesNotTrustInternalTelemetryFlag(t *testing.T) {
	require.Equal(t, realm.Userland, execRealm(nil))
	require.Equal(t, realm.Userland, execRealm(&ExecutionMetadata{Internal: true}))
	require.Equal(t, realm.Daggerland, execRealm(&ExecutionMetadata{DaggerlandRealm: true}))
}
