package schema

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/mod/semver"

	"github.com/dagger/dagger/cmd/codegen/introspection/sdl"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/internal/version"
)

// The test-split test-released-schema check copies this file into the release
// tag's tree, so it may use only helpers that exist there: it repeats
// TestBaseSchemaAllowlist's setup on purpose instead of sharing a helper.
func TestWriteReleasedViewSDL(t *testing.T) {
	out := os.Getenv("RELEASED_SCHEMA_OUT")
	if out == "" {
		t.Skip("run by the test-split test-released-schema check")
	}
	released := os.Getenv("RELEASED_SCHEMA_VERSION")
	if released == "" {
		next := version.Version(version.WithV())
		for _, tag := range strings.Fields(os.Getenv("RELEASED_SCHEMA_TAGS")) {
			if semver.IsValid(tag) && semver.Compare(tag, next) < 0 && semver.Compare(tag, released) > 0 {
				released = tag
			}
		}
		require.NotEmpty(t, released, "no release tag below %s in RELEASED_SCHEMA_TAGS", next)
	}

	ctx := context.Background()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = dagql.ContextWithCache(ctx, cache)
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID:  "released-schema-client",
		SessionID: "released-schema-session",
	})
	srv := &currentTypeDefsTestServer{}
	coreSchemaBase, err := NewCoreSchemaBase(ctx, srv)
	require.NoError(t, err)
	view := call.View(engine.APIViewVersion(released))
	dag, err := coreSchemaBase.Fork(ctx, core.NewRoot(srv), view)
	require.NoError(t, err)
	schemaJSON, err := getSchemaJSON(nil, nil, view, dag)
	require.NoError(t, err)

	var got strings.Builder
	sdl.Format(&got, decodeSchemaResponse(t, schemaJSON).Schema)
	require.NoError(t, os.WriteFile(filepath.Join(out, "version"), []byte(released), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(out, "schema.graphqls"), []byte(got.String()), 0o644))
}
