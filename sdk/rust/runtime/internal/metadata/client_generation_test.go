package metadata

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClientGenerationMetadataIsClosedAndPathConfined(t *testing.T) {
	t.Parallel()

	valid, err := DecodeClientGeneration([]byte(`{"format_version":1,"required_host_files":["Cargo.toml","src/lib.rs"]}`))
	require.NoError(t, err)
	require.Equal(t, []string{"Cargo.toml", "src/lib.rs"}, valid.RequiredHostFiles)

	for _, invalid := range []string{
		`{"format_version":2,"required_host_files":[]}`,
		`{"format_version":1,"required_host_files":["/etc/passwd"]}`,
		`{"format_version":1,"required_host_files":["../escape"]}`,
		`{"format_version":1,"required_host_files":["src\\lib.rs"]}`,
		`{"format_version":1,"required_host_files":["Cargo.toml","Cargo.toml"]}`,
		`{"format_version":1,"required_host_files":[],"extra":true}`,
	} {
		_, err := DecodeClientGeneration([]byte(invalid))
		require.Error(t, err, invalid)
	}
}
