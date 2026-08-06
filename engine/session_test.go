package engine

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSessionProtocolIDs(t *testing.T) {
	t.Parallel()

	sessionID, err := GenerateSessionID()
	require.NoError(t, err)
	require.NoError(t, ValidateSessionID(sessionID))
	require.Len(t, sessionID, len(SessionIDPrefix)+SessionIDEncodedLength)

	queryID, err := GenerateQueryID()
	require.NoError(t, err)
	require.NoError(t, ValidateQueryID(queryID))

	attachmentID, err := GenerateAttachmentID()
	require.NoError(t, err)
	require.NoError(t, ValidateAttachmentID(attachmentID))

	for name, id := range map[string]string{
		"wrong prefix":       "query_" + strings.Repeat("a", 26),
		"too short":          SessionIDPrefix + strings.Repeat("a", 25),
		"too long":           SessionIDPrefix + strings.Repeat("a", 27),
		"uppercase":          SessionIDPrefix + strings.Repeat("A", 26),
		"invalid base32":     SessionIDPrefix + strings.Repeat("0", 26),
		"path traversal":     SessionIDPrefix + "../../" + strings.Repeat("a", 20),
		"embedded separator": SessionIDPrefix + strings.Repeat("a", 12) + "/" + strings.Repeat("a", 13),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Error(t, ValidateSessionID(id))
		})
	}
}
