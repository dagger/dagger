package plancontext

import (
	bk "github.com/moby/buildkit/client"
)

type cacheConfigContext struct {
	exports []bk.CacheOptionsEntry
	imports []bk.CacheOptionsEntry
}

func (c *cacheConfigContext) Exports() []bk.CacheOptionsEntry {
	return c.exports
}

func (c *cacheConfigContext) Imports() []bk.CacheOptionsEntry {
	return c.exports
}

func (c *cacheConfigContext) SetExports(exports []bk.CacheOptionsEntry) {
	c.exports = exports
}

func (c *cacheConfigContext) SetImports(imports []bk.CacheOptionsEntry) {
	c.imports = imports
}
