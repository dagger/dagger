package core

import (
	lru "github.com/hashicorp/golang-lru/v2"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
)

// schemaASTs shares module sets' GraphQL descriptions across sessions. A
// description depends only on the modules' results, their install options
// and the core schema's view, which its key and the cache's own views cover,
// so every session installing the same set gets the same one. Each session
// otherwise rebuilt the descriptions of the sets earlier sessions had built.
//
// It holds at most 128 module sets, each with a description for each view a
// client asked for.
var schemaASTs, _ = lru.New[string, *dagql.SchemaASTCache](128) // error is impossible on positive size

// schemaASTCache shares only the GraphQL description, never a client's server.
// Authored defaults can expand the caller's host environment, so schemas with
// any .env values keep their private metadata cache.
func schemaASTCache(mods []modInstall) *dagql.SchemaASTCache {
	entries := make([]modDepEntry, len(mods))
	for i, entry := range mods {
		switch mod := entry.mod.(type) {
		case *userMod:
			if source := mod.res.Self().GetSource(); source != nil && source.UserDefaults != nil && len(source.UserDefaults.Environ) > 0 {
				return nil
			}
		case coreSchemaForker:
			// Core metadata varies by view, which the AST cache keys itself.
			if stable, ok := mod.(interface{ SchemaASTCacheKey() string }); !ok || stable.SchemaASTCacheKey() == "" {
				return nil
			}
		default:
			// Other module implementations may install context-dependent metadata.
			return nil
		}
		entries[i] = modDepEntry{mod: entry.mod, opts: entry.opts}
	}
	key, ok := schemaEntriesKey(entries, true)
	if !ok {
		return nil
	}
	if cache, ok := schemaASTs.Get(key); ok {
		return cache
	}
	cache := &dagql.SchemaASTCache{
		OnBuild: func(view call.View) { countDescription(key, view) },
	}
	if existing, ok, _ := schemaASTs.PeekOrAdd(key, cache); ok {
		return existing
	}
	return cache
}
