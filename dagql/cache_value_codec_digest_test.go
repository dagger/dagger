package dagql

import (
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// validateLayerDigest answers every digest as digest.Digest.Validate does:
// valid digests of each SHA-2 algorithm, and digests wrong in each way it
// tells apart, with the same error.
func TestValidateLayerDigestIsDigestValidate(t *testing.T) {
	t.Parallel()
	hex := func(n int) string { return strings.Repeat("0123456789abcdef", n/16+1)[:n] }
	for _, d := range []digest.Digest{
		digest.FromString("a layer"),
		digest.SHA384.FromString("a layer"),
		digest.SHA512.FromString("a layer"),
		digest.Digest("sha256:" + hex(64)),
		digest.Digest("sha256:" + strings.ToUpper(hex(64))),
		digest.Digest("sha256:" + hex(63)),
		digest.Digest("sha256:" + hex(65)),
		digest.Digest("sha256:" + hex(63) + "g"),
		digest.Digest("sha256:" + hex(63) + ":"),
		digest.Digest("sha512:" + hex(64)),
		digest.Digest("sha256:"),
		digest.Digest("sha256"),
		digest.Digest(":" + hex(64)),
		digest.Digest(""),
		digest.Digest("md5:" + hex(32)),
		digest.Digest("Sha256:" + hex(64)),
		digest.Digest("sha256:" + hex(64) + "\n"),
	} {
		require.Equal(t, d.Validate(), validateLayerDigest(d), "%q", d)
	}
}
