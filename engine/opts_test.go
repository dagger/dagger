package engine

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClientMetadataAppendToHTTPHeadersNormalizesLegacyWorkspaceModuleLoading(t *testing.T) {
	t.Parallel()

	headers := (&ClientMetadata{
		ClientID:             "client",
		ClientVersion:        "v1.2.3",
		ClientSecretToken:    "secret",
		LoadWorkspaceModules: false,
		SkipWorkspaceModules: true,
	}).AppendToHTTPHeaders(http.Header{})

	md, err := ClientMetadataFromHTTPHeaders(headers)
	require.NoError(t, err)
	require.False(t, md.LoadWorkspaceModules)
	require.False(t, md.SkipWorkspaceModules)
}

func TestClientMetadataFromHTTPHeadersRejectsConflictingWorkspaceModuleLoading(t *testing.T) {
	t.Parallel()

	headers := http.Header{}
	encoded, err := json.Marshal(ClientMetadata{
		ClientID:             "client",
		ClientVersion:        "v1.2.3",
		ClientSecretToken:    "secret",
		LoadWorkspaceModules: true,
		SkipWorkspaceModules: true,
	})
	require.NoError(t, err)
	headers.Set(ClientMetadataMetaKey, base64.StdEncoding.EncodeToString(encoded))

	_, err = ClientMetadataFromHTTPHeaders(headers)
	require.ErrorContains(t, err, "mutually exclusive")
}
