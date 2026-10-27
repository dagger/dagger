package templates

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine"
)

// The cut codegen names by is the engine's.
func TestIdentifierCasingVersion(t *testing.T) {
	require.Equal(t, engine.IdentifierCasingVersion, identifierCasingVersion)
}

// From engine.IdentifierCasingVersion on, Go identifiers are formatted with
// the naming rules, so the GraphQL casing convention's names (commitSha)
// keep the identifiers Go had for the old ones (CommitSHA). Earlier schemas
// keep the golint-style initialisms byte for byte. Schema versions are
// views, as __schemaVersion reports them.
func TestGoNamer(t *testing.T) {
	legacy := newGoNamer(engine.APIViewVersion("v1.0.0-beta.20"))
	for _, version := range []string{"", "v0.21.0", engine.APIViewVersion("v1.0.0-beta.21"), engine.APIViewVersion("v1.0.0-0"), "not-a-version"} {
		require.Equal(t, legacy, newGoNamer(version), version)
	}
	for _, tc := range []struct{ name, want string }{
		{"commitSHA", "CommitSHA"},
		{"commitSha", "CommitSha"},
		{"shortSha", "ShortSha"},
		{"withGPU", "WithGPU"},
		{"asJSON", "AsJSON"},
		{"fooId", "FooID"},
		{"JSONValue", "JSONValue"},
	} {
		require.Equal(t, tc.want, legacy.formatName(tc.name), tc.name)
	}

	for _, declared := range []string{engine.IdentifierCasingVersion, "v1.0.0-beta.23", "v1.0.0", "v1.0.0-dev-0123456789ab"} {
		version := engine.APIViewVersion(declared)
		cased := newGoNamer(version)
		for _, tc := range []struct{ name, want string }{
			// renamed fields keep the Go identifiers of their old names
			{"commitSha", "CommitSHA"},
			{"commitSHA", "CommitSHA"},
			{"treeSha", "TreeSHA"},
			{"previousSha", "PreviousSHA"},
			{"prerequisiteShas", "PrerequisiteSHAs"},
			{"expectedRemoteSha", "ExpectedRemoteSHA"},
			{"withGpu", "WithGPU"},
			{"experimentalWithGpu", "ExperimentalWithGPU"},
			{"experimentalWithAllGpus", "ExperimentalWithAllGPUs"},
			{"asJson", "AsJSON"},
			{"traceUrl", "TraceURL"},
			{"htmlUrl", "HTMLURL"},
			{"htmlRepoUrl", "HTMLRepoURL"},
			{"withSdk", "WithSDK"},
			{"withMcpServer", "WithMCPServer"},
			{"withVcsGeneratedPaths", "WithVCSGeneratedPaths"},
			{"introspectionSchemaJson", "IntrospectionSchemaJSON"},
			{"insecureSkipTlsVerify", "InsecureSkipTLSVerify"},
			// type names are already conventional
			{"JSONValue", "JSONValue"},
			{"LLMTokenUsage", "LLMTokenUsage"},
			{"HTTPState", "HTTPState"},
			{"ContainerID", "ContainerID"},
			// internal names keep the legacy formatting
			{"__typename", legacyFormatName("__typename")},
		} {
			require.Equal(t, tc.want, cased.formatName(tc.name), "%s %s", version, tc.name)
		}
	}
}
