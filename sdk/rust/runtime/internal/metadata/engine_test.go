package metadata

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCanonicalJSONUsesTheRustControlSpelling(t *testing.T) {
	t.Parallel()

	encoded, err := CanonicalJSON(map[string]any{
		"z": 1,
		"a": map[string]any{"z": false, "a": true},
	})
	require.NoError(t, err)
	require.Equal(t, "{\n  \"a\": {\n    \"a\": true,\n    \"z\": false\n  },\n  \"z\": 1\n}\n", string(encoded))
}

func TestProperty06InitializationWorkingDirectoryRebasesToOneModuleIdentity(t *testing.T) {
	t.Parallel()

	for seed := 0; seed < 256; seed++ {
		segments := 1 + seed%8
		candidate := ""
		for index := 0; index < segments; index++ {
			if candidate != "" {
				candidate += "/"
			}
			candidate += fmt.Sprintf("nested-%d", index)
		}
		rebased, err := RebaseOperationPath(candidate)
		require.NoError(t, err)
		require.Equal(t, "workspace/"+candidate, rebased)
	}

	root, err := RebaseOperationPath(".")
	require.NoError(t, err)
	require.Equal(t, "workspace", root)
	for _, invalid := range []string{"../escape", "/absolute", "alias\\path", "a/../b", "a//b"} {
		_, err := RebaseOperationPath(invalid)
		require.Error(t, err, invalid)
	}
}

func TestOperationVCSPathsRemainRelativeToCallerContext(t *testing.T) {
	t.Parallel()

	paths, err := StripOperationRoot([]string{
		"workspace/module/.dagger/rust/operation-manifest.json",
		"workspace/module/src/dagger_generated/client.rs",
	})
	require.NoError(t, err)
	require.Equal(t, []string{
		"module/.dagger/rust/operation-manifest.json",
		"module/src/dagger_generated/client.rs",
	}, paths)

	_, err = StripOperationRoot([]string{"foreign/source.rs"})
	require.Error(t, err)
}

func TestEngineDiagnosticDecoderAcceptsOnlyBoundedCanonicalSafeContent(t *testing.T) {
	t.Parallel()

	encoded, err := CanonicalJSON(EngineDiagnostic{
		Code:       "GENERATED_STALE",
		Coordinate: "workspace/module/.dagger/rust/operation-manifest.json",
		Message:    "generated ownership manifest differs from runtime inputs; run `dagger generate`",
		Causes:     []EngineDiagnostic{},
	})
	require.NoError(t, err)
	diagnostic, err := DecodeEngineDiagnostic(encoded)
	require.NoError(t, err)
	require.Equal(t, "GENERATED_STALE", diagnostic.Code)

	unsafe, err := CanonicalJSON(EngineDiagnostic{
		Code: "BUILD_FAILED", Message: "Bearer secret", Causes: []EngineDiagnostic{},
	})
	require.NoError(t, err)
	_, err = DecodeEngineDiagnostic(unsafe)
	require.Error(t, err)
}

func TestRuntimePlanAllowsOnlyTheVerifierAuthoredCargoVector(t *testing.T) {
	t.Parallel()

	plan := map[string]any{
		"binary_relative_path": "release/dagger-module",
		"cargo_args": []string{
			"build", "--manifest-path", "workspace/Cargo.toml",
			"--package", "example", "--bin", "dagger-module", "--release", "--locked",
			"--target", "x86_64-unknown-linux-gnu", "--target-dir", "/var/lib/dagger/rust/target",
		},
		"format_version":   1,
		"manifest":         map[string]any{},
		"mode":             "checked-generated",
		"project":          map[string]any{},
		"provenance_input": map[string]any{},
	}
	encoded, err := CanonicalJSON(plan)
	require.NoError(t, err)
	decoded, err := DecodeRuntimeBuildPlan(encoded)
	require.NoError(t, err)
	require.Equal(t, "dagger-module", decoded.CargoArgs[6])

	plan["cargo_args"].([]string)[6] = "caller-selected"
	encoded, err = CanonicalJSON(plan)
	require.NoError(t, err)
	_, err = DecodeRuntimeBuildPlan(encoded)
	require.Error(t, err)
}

func TestModuleSourceDigestIsDomainSeparated(t *testing.T) {
	t.Parallel()

	digest := DigestModuleSource("xxh3:opaque")
	require.True(t, strings.HasPrefix(digest, "sha256:"))
	require.Len(t, digest, 71)
	require.NotEqual(t, DigestBytes([]byte("xxh3:opaque")), digest)
}

func TestModuleSourceFileDigestIsCanonicalAndSensitive(t *testing.T) {
	t.Parallel()

	first, err := DigestModuleSourceFiles([]ModuleSourceFile{
		{Path: "src/lib.rs", Digest: "xxh3:lib"},
		{Path: "Cargo.toml", Digest: "xxh3:manifest"},
	})
	require.NoError(t, err)
	permuted, err := DigestModuleSourceFiles([]ModuleSourceFile{
		{Path: "Cargo.toml", Digest: "xxh3:manifest"},
		{Path: "src/lib.rs", Digest: "xxh3:lib"},
	})
	require.NoError(t, err)
	require.Equal(t, first, permuted)

	changed, err := DigestModuleSourceFiles([]ModuleSourceFile{
		{Path: "Cargo.toml", Digest: "xxh3:changed"},
		{Path: "src/lib.rs", Digest: "xxh3:lib"},
	})
	require.NoError(t, err)
	require.NotEqual(t, first, changed)

	_, err = DigestModuleSourceFiles([]ModuleSourceFile{
		{Path: "Cargo.toml", Digest: "xxh3:first"},
		{Path: "Cargo.toml", Digest: "xxh3:second"},
	})
	require.Error(t, err)
}
