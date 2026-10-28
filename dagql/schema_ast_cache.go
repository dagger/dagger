package dagql

import (
	"sync"

	"github.com/dagger/dagger/dagql/call"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
)

// SchemaASTCache shares immutable GraphQL descriptions between independently
// installed servers. It contains no query root, resolvers, results, or clients.
// Its zero value is ready to use.
type SchemaASTCache struct {
	mu      sync.Mutex
	entries map[call.View]*schemaASTEntry
}

type schemaASTEntry struct {
	once   sync.Once
	schema *ast.Schema
	digest digest.Digest
}

func (c *SchemaASTCache) get(view call.View, build func(call.View) (*ast.Schema, digest.Digest)) *schemaASTEntry {
	c.mu.Lock()
	if c.entries == nil {
		c.entries = make(map[call.View]*schemaASTEntry)
	}
	entry := c.entries[view]
	if entry == nil {
		entry = &schemaASTEntry{}
		c.entries[view] = entry
	}
	c.mu.Unlock()
	// sync.Once does not retain build (and therefore the building server) after
	// it returns. Other servers retain only the immutable AST and its digest.
	entry.once.Do(func() { entry.schema, entry.digest = build(view) })
	return entry
}

// UseSchemaASTCache reuses descriptions of an exactly equivalent schema.
// Callers must establish equivalence of all installed metadata, including
// defaults and view filters, independently of root values and resolvers. A
// later install detaches only this server from the shared cache.
func (s *Server) UseSchemaASTCache(cache *SchemaASTCache) {
	if cache == nil {
		return
	}
	s.reconcileInterfaceImplsIfDirty()
	s.installLock.RLock()
	defer s.installLock.RUnlock()
	s.schemaLock.Lock()
	defer s.schemaLock.Unlock()
	s.schemaASTCache = cache
}
