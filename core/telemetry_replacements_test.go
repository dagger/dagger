package core

import (
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"gotest.tools/v3/assert"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

// An entry's replacement count is stamped beside its state once its value was
// replaced in place, and never while the count is 0.
func TestCacheStateAttrsReplacements(t *testing.T) {
	for _, attr := range cacheStateAttrs(dagql.CacheResultState{}) {
		assert.Assert(t, attr.Key != telemetryattrs.CacheReplacementsAttr, "a value never replaced carries no count")
	}
	mapped := cacheStateAttrs(dagql.CacheResultState{Replacements: 2})
	found := false
	for _, attr := range mapped {
		if attr.Key == attribute.Key(telemetryattrs.CacheReplacementsAttr) {
			found = true
			assert.Equal(t, "2", attr.Value.AsString())
		}
	}
	assert.Assert(t, found)
}

// A result the engine image ships is stamped so, and no other is.
func TestCacheStateAttrsImage(t *testing.T) {
	for _, attr := range cacheStateAttrs(dagql.CacheResultState{}) {
		assert.Assert(t, attr.Key != telemetryattrs.CacheImageAttr, "an entry the engine made carries no image fact")
	}
	found := false
	for _, attr := range cacheStateAttrs(dagql.CacheResultState{Image: true}) {
		if attr.Key == attribute.Key(telemetryattrs.CacheImageAttr) {
			found = true
			assert.Equal(t, "true", attr.Value.AsString())
		}
	}
	assert.Assert(t, found)
}
