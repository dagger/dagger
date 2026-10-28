package core

import "github.com/dagger/dagger/dagql"

// schemaASTCache shares only the GraphQL description, never a client's server.
// Authored defaults can expand the caller's host environment, so schemas with
// any .env values keep their private metadata cache.
func (m *SchemaBuilderMemo) schemaASTCache(mods []modInstall) *dagql.SchemaASTCache {
	if m == nil {
		return nil
	}
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
		entries[i] = modDepEntry(entry)
	}
	key, ok := schemaEntriesKey(entries, true)
	if !ok {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.schemaASTs == nil {
		m.schemaASTs = make(map[string]*dagql.SchemaASTCache)
	}
	if m.schemaASTs[key] == nil {
		m.schemaASTs[key] = &dagql.SchemaASTCache{}
	}
	return m.schemaASTs[key]
}
