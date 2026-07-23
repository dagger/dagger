package cloud

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseGraphQLErrors(t *testing.T) {
	// A failed operation: errors with null (or absent) data.
	require.Error(t, parseGraphQLErrors("op",
		[]byte(`{"data":null,"errors":[{"message":"boom"}]}`)))
	require.Error(t, parseGraphQLErrors("op",
		[]byte(`{"errors":[{"message":"boom"}]}`)))

	// Partial success: field errors alongside usable data must not abort the
	// stream -- the data still flows to the callback.
	require.NoError(t, parseGraphQLErrors("op",
		[]byte(`{"data":{"spansUpdated":[]},"errors":[{"message":"field error"}]}`)))

	// Normal payloads and junk are not GraphQL errors.
	require.NoError(t, parseGraphQLErrors("op", []byte(`{"data":{"x":1}}`)))
	require.NoError(t, parseGraphQLErrors("op", []byte(`not json`)))
}
