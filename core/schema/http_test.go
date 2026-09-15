package schema

import (
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

func TestParseChecksumArg(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		parsed, err := parseChecksumArg(nil)
		require.NoError(t, err)
		require.Equal(t, digest.Digest(""), parsed)
	})

	t.Run("empty", func(t *testing.T) {
		raw := ""
		parsed, err := parseChecksumArg(&raw)
		require.NoError(t, err)
		require.Equal(t, digest.Digest(""), parsed)
	})

	t.Run("valid", func(t *testing.T) {
		raw := "sha256:8f434346648f6b96df89dda901c5176b10a6d83961c83d4f0df47f85e8a45b2e"
		parsed, err := parseChecksumArg(&raw)
		require.NoError(t, err)
		require.Equal(t, digest.Digest(raw), parsed)
	})

	t.Run("invalid", func(t *testing.T) {
		raw := "not-a-digest"
		_, err := parseChecksumArg(&raw)
		require.ErrorContains(t, err, `invalid checksum "not-a-digest"`)
	})
}
