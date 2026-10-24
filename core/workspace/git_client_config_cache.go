package workspace

import (
	"crypto/sha256"
	"sync"

	lru "github.com/hashicorp/golang-lru/v2"
)

const maxCachedGitClientConfigSize = 64 << 10

type gitClientDeclarations struct {
	scopes   map[string][]string
	warnings []string
}

// Bound retained contents and coalesce concurrent decoding without holding the
// LRU lock during parsing. A live tree read still precedes every lookup.
var gitClientConfigCache = func() *lru.Cache[[sha256.Size]byte, func() gitClientDeclarations] {
	cache, _ := lru.New[[sha256.Size]byte, func() gitClientDeclarations](32) // positive capacity
	return cache
}()

func cachedGitClientDeclarations(data []byte) gitClientDeclarations {
	if len(data) > maxCachedGitClientConfigSize {
		return parseGitClientDeclarations(data)
	}
	key := sha256.Sum256(data)
	if load, ok := gitClientConfigCache.Get(key); ok {
		return load()
	}
	load := sync.OnceValue(func() gitClientDeclarations {
		return parseGitClientDeclarations(data)
	})
	if existing, ok, _ := gitClientConfigCache.PeekOrAdd(key, load); ok {
		load = existing
	}
	return load()
}

func parseGitClientDeclarations(data []byte) gitClientDeclarations {
	cfg, warnings, err := parseConfigAt(data, ".")
	if err != nil {
		// Invalid configurations declare nothing, but warnings emitted before a
		// reconciliation error must still reach each caller.
		return gitClientDeclarations{warnings: warnings}
	}
	return gitClientDeclarations{scopes: ModuleScopeGitClients(cfg, "."), warnings: warnings}
}
