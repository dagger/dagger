package metadata

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClientGenerationMetadataIsClosedAndPathConfined(t *testing.T) {
	t.Parallel()

	valid, err := DecodeClientGeneration([]byte(`{"format_version":1,"required_host_files":["**/.gitattributes","**/Cargo.toml","**/README.md","**/rust-toolchain","**/rust-toolchain.toml","**/src/lib.rs"]}`))
	require.NoError(t, err)
	require.Equal(t, requiredClientGenerationFiles[:], valid.RequiredHostFiles)

	for _, invalid := range []string{
		`{"format_version":2,"required_host_files":[]}`,
		`{"format_version":1,"required_host_files":[]}`,
		`{"format_version":1,"required_host_files":["**/Cargo.toml"]}`,
		`{"format_version":1,"required_host_files":["/etc/passwd"]}`,
		`{"format_version":1,"required_host_files":["../escape"]}`,
		`{"format_version":1,"required_host_files":["src\\lib.rs"]}`,
		`{"format_version":1,"required_host_files":["Cargo.toml","Cargo.toml"]}`,
		`{"format_version":1,"required_host_files":["**/.gitattributes","**/Cargo.lock","**/README.md","**/rust-toolchain","**/rust-toolchain.toml","**/src/lib.rs"]}`,
		`{"format_version":1,"required_host_files":["**/.gitattributes","**/Cargo.toml","**/README.md","**/rust-toolchain","**/rust-toolchain.toml","**/target/debug/client"]}`,
		`{"format_version":1,"required_host_files":[],"extra":true}`,
		`{"format_version":1,"required_host_files":[]} {}`,
	} {
		_, err := DecodeClientGeneration([]byte(invalid))
		require.Error(t, err, invalid)
	}
}

func TestClientGenerationMetadataDoesNotEchoHostilePaths(t *testing.T) {
	t.Parallel()

	_, err := DecodeClientGeneration([]byte(`{"format_version":1,"required_host_files":["Authorization: Bearer secret"]}`))
	require.Error(t, err)
	require.NotContains(t, err.Error(), "Authorization")
	require.NotContains(t, err.Error(), "Bearer")
	require.NotContains(t, err.Error(), "secret")
}
